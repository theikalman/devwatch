package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"devwatch/internal/value"
)

func open(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func (s *Store) count(t *testing.T, id int64) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sample WHERE watcher_id = ?`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOpenKeepsDataPrivate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".devwatch", "data.db")
	open(t, path)
	if b, err := os.ReadFile(filepath.Join(root, ".devwatch", ".gitignore")); err != nil || string(b) != "*\n" {
		t.Errorf(".gitignore = %q, %v", b, err)
	}
	for p, want := range map[string]os.FileMode{path: 0o600, filepath.Dir(path): 0o700} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: mode %v, %v; want %v", p, fi.Mode().Perm(), err, want)
		}
	}

	// An existing folder is the user's: no .gitignore that would hide it from git.
	open(t, filepath.Join(root, "data.db"))
	if _, err := os.Stat(filepath.Join(root, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("wrote .gitignore into an existing folder: %v", err)
	}
}

func TestInsertAndLast(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "data.db"))
	id, err := s.Watcher("orders", "orders", "cmd")
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.Watcher("orders", "Orders", "cmd"); err != nil || again != id {
		t.Fatalf("same key gave id %d, %v; want %d", again, err, id)
	}
	if l, err := s.Last(id); err != nil || l.Found {
		t.Fatalf("empty Last = %+v, %v", l, err)
	}
	n := 17.0
	t0 := time.UnixMilli(1_000)
	must(t, s.Insert(id, Sample{At: t0, Health: value.OK, Num: &n, Changed: true, Kind: value.Number, Value: []byte("17"), Summary: "17"}))
	must(t, s.Insert(id, Sample{At: t0.Add(time.Second), Health: value.Warn, Num: &n, Changed: true, Summary: "ok → warn"}))
	l, err := s.Last(id)
	if err != nil || !l.Found || l.Health != value.Warn || l.Kind != value.Number || string(l.Value) != "17" {
		t.Fatalf("Last = %+v, %v", l, err)
	}

	// A value of another kind drops the old rows.
	must(t, s.Insert(id, Sample{At: t0.Add(2 * time.Second), Health: value.OK, Changed: true, Kind: value.Text, Value: []byte(`"x"`)}))
	if got := s.count(t, id); got != 1 {
		t.Fatalf("%d rows after kind change, want 1", got)
	}
}

func TestPruneKeepsNewestValue(t *testing.T) {
	s := open(t, filepath.Join(t.TempDir(), "data.db"))
	id, _ := s.Watcher("w", "w", "cmd")
	at := func(sec int) time.Time { return time.Unix(int64(sec), 0) }
	must(t, s.Insert(id, Sample{At: at(1), Health: value.OK, Changed: true, Kind: value.Number, Value: []byte("1")}))
	must(t, s.Insert(id, Sample{At: at(2), Health: value.OK}))
	must(t, s.Insert(id, Sample{At: at(3), Health: value.OK, Changed: true, Kind: value.Number, Value: []byte("2")}))
	must(t, s.Insert(id, Sample{At: at(4), Health: value.OK}))
	must(t, s.Insert(id, Sample{At: at(6), Health: value.OK}))
	n, err := s.Prune(at(5))
	if err != nil || n != 3 {
		t.Fatalf("pruned %d, %v; want 3", n, err)
	}
	if l, _ := s.Last(id); string(l.Value) != "2" || s.count(t, id) != 2 {
		t.Fatalf("after prune: value %s, %d rows", l.Value, s.count(t, id))
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
