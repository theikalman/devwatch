// devwatch watches the state you care about while debugging: database values, containers, HTTP endpoints,
// command output. See docs/plan.md.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"text/tabwriter"

	"devwatch/internal/config"
	"devwatch/internal/engine"
	"devwatch/internal/source"
	"devwatch/internal/store"
	"devwatch/internal/value"
)

const usage = `devwatch watches local state while you debug.

Usage:
  devwatch [-c FILE]         run every watcher and print each change
  devwatch check [-c FILE]   run every watcher once and print the results; exit 1 on error or crit

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
	return watch(cfg, stdout, stderr)
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
				h = value.Health(v, w.WarnRule, w.CritRule, w.ExpectRule)
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

// watch runs the engine until interrupted and prints each change.
// shortcut: terminal output only; M2 serves the web UI from here.
func watch(cfg *config.Config, stdout, stderr io.Writer) int {
	st, err := store.Open(cfg.DB)
	if err != nil {
		fmt.Fprintln(stderr, "db:", err)
		return 1
	}
	defer st.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintf(stdout, "devwatch: %d watchers, history in %s. Ctrl+C to stop.\n", len(cfg.Watch), cfg.DB)
	var mu sync.Mutex
	err = engine.New(cfg, st, func(c engine.Check) {
		if !c.Sample.Changed {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(stdout, "%s  %-5s  %-24s  %s\n", c.Sample.At.Format("15:04:05"), c.Sample.Health, c.Watcher.Name, c.Sample.Summary)
	}).Run(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
