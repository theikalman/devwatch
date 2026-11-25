package config

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPresetEntries(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://app@localhost/app")
	p := write(t, `vars:
  stack: shop
watch:
  - postgres.count: users
  - postgres.row: {table: orders, where: "id = 42"}
    name: "order #42"
    every: 1s
  - docker.compose: ${stack}
  - process: autofsd
    expect: "> 1"
  - git.branch:
  - http.health: {url: "http://localhost:8080/health", status: "200"}
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	ws := c.Watch
	if w := ws[0]; w.Name != "users count" || w.SQL != "SELECT count(*) FROM users" || w.DSN != "postgres://app@localhost/app" {
		t.Errorf("postgres.count: %+v", w)
	}
	if w := ws[1]; w.Name != "order #42" || w.SQL != "SELECT * FROM orders WHERE id = 42 LIMIT 1" || w.Every != time.Second {
		t.Errorf("postgres.row with overrides: %+v", w)
	}
	if w := ws[2]; w.Name != "shop stack" || w.Docker.Compose != "shop" {
		t.Errorf("docker.compose with ${stack}: %+v", w)
	}
	if w := ws[3]; w.Env["DEVWATCH_PATTERN"] != "autofsd" || w.Expect != "> 1" || w.ExpectRule == nil {
		t.Errorf("process with expect override: %+v", w)
	}
	if w := ws[4]; w.Name != "git branch" || w.Dir != filepath.Dir(p) {
		t.Errorf("git.branch with no value: %+v", w)
	}
	if w := ws[5]; w.Name != "localhost:8080/health" || w.Status != "200" {
		t.Errorf("http.health: %+v", w)
	}

	// Every example in the catalog loads as written, and so does the starter config.
	var all strings.Builder
	all.WriteString("watch:\n")
	for i, pr := range Presets() {
		fmt.Fprintf(&all, "  %s\n    name: example %d\n", pr.Example, i)
	}
	if _, err := Load(write(t, all.String())); err != nil {
		t.Errorf("catalog examples: %v", err)
	}
	if _, err := Load(write(t, string(Starter))); err != nil {
		t.Errorf("starter config: %v", err)
	}
}

func TestVars(t *testing.T) {
	t.Setenv("DEVWATCH_TEST_HOST", "db.local")
	c, err := Load(write(t, "vars: {port: \"5432\"}\nwatch:\n  - name: db\n    port: ${DEVWATCH_TEST_HOST}:${port}\n  - name: shell\n    cmd: echo $${HOME} ${DEVWATCH_TEST_HOST}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Watch[0].Port != "db.local:5432" || c.Watch[1].Cmd != "echo ${HOME} db.local" {
		t.Errorf("expanded: %q, %q", c.Watch[0].Port, c.Watch[1].Cmd)
	}
}

func TestErrorPositions(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	tests := []struct{ yaml, want string }{
		{"watch:\n  - name: a\n    cmd: x\n    intervall: 2s", `:4:5: watcher "a": unknown field "intervall"`},
		{"title: x\nwatchers: []", `:2:1: unknown setting "watchers"`},
		{"watch:\n  - name: a\n    cmd: echo ${NOPE_DEVWATCH}", `:3:10: ${NOPE_DEVWATCH} is not in vars: or the environment`},
		{"watch:\n  - postgres.cout: users", `:2:5: unknown preset "postgres.cout"; devwatch presets lists them`},
		{"watch:\n  - postgres.count: {table: t, were: x}", `:2:32: postgres.count: no setting "were"`},
		{"watch:\n  - postgres.row: orders", `:2:19: postgres.row: takes settings`},
		{"watch:\n  - http.json: {url: \"http://x\"}", `http.json: needs path`},
		{"watch:\n  - process: a\n    docker.compose: b", `:3:5: one preset per watcher`},
		{"watch:\n  - name: b\n    cmd: x\n  - name: a\n    cmd: x\n    warn: 10", `:4:5: watcher "a": warn:`},
		{"watch:\n  - name: a\n    warn: > 18\n    cmd: x", `:3: did not find expected comment or line break (a value starting with > or | needs quotes`},
		{"watch:\n  - name: a\n    cmd: printf '{\"a\": 1}'", `:3: mapping values are not allowed in this context (a value containing ": "`},
	}
	for _, tt := range tests {
		p := write(t, tt.yaml)
		_, err := Load(p)
		if err == nil || !strings.HasPrefix(err.Error(), p) || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Load(%q) error = %v\nwant it to contain %q", tt.yaml, err, tt.want)
		}
	}
}
