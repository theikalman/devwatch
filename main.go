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

Flags:
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	sub := ""
	if len(args) > 0 && args[0] == "check" {
		sub, args = "check", args[1:]
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
	eng := engine.New(cfg, st, func(c engine.Check) {
		srv.Report(c.Watcher.Key, c.Sample)
		if !c.Sample.Changed {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(stdout, "%s  %-5s  %-24s  %s\n", c.Sample.At.Format("15:04:05"), c.Sample.Health, c.Watcher.Name, c.Sample.Summary)
	})
	tcp, _ := ln.Addr().(*net.TCPAddr)
	if srv, err = server.New(cfg, path, st, eng.RunNow, tcp != nil && tcp.IP.IsLoopback()); err != nil {
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
	done := make(chan error, 1)
	go func() { done <- eng.Run(ctx) }()
	select {
	case <-ctx.Done():
		err = <-done
	case err = <-done: // the engine could not start
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	hs.Shutdown(shutdown)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
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
