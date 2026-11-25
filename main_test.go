package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, yaml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "devwatch.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheck(t *testing.T) {
	p := writeConfig(t, `watch:
  - name: five
    cmd: echo 5
    unit: things
  - name: zero
    cmd: echo 0
    expect: "> 0"
  - name: broken
    cmd: echo nope >&2; exit 3
`)
	var out, errb bytes.Buffer
	if code := run([]string{"check", "-c", p}, &out, &errb); code != 1 {
		t.Fatalf("exit %d, want 1\n%s%s", code, &out, &errb)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	for i, want := range [][]string{{"NAME", "HEALTH"}, {"five", "number", "ok", "5 things"}, {"zero", "crit"}, {"broken", "error", "exit status 3: nope"}} {
		for _, w := range want {
			if i >= len(lines) || !strings.Contains(lines[i], w) {
				t.Errorf("line %d does not contain %q:\n%s", i, w, &out)
			}
		}
	}

	ok := writeConfig(t, "watch:\n  - name: five\n    cmd: echo 5\n")
	if code := run([]string{"check", "-c", ok}, &out, &errb); code != 0 {
		t.Errorf("all ok: exit %d", code)
	}
}

func TestUsage(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"-h"}, &out, &errb); code != 0 || !strings.Contains(errb.String(), "devwatch check") {
		t.Errorf("-h: exit %d\n%s", code, &errb)
	}
	if code := run([]string{"chek"}, &out, &errb); code != 2 {
		t.Errorf("unknown argument: exit %d", code)
	}
	if code := run([]string{"-c", filepath.Join(t.TempDir(), "missing.yaml")}, &out, &errb); code != 1 {
		t.Errorf("missing config: exit %d", code)
	}
}

func TestWatchConfig(t *testing.T) {
	p := writeConfig(t, "watch: []\n")
	changes := make(chan struct{}, 10)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchConfig(ctx, p, 10*time.Millisecond, func() { changes <- struct{}{} })
	time.Sleep(50 * time.Millisecond)
	select {
	case <-changes:
		t.Fatal("reported a change before any edit")
	default:
	}
	os.WriteFile(p, []byte("watch: [edited]\n"), 0o600)
	select {
	case <-changes:
	case <-time.After(2 * time.Second):
		t.Fatal("an edit went unnoticed")
	}
}

func TestPresetsAndInit(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"presets"}, &out, &errb); code != 0 || !strings.Contains(out.String(), "- postgres.count: users") {
		t.Errorf("presets: exit %d\n%s", code, &out)
	}
	p := filepath.Join(t.TempDir(), "devwatch.yaml")
	if code := run([]string{"init", "-c", p}, &out, &errb); code != 0 {
		t.Fatalf("init: exit %d\n%s", code, &errb)
	}
	os.WriteFile(p, []byte("mine"), 0o644)
	if code := run([]string{"init", "-c", p}, &out, &errb); code != 1 {
		t.Errorf("init over an existing file: exit %d", code)
	}
	if b, _ := os.ReadFile(p); string(b) != "mine" {
		t.Errorf("init replaced the existing file")
	}
}
