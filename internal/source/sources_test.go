package source

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"devwatch/internal/config"
	"devwatch/internal/value"
)

func fetch(t *testing.T, w config.Watcher) (value.Value, error) {
	t.Helper()
	w.Timeout = 5 * time.Second
	return Fetch(context.Background(), w)
}

func TestSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE orders (id INTEGER PRIMARY KEY, status TEXT, note TEXT, payload TEXT);
		INSERT INTO orders VALUES (1, 'paid', NULL, '{"kind": "card"}'), (2, 'pending', 'x', '{}'), (3, 'pending', NULL, '{}');`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	dsn := "sqlite:" + path
	tests := []struct {
		query, as, value string
		kind             value.Kind
		want             string
	}{
		{"SELECT count(*) FROM orders", "", "", value.Number, "3"},
		{"SELECT id, status, note FROM orders WHERE id = 1", "", "", value.Row, "id=1 status=paid note=NULL"},
		{"SELECT id, status FROM orders ORDER BY id", "", "", value.Table, "3 rows"},
		{"SELECT id, status FROM orders WHERE id = 9", "", "", value.Table, "0 rows"},
		{"SELECT id, payload FROM orders WHERE id = 1", "", "payload.kind", value.Text, "card"},
		{"SELECT status FROM orders WHERE id = 1", "table", "", value.Table, "1 row"},
	}
	for _, tt := range tests {
		v, err := fetch(t, config.Watcher{SQL: tt.query, DSN: dsn, As: tt.as, Value: tt.value})
		if err != nil || v.Kind != tt.kind || v.String() != tt.want {
			t.Errorf("%s: got %s %q, %v; want %s %q", tt.query, v.Kind, v.String(), err, tt.kind, tt.want)
		}
	}
	for query, want := range map[string]string{
		"UPDATE orders SET status = 'x'": "readonly",
		"SELECT id FROM orders":          "as number",
		"SELECT nope FROM orders":        "no such column",
	} {
		as := ""
		if query == "SELECT id FROM orders" {
			as = "number"
		}
		if _, err := fetch(t, config.Watcher{SQL: query, DSN: dsn, As: as}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: error %v, want it to contain %q", query, err, want)
		}
	}
	if _, err := fetch(t, config.Watcher{SQL: "SELECT 1", DSN: "sqlite:" + filepath.Join(t.TempDir(), "missing.db")}); err == nil {
		t.Error("a missing SQLite file was created instead of reported")
	}
}

// TestPostgres runs against a real server: DEVWATCH_TEST_PG=postgres://user:pass@localhost:5432/db.
func TestPostgres(t *testing.T) {
	dsn := os.Getenv("DEVWATCH_TEST_PG")
	if dsn == "" {
		t.Skip("set DEVWATCH_TEST_PG to run")
	}
	if v, err := fetch(t, config.Watcher{SQL: "SELECT 41 + 1", DSN: dsn}); err != nil || v.String() != "42" {
		t.Fatalf("SELECT 41 + 1 = %q, %v", v.String(), err)
	}
	if v, err := fetch(t, config.Watcher{SQL: "SELECT now() AS at, NULL::text AS note, '{\"a\":1}'::jsonb AS doc", DSN: dsn}); err != nil || v.Kind != value.Row {
		t.Fatalf("row: %s %v", v.Kind, err)
	}
	if _, err := fetch(t, config.Watcher{SQL: "CREATE TABLE devwatch_should_not_exist (i int)", DSN: dsn}); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("a write was allowed: %v", err)
	}
}

func TestProcessPreset(t *testing.T) {
	tag := fmt.Sprintf("devwatch-proc-test-%d", os.Getpid())
	p := filepath.Join(t.TempDir(), "devwatch.yaml")
	if err := os.WriteFile(p, []byte("watch:\n  - process: "+tag+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	count := func() string {
		v, err := Fetch(context.Background(), cfg.Watch[0])
		if err != nil {
			t.Fatal(err)
		}
		return v.String()
	}
	if got := count(); got != "0" {
		t.Fatalf("before: %s; the pipeline counts itself", got)
	}
	proc := exec.Command("sh", "-c", "sleep 30; :", tag) // tag becomes sh's $0, in its command line
	if err := proc.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { proc.Process.Kill(); proc.Wait() }()
	if got := count(); got != "1" {
		t.Fatalf("with one matching process: %s", got)
	}
}

func TestHTTP(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ok", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"build": {"sha": "abc"}}`)) })
	mux.HandleFunc("/down", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "maintenance", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(r.Method + " " + r.Header.Get("X-Test"))) })
	ts := httptest.NewServer(mux)
	defer ts.Close()
	get := func(path string) config.Watcher {
		return config.Watcher{HTTP: ts.URL + path, Method: "GET", Status: "2xx"}
	}

	v, err := fetch(t, get("/ok"))
	if p := v.Data.(value.ProbeData); err != nil || !p.Up || p.Code != 200 || v.Num == nil {
		t.Errorf("up probe: %+v, %v", v, err)
	}
	v, err = fetch(t, get("/down"))
	if p := v.Data.(value.ProbeData); err != nil || p.Up || p.Reason != "status 503" {
		t.Errorf("down probe: %+v, %v", v, err)
	}
	w := get("/missing")
	w.Status = "404"
	if v, err := fetch(t, w); err != nil || !v.Data.(value.ProbeData).Up {
		t.Errorf("status 404 expected: %+v, %v", v, err)
	}

	w = get("/ok")
	w.Value = "build.sha"
	if v, err := fetch(t, w); err != nil || v.String() != "abc" {
		t.Errorf("value: %q, %v", v.String(), err)
	}
	w = get("/down")
	w.Value = "build.sha"
	if _, err := fetch(t, w); err == nil || err.Error() != "HTTP 503 Service Unavailable: maintenance" {
		t.Errorf("value on a 503: %v", err)
	}
	w = get("/echo")
	w.Method, w.Headers, w.As = "POST", map[string]string{"X-Test": "yes"}, "text"
	if v, err := fetch(t, w); err != nil || v.String() != "POST yes" {
		t.Errorf("method and headers: %q, %v", v.String(), err)
	}

	ts.Close()
	v, err = fetch(t, get("/ok"))
	if p := v.Data.(value.ProbeData); err != nil || p.Up || !strings.Contains(p.Reason, "refused") {
		t.Errorf("closed server: %+v, %v", v, err)
	}
}

