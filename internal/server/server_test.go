package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"devwatch/internal/config"
	"devwatch/internal/store"
	"devwatch/internal/value"
)

func setup(t *testing.T) (*Server, *store.Store, *config.Config, *atomic.Int32) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "devwatch.yaml")
	yaml := "title: test\nwatch:\n  - name: orders\n    cmd: TOKEN=abc echo 5\n    warn: \"> 3\"\n    env: {DB_PASSWORD: hunter2}\n"
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
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
	t.Cleanup(func() { st.Close() })
	// History from before a restart.
	id, _ := st.Watcher("orders", "orders", "cmd")
	n := 4.0
	if err := st.Insert(id, store.Sample{At: time.UnixMilli(1000), Health: value.Warn, Num: &n, Changed: true, Kind: value.Number, Value: []byte("4"), Summary: "4"}); err != nil {
		t.Fatal(err)
	}
	var runs atomic.Int32
	srv, err := New(cfg, p, st, func(key string) bool {
		if key != "orders" {
			return false
		}
		runs.Add(1)
		return true
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	return srv, st, cfg, &runs
}

// readEvent reads one server-sent event and returns its name and data.
func readEvent(t *testing.T, r *bufio.Reader) (string, string) {
	t.Helper()
	var name, data string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("reading event: %v", err)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "" && name != "":
			return name, data
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data = strings.TrimPrefix(line, "data: ")
		}
	}
}

func TestEventsSnapshotThenSamples(t *testing.T) {
	srv, _, _, _ := setup(t)
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
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	r := bufio.NewReader(resp.Body)

	name, data := readEvent(t, r)
	var snap struct {
		Title    string        `json:"title"`
		Config   string        `json:"config"`
		Watchers []watcherJSON `json:"watchers"`
	}
	if err := json.Unmarshal([]byte(data), &snap); name != "snapshot" || err != nil {
		t.Fatalf("first event %q: %v", name, err)
	}
	w := snap.Watchers[0]
	if snap.Title != "test" || snap.Config != "devwatch.yaml" || w.Key != "orders" || string(w.Value) != "4" || w.Last == nil || w.Last.Health != value.Warn || w.Rules["warn"] != "> 3" {
		t.Fatalf("snapshot = %+v", snap)
	}
	if strings.Contains(w.Spec+w.Target, "abc") || strings.Contains(w.Spec, "hunter2") {
		t.Fatalf("secret in spec %q / target %q", w.Spec, w.Target)
	}

	n := 5.0
	srv.Report("orders", store.Sample{At: time.UnixMilli(2000), Health: value.Warn, Num: &n, Changed: true, Kind: value.Number, Value: []byte("5"), Summary: "4 → 5 (+1)", Why: "it matches warn > 3"})
	name, data = readEvent(t, r)
	var ev sampleJSON
	if err := json.Unmarshal([]byte(data), &ev); name != "sample" || err != nil || ev.Key != "orders" || string(ev.Value) != "5" || ev.Why == "" {
		t.Fatalf("second event %q %s: %v", name, data, err)
	}
	// A new browser now gets the new value and the old one as prev.
	snapBytes, _, stop := srv.hub.subscribe()
	stop()
	if s := string(snapBytes); !strings.Contains(s, `"value":5`) || !strings.Contains(s, `"prev":4`) {
		t.Fatalf("snapshot after report: %s", s)
	}
}

func TestSlowBrowserIsDropped(t *testing.T) {
	srv, _, _, _ := setup(t)
	_, events, cancel := srv.hub.subscribe()
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			srv.Report("orders", store.Sample{At: time.UnixMilli(int64(i)), Health: value.OK})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Report blocked on a browser that doesn't read")
	}
	n := 0
	for range events {
		n++
	}
	if n == 0 || n > 256 {
		t.Fatalf("got %d events before the drop", n)
	}
}

func TestAPI(t *testing.T) {
	srv, _, _, runs := setup(t)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	do := func(method, path string, header map[string]string) *http.Response {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, nil)
		for k, v := range header {
			if k == "Host" {
				req.Host = v
			} else {
				req.Header.Set(k, v)
			}
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	same := map[string]string{"Sec-Fetch-Site": "same-origin"}
	if r := do("POST", "/api/watchers/orders/run", same); r.StatusCode != http.StatusAccepted || runs.Load() != 1 {
		t.Errorf("run now: %d, %d runs", r.StatusCode, runs.Load())
	}
	if r := do("POST", "/api/watchers/orders/run", map[string]string{"Sec-Fetch-Site": "cross-site"}); r.StatusCode != http.StatusForbidden || runs.Load() != 1 {
		t.Errorf("cross-site run: %d", r.StatusCode)
	}
	if r := do("POST", "/api/watchers/nope/run", same); r.StatusCode != http.StatusNotFound {
		t.Errorf("unknown watcher: %d", r.StatusCode)
	}
	if r := do("GET", "/", map[string]string{"Host": "evil.example:7777"}); r.StatusCode != http.StatusForbidden {
		t.Errorf("foreign host: %d", r.StatusCode)
	}
	if r := do("GET", "/", map[string]string{"Host": "localhost:7777"}); r.StatusCode != http.StatusOK || !strings.Contains(r.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Errorf("index: %d, CSP %q", r.StatusCode, r.Header.Get("Content-Security-Policy"))
	}

	var changes struct {
		Changes []struct{ Key, Summary string }
	}
	getJSON(t, ts.URL+"/api/changes", &changes)
	if len(changes.Changes) != 1 || changes.Changes[0].Key != "orders" || changes.Changes[0].Summary != "4" {
		t.Errorf("changes = %+v", changes)
	}
	var samples struct{ Samples []sampleJSON }
	getJSON(t, ts.URL+"/api/watchers/orders/samples?since=0", &samples)
	if len(samples.Samples) != 1 || string(samples.Samples[0].Value) != "4" {
		t.Errorf("samples = %+v", samples)
	}
	if r := do("GET", "/api/watchers/orders/samples?since=yesterday", nil); r.StatusCode != http.StatusBadRequest {
		t.Errorf("bad since: %d", r.StatusCode)
	}
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"AWS_SECRET_ACCESS_KEY=abc aws s3 ls":         "AWS_SECRET_ACCESS_KEY=•••• aws s3 ls",
		"AWS_ACCESS_KEY_ID=test aws s3 ls":            "AWS_ACCESS_KEY_ID=•••• aws s3 ls",
		`API_TOKEN="a b" curl x`:                      "API_TOKEN=•••• curl x",
		"psql postgres://app:s3cret@localhost:5432/x": "psql postgres://app:••••@localhost:5432/x",
		"pgrep -f autofsd | wc -l":                    "pgrep -f autofsd | wc -l",
	} {
		if got := redact(in); got != want {
			t.Errorf("redact(%q) = %q, want %q", in, got, want)
		}
	}
}
