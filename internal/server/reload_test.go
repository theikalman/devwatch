package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devwatch/internal/config"
)

func TestReloadAndConfigError(t *testing.T) {
	srv, _, cfg, _ := setup(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	readEvent(t, r) // the first snapshot

	srv.ConfigError("devwatch.yaml:3:5: unknown field \"intervall\"")
	if name, data := readEvent(t, r); name != "config_error" || !strings.Contains(data, "intervall") {
		t.Fatalf("got %s %s", name, data)
	}
	snap, _, stop := srv.hub.subscribe()
	stop()
	if !strings.Contains(string(snap), `"config_error":"devwatch.yaml:3:5`) {
		t.Fatalf("a new browser doesn't see the error: %s", snap)
	}

	p := filepath.Join(cfg.Dir, "devwatch.yaml")
	if err := os.WriteFile(p, []byte("title: renamed\nwatch:\n  - name: fresh\n    cmd: echo 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	next, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Reload(next); err != nil {
		t.Fatal(err)
	}
	name, data := readEvent(t, r)
	if name != "snapshot" || !strings.Contains(data, `"title":"renamed"`) || !strings.Contains(data, `"key":"fresh"`) ||
		strings.Contains(data, `"key":"orders"`) || strings.Contains(data, "config_error") {
		t.Fatalf("after reload: %s %s", name, data)
	}
	if r, _ := http.Get(ts.URL + "/api/watchers/orders/samples"); r.StatusCode != http.StatusNotFound {
		t.Errorf("removed watcher still answers: %d", r.StatusCode)
	}
}
