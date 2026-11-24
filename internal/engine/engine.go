// Package engine runs every watcher on its interval, records each check and reports it.
package engine

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/rand/v2"
	"sync"
	"time"

	"devwatch/internal/config"
	"devwatch/internal/source"
	"devwatch/internal/store"
	"devwatch/internal/value"
)

// Check is one finished check, as reported to the caller.
type Check struct {
	Watcher *config.Watcher
	Sample  store.Sample
}

type Engine struct {
	cfg    *config.Config
	st     *store.Store
	report func(Check) // called from the watchers' goroutines
}

func New(cfg *config.Config, st *store.Store, report func(Check)) *Engine {
	return &Engine{cfg, st, report}
}

// Run checks every watcher until ctx is cancelled, pruning history older than cfg.Keep every 5 minutes.
func (e *Engine) Run(ctx context.Context) error {
	ids := make([]int64, len(e.cfg.Watch))
	trackers := make([]*tracker, len(e.cfg.Watch))
	for i := range e.cfg.Watch {
		w := &e.cfg.Watch[i]
		id, err := e.st.Watcher(w.Key, w.Name, w.Source())
		if err != nil {
			return err
		}
		last, err := e.st.Last(id)
		if err != nil {
			return err
		}
		if trackers[i], err = resume(last); err != nil {
			return fmt.Errorf("watcher %q: stored value: %w", w.Name, err)
		}
		ids[i] = id
	}
	var wg sync.WaitGroup
	for i := range e.cfg.Watch {
		wg.Go(func() { e.loop(ctx, &e.cfg.Watch[i], ids[i], trackers[i]) })
	}
	e.prune()
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case <-tick.C:
			e.prune()
		}
	}
}

func (e *Engine) loop(ctx context.Context, w *config.Watcher, id int64, t *tracker) {
	timer := time.NewTimer(rand.N(min(500*time.Millisecond, w.Every))) // spread the first checks out
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		start := time.Now()
		v, err := source.Fetch(ctx, *w)
		if ctx.Err() != nil {
			return // shutting down: the check was cut short, so don't record it
		}
		smp := t.next(v, err, w, start, time.Since(start).Milliseconds())
		if err := e.st.Insert(id, smp); err != nil {
			log.Printf("watcher %q: store: %v", w.Name, err)
		}
		e.report(Check{w, smp})
		timer.Reset(w.Every)
	}
}

func (e *Engine) prune() {
	if _, err := e.st.Prune(time.Now().Add(-e.cfg.Keep)); err != nil {
		log.Printf("prune: %v", err)
	}
}

// tracker remembers a watcher's previous sample to decide what changed.
type tracker struct {
	seen   bool
	health string
	err    string
	raw    []byte       // canonical JSON of val
	val    *value.Value // last good value
}

func resume(l store.Last) (*tracker, error) {
	t := &tracker{seen: l.Found, health: l.Health, err: l.Error}
	if l.Value != nil {
		v, err := value.Decode(l.Kind, l.Value)
		if err != nil {
			return nil, err
		}
		t.raw, t.val = l.Value, &v
	}
	return t, nil
}

// next turns one check into a sample. The value is kept only when it differs from the last good one;
// the sample counts as changed when the value, the health or the error differs.
func (t *tracker) next(v value.Value, ferr error, w *config.Watcher, at time.Time, ms int64) store.Sample {
	s := store.Sample{At: at, MS: ms}
	if ferr != nil {
		s.Health, s.Error = value.Error, ferr.Error()
	} else {
		s.Health, s.Num = value.Health(v, w.WarnRule, w.CritRule, w.ExpectRule), v.Num
		if raw := v.JSON(); t.val == nil || v.Kind != t.val.Kind || !bytes.Equal(raw, t.raw) {
			s.Kind, s.Value = v.Kind, raw
		}
	}
	s.Changed = !t.seen || s.Value != nil || s.Health != t.health || s.Error != t.err
	if s.Changed {
		switch {
		case s.Error != "":
			s.Summary = s.Error
		case s.Value != nil:
			s.Summary = value.Summary(t.val, v)
		default:
			s.Summary = t.health + " → " + s.Health
		}
	}
	t.seen, t.health, t.err = true, s.Health, s.Error
	if s.Value != nil {
		t.raw, t.val = s.Value, &v
	}
	return s
}
