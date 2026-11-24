package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, yaml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "devwatch.yaml")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadDefaults(t *testing.T) {
	p := write(t, `title: demo
watch:
  - name: "Order #42"
    cmd: echo 1
    warn: "> 3"
    dir: sub
  - name: db
    port: localhost:5432
    every: 1s
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(p)
	if c.Every != 5*time.Second || c.Timeout != 5*time.Second || c.Keep != 24*time.Hour {
		t.Errorf("defaults: every %s, timeout %s, keep %s", c.Every, c.Timeout, c.Keep)
	}
	if c.DB != filepath.Join(dir, ".devwatch", "data.db") {
		t.Errorf("db = %s", c.DB)
	}
	a, b := c.Watch[0], c.Watch[1]
	if a.Key != "order-42" || a.Source() != "cmd" || a.Dir != filepath.Join(dir, "sub") || a.WarnRule == nil || a.Every != 5*time.Second {
		t.Errorf("first watcher: %+v", a)
	}
	if b.Key != "db" || b.Source() != "port" || b.Every != time.Second || b.Dir != dir {
		t.Errorf("second watcher: %+v", b)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct{ yaml, want string }{
		{"", "file is empty"},
		{"watch: []", "no watchers"},
		{"watch:\n  - cmd: x", "name is required"},
		{"watch:\n  - name: '#'\n    cmd: x", "needs a letter or digit"},
		{"watch:\n  - name: a\n", "needs a source"},
		{"watch:\n  - name: a\n    cmd: x\n    port: h:1", "more than one source"},
		{"watch:\n  - name: a b\n    cmd: x\n  - name: A-B\n    cmd: y", `too close to "a b"`},
		{"watch:\n  - name: a\n    cmd: x\n    intervall: 2s", "intervall"},
		{"watch:\n  - name: a\n    cmd: x\n    warn: 10", "warn:"},
		{"watch:\n  - name: a\n    cmd: x\n    expect: '> x'", "needs a number"},
		{"watch:\n  - name: a\n    cmd: x\n    as: bool", `as: "bool"`},
		{"watch:\n  - name: a\n    cmd: x\n    match: '('", "match:"},
		{"watch:\n  - name: a\n    port: nohost", "port:"},
	}
	for _, tt := range tests {
		_, err := Load(write(t, tt.yaml))
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Load(%q) error = %v, want it to contain %q", tt.yaml, err, tt.want)
		}
	}
}