// fakeDocker serves the two Engine API calls devwatch makes, on a unix socket.
func fakeDocker(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "dw") // short: unix socket paths are limited to ~100 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	type item struct {
		ID     string `json:"Id"`
		Names  []string
		Image  string
		Labels map[string]string
	}
	all := []item{
		{"c1", []string{"/shop-api-1"}, "api:dev", map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "api"}},
		{"c2", []string{"/shop-redis-1"}, "redis:7", map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "redis"}},
		{"c3", []string{"/api-old"}, "api:old", nil},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /containers/json", func(w http.ResponseWriter, r *http.Request) {
		var f map[string][]string
		json.Unmarshal([]byte(r.URL.Query().Get("filters")), &f)
		var out []item
		for _, it := range all {
			switch {
			case len(f["label"]) == 1 && f["label"][0] == "com.docker.compose.project=shop" && it.Labels["com.docker.compose.project"] == "shop":
				out = append(out, it)
			case len(f["name"]) == 1 && strings.Contains(it.Names[0], f["name"][0]):
				out = append(out, it)
			}
		}
		json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /containers/{id}/json", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("id") {
		case "c1":
			w.Write([]byte(`{"RestartCount": 1, "State": {"Status": "running", "StartedAt": "2026-11-24T12:00:00Z", "FinishedAt": "0001-01-01T00:00:00Z", "Health": {"Status": "healthy"}}}`))
		case "c2":
			w.Write([]byte(`{"RestartCount": 0, "State": {"Status": "exited", "ExitCode": 137, "StartedAt": "2026-11-24T11:00:00Z", "FinishedAt": "2026-11-24T12:03:00Z"}}`))
		default:
			w.Write([]byte(`{"State": {"Status": "running", "StartedAt": "2026-11-24T10:00:00Z"}}`))
		}
	})
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	return sock
}

func TestDocker(t *testing.T) {
	sock := fakeDocker(t)
	v, err := fetch(t, config.Watcher{Docker: &config.Docker{Compose: "shop"}, Socket: sock})
	if err != nil {
		t.Fatal(err)
	}
	cs := v.Data.([]value.ContainerData)
	if v.String() != "1/2 running" || cs[0].Name != "api" || cs[0].Health != "healthy" || cs[0].Restarts != 1 || cs[0].Finished != 0 ||
		cs[1].Name != "redis" || cs[1].ExitCode != 137 || cs[1].Finished == 0 {
		t.Fatalf("compose: %s %+v", v, cs)
	}
	if h, why := value.Health(v, nil, nil, nil); h != value.Crit || why != "redis exited (137)" {
		t.Errorf("health %s %q", h, why)
	}

	// The name filter matches substrings; only the exact name is kept.
	v, err = fetch(t, config.Watcher{Docker: &config.Docker{Container: "api-old"}, Socket: sock})
	if cs := v.Data.([]value.ContainerData); err != nil || len(cs) != 1 || cs[0].Name != "api-old" {
		t.Fatalf("container: %+v, %v", v, err)
	}
	v, _ = fetch(t, config.Watcher{Docker: &config.Docker{Container: "api"}, Socket: sock})
	if h, why := value.Health(v, nil, nil, nil); len(v.Data.([]value.ContainerData)) != 0 || h != value.Crit || why != "no container matches" {
		t.Errorf("no exact match: %+v %s %q", v, h, why)
	}

	if _, err := fetch(t, config.Watcher{Docker: &config.Docker{Compose: "shop"}, Socket: filepath.Join(filepath.Dir(sock), "none.sock")}); err == nil || !strings.Contains(err.Error(), "docker at") {
		t.Errorf("missing socket: %v", err)
	}
	t.Setenv("DOCKER_HOST", "tcp://10.0.0.1:2375")
	if _, err := fetch(t, config.Watcher{Docker: &config.Docker{Compose: "shop"}}); err == nil || !strings.Contains(err.Error(), "only unix://") {
		t.Errorf("tcp DOCKER_HOST: %v", err)
	}
}
