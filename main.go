// devwatch watches the state you care about while debugging: database values, containers, HTTP endpoints,
// command output. See docs/plan.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"text/tabwriter"
	"time"

	"devwatch/internal/config"
	"devwatch/internal/engine"
	"devwatch/internal/server"
	"devwatch/internal/source"
	"devwatch/internal/store"
	"devwatch/internal/value"
)

const usage = `devwatch watches local state while you debug.

Usage:
  devwatch [-c FILE] [--addr ADDR] [--open]   run every watcher and serve the live dashboard
  devwatch check [-c FILE]                    run every watcher once and print the results; exit 1 on error or crit
  devwatch presets                            list the presets and their settings
  devwatch init [-c FILE]                     write a starter config (never over an existing file)

Flags:
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	sub := ""
	if len(args) > 0 && (args[0] == "check" || args[0] == "presets" || args[0] == "init") {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("devwatch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, usage); fs.PrintDefaults() }
	path := fs.String("c", "devwatch.yaml", "config file")
	addr := fs.String("addr", "127.0.0.1:7777", "address for the dashboard; anything but loopback exposes it to your network")
	open := fs.Bool("open", false, "open the dashboard in the browser")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unknown argument %q\n\n", fs.Arg(0))
		fs.Usage()
		return 2
	}
	switch sub {
	case "presets":
		return listPresets(stdout)
	case "init":
		return initConfig(*path, stdout, stderr)
	}
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if sub == "check" {
		return check(cfg, stdout)
	}
	return serve(cfg, *path, *addr, *open, stdout, stderr)
}

func listPresets(out io.Writer) int {
	fmt.Fprint(out, "Presets: write one as a watcher entry; settings next to it (name, warn, every…) override its own.\n\n")
	for _, p := range config.Presets() {
		fmt.Fprintf(out, "%s\n    %s\n    %s\n    takes: %s\n\n", p.Name, p.Doc, p.Example, p.ParamList())
	}
	return 0
}

// initConfig writes the starter config; it never replaces a file.
func initConfig(path string, stdout, stderr io.Writer) int {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		fmt.Fprintln(stderr, "init:", err)
		return 1
	}
	_, err = f.Write(config.Starter)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		fmt.Fprintln(stderr, "init:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Wrote %s. Edit it, then run devwatch -c %s\n", path, path)
	return 0
}

// check runs every watcher once, all at the same time, and prints one line each in config order.
func check(cfg *config.Config, out io.Writer) int {
	type result struct {
		v      value.Value
		err    error
		health string
	}
	results := make([]result, len(cfg.Watch))
	var wg sync.WaitGroup
	for i := range cfg.Watch {
		wg.Go(func() {
			w := &cfg.Watch[i]
			v, err := source.Fetch(context.Background(), *w)
			h := value.Error
			if err == nil {
				h, _ = value.Health(v, w.WarnRule, w.CritRule, w.ExpectRule)
			}
			results[i] = result{v, err, h}
		})
	}
	wg.Wait()

	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tKIND\tHEALTH\tVALUE")
	code := 0
	for i, r := range results {
		w := cfg.Watch[i]
		kind, shown := string(r.v.Kind), r.v.String()
		if r.err != nil {
			kind, shown = "-", r.err.Error()
		} else if w.Unit != "" {
			shown += " " + w.Unit
		}
		if r.health == value.Error || r.health == value.Crit {
			code = 1
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", w.Name, kind, r.health, shown)
	}
	tw.Flush()
	return code
}

// serve runs every watcher and serves the dashboard until interrupted. Each change is also printed.
func serve(cfg *config.Config, path, addr string, open bool, stdout, stderr io.Writer) int {
	st, err := store.Open(cfg.DB)
	if err != nil {
		fmt.Fprintln(stderr, "db:", err)
		return 1
	}
	defer st.Close()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Fprintf(stderr, "listen: %v\n(is devwatch already running? pick another address with --addr)\n", err)
		return 1
	}

	var srv *server.Server
	var mu sync.Mutex
	report := func(c engine.Check) {
		srv.Report(c.Watcher.Key, c.Sample)
		if !c.Sample.Changed {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(stdout, "%s  %-5s  %-24s  %s\n", c.Sample.At.Format("15:04:05"), c.Sample.Health, c.Watcher.Name, c.Sample.Summary)
	}
	var eng atomic.Pointer[engine.Engine] // replaced on each reload; run-now asks the current one
	eng.Store(engine.New(cfg, st, report))
	runNow := func(key string) bool { return eng.Load().RunNow(key) }
	tcp, _ := ln.Addr().(*net.TCPAddr)
	if srv, err = server.New(cfg, path, st, runNow, tcp != nil && tcp.IP.IsLoopback()); err != nil {
		ln.Close()
		fmt.Fprintln(stderr, err)
		return 1
	}
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}
	hs.RegisterOnShutdown(srv.Close) // event streams never go idle on their own
	go hs.Serve(ln)

	url := "http://" + ln.Addr().String()
	if tcp != nil && tcp.IP.IsUnspecified() {
		url = fmt.Sprintf("http://localhost:%d", tcp.Port)
	}
	fmt.Fprintf(stdout, "devwatch: %d watchers on %s · history in %s · Ctrl+C to stop\n", len(cfg.Watch), url, cfg.DB)
	if open {
		openBrowser(url, stderr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	reloads := make(chan *config.Config)
	go watchConfig(ctx, path, time.Second, func() {
		next, err := config.Load(path)
		if err != nil {
			fmt.Fprintln(stderr, "config not reloaded:", err)
			srv.ConfigError(err.Error())
			return
		}
		next.DB = cfg.DB // the open database stays; a new db: takes effect on restart
		select {
		case reloads <- next:
		case <-ctx.Done():
		}
	})
	err = runEngines(ctx, eng.Load(), reloads, func(next *config.Config) (*engine.Engine, error) {
		if err := srv.Reload(next); err != nil {
			return nil, err
		}
		e := engine.New(next, st, report)
		eng.Store(e)
		fmt.Fprintf(stdout, "devwatch: reloaded %s, %d watchers\n", path, len(next.Watch))
		return e, nil
	})
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hs.Shutdown(shutdown)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// runEngines runs e until ctx ends. On each reload it stops e, waits for its checks to finish, and runs the
// engine swap returns. shortcut: every watcher restarts on any edit; fine, since each resumes from the store.
func runEngines(ctx context.Context, e *engine.Engine, reloads <-chan *config.Config, swap func(*config.Config) (*engine.Engine, error)) error {
	for {
		run, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- e.Run(run) }()
		select {
		case <-ctx.Done():
			stop()
			return <-done
		case err := <-done: // the engine could not start
			stop()
			return err
		case next := <-reloads:
			stop()
			if err := <-done; err != nil {
				return err
			}
			var err error
			if e, err = swap(next); err != nil {
				return err
			}
		}
	}
}

// watchConfig calls changed whenever path's size or modification time changes, checking every interval.
func watchConfig(ctx context.Context, path string, every time.Duration, changed func()) {
	stamp := func() string {
		fi, err := os.Stat(path)
		if err != nil {
			return "" // mid-save by an editor that replaces the file; try again next tick
		}
		return fmt.Sprint(fi.Size(), fi.ModTime().UnixNano())
	}
	last := stamp()
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if now := stamp(); now != "" && now != last {
				last = now
				changed()
			}
		}
	}
}

func openBrowser(url string, stderr io.Writer) {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	cmd := exec.Command(name, url)
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(stderr, "open browser: %v\n", err)
		return
	}
	go cmd.Wait()
}
