package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"devwatch/internal/config"
	"devwatch/internal/watcher"
)

const maxLines = 1000 // lines kept in memory per stream pane

type streamLine struct {
	idx  int
	line string
	at   time.Time
}

type streamExit struct {
	idx int
	err error
}

// startStreams runs every `stream` watcher in its own goroutine, sending events to ch.
// Callers cancel ctx and then wg.Wait() so child processes are killed before exit.
func startStreams(ctx context.Context, wg *sync.WaitGroup, cfg *config.Config, ch chan<- tea.Msg) {
	send := func(m tea.Msg) {
		select {
		case ch <- m:
		case <-ctx.Done():
		}
	}
	for i, w := range cfg.Watchers {
		if w.Type != "stream" {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			var logf *os.File
			if cfg.LogDir != "" {
				var err error
				if logf, err = openLog(cfg.LogDir, w.Name); err != nil {
					send(streamLine{i, "log: " + err.Error(), time.Now()})
				} else {
					defer logf.Close()
				}
			}
			emit := func(line string) {
				at := time.Now()
				if logf != nil {
					fmt.Fprintf(logf, "[%s] %s\n", at.Format("2006-01-02 15:04:05"), line)
				}
				send(streamLine{i, line, at})
			}
			for {
				err := watcher.Stream(ctx, w, emit)
				send(streamExit{i, err})
				if w.Restart <= 0 {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(w.Restart):
				}
				marker := "── restarted " + time.Now().Format("15:04:05")
				if err != nil {
					marker += " (previous run: " + err.Error() + ")"
				}
				emit(marker + " ──")
			}
		}()
	}
}

// waitEvent blocks for the next stream event; Update re-arms it after each one.
func waitEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}
