package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
