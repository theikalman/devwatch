package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"devwatch/internal/config"
	"devwatch/internal/store"
	"devwatch/internal/value"
)

func TestTrackerNext(t *testing.T) {
	warn, _ := value.ParseRule("> 18")
	w := &config.Watcher{WarnRule: warn}
	tr := &tracker{}
	boom := errors.New("boom")
	steps := []struct {
		v       value.Value
		err     error
		changed bool
		stored  bool
		health  string
		summary string
	}{
		{value.NewNumber(17), nil, true, true, value.OK, "17"},
		{value.NewNumber(17), nil, false, false, value.OK, ""},
		{value.NewNumber(20), nil, true, true, value.Warn, "17 → 20 (+3)"},
		{value.Value{}, boom, true, false, value.Error, "boom"},
		{value.Value{}, boom, false, false, value.Error, ""},
		{value.NewNumber(20), nil, true, false, value.Warn, "error → warn"},
		{value.NewText("20"), nil, true, true, value.Warn, "20"}, // same JSON, other kind
	}
	for i, st := range steps {
		s := tr.next(st.v, st.err, w, time.Now(), 1)
		if s.Changed != st.changed || (s.Value != nil) != st.stored || s.Health != st.health || s.Summary != st.summary {
			t.Errorf("step %d: changed=%v stored=%v health=%s summary=%q; want %v %v %s %q",
				i, s.Changed, s.Value != nil, s.Health, s.Summary, st.changed, st.stored, st.health, st.summary)
		}
	}
}

func TestRunRecordsAndResumes(t *testing.T) {
	p := filepath.Join(t.TempDir(), "devwatch.yaml")
	if err := os.WriteFile(p, []byte("every: 20ms\nwatch:\n  - name: five\n    cmd: echo 5\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.DB)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	run := func() []Check {
		var mu sync.Mutex
		var checks []Check
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		err := New(cfg, st, func(c Check) { mu.Lock(); checks = append(checks, c); mu.Unlock() }).Run(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return checks
	}

	first := run()
	if len(first) < 2 {
		t.Fatalf("%d checks in 300ms", len(first))
	}
	if s := first[0].Sample; !s.Changed || s.Summary != "5" || s.Health != value.OK {
		t.Errorf("first check: %+v", s)
	}
	for _, c := range first[1:] {
		if c.Sample.Changed {
			t.Errorf("unchanged value reported as a change: %+v", c.Sample)
		}
	}
	for _, c := range run() {
		if c.Sample.Changed {
			t.Errorf("after restart, unchanged value reported as a change: %+v", c.Sample)
		}
	}
}
