// Package store keeps every check in SQLite: one row per check, the value itself only when it changed.
package store

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"devwatch/internal/value"
)

const schema = `
CREATE TABLE IF NOT EXISTS watcher (
  id     INTEGER PRIMARY KEY,
  key    TEXT NOT NULL UNIQUE, -- slug of the name: history follows the name
  name   TEXT NOT NULL,
  kind   TEXT NOT NULL,        -- unused since values carry their own kind; kept so older files accept inserts
  source TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sample (
  watcher_id INTEGER NOT NULL REFERENCES watcher(id),
  at      INTEGER NOT NULL,    -- unix ms, check start
  health  TEXT    NOT NULL,
  ms      INTEGER NOT NULL,    -- check duration
  num     REAL,
  changed INTEGER NOT NULL,    -- value, health or error differs from the previous sample
  kind    TEXT,                -- the value's kind, set with value
  value   TEXT,                -- canonical JSON, only when the value changed
  summary TEXT,                -- only when changed
  error   TEXT,
  PRIMARY KEY (watcher_id, at)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS sample_changes ON sample(at) WHERE changed = 1;
`

type Store struct{ db *sql.DB }

// Sample is one check.
type Sample struct {
	At      time.Time
	Health  string
	MS      int64
	Num     *float64
	Changed bool
	Kind    value.Kind // set with Value
	Value   []byte     // canonical JSON, only when the value changed
	Summary string     // only when Changed
	Error   string
	Why     string // why the health isn't OK; not stored
}

// Last is a watcher's state at its newest sample, so change detection carries across restarts.
type Last struct {
	Found         bool
	Health, Error string
	Kind          value.Kind
	Value         []byte // newest stored value; nil if there is none
}

// Open opens or creates the database. A folder it has to create gets mode 0700 and a .gitignore
// of "*", so stored output stays private and out of git.
func Open(path string) (*Store, error) {
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
		// Only in a folder devwatch created, so it never hides a project directory from git.
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*\n"), 0o600); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // shortcut: one connection serializes all access; fine at a few writes per second
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}

// migrate creates the tables, and gives files from before per-value kinds a sample.kind column,
// filled from the watcher's kind they used to share.
func migrate(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	var has int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('sample') WHERE name = 'kind'`).Scan(&has); err != nil || has > 0 {
		return err
	}
	_, err := db.Exec(`ALTER TABLE sample ADD COLUMN kind TEXT;
		UPDATE sample SET kind = (SELECT kind FROM watcher WHERE id = sample.watcher_id) WHERE value IS NOT NULL`)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// Watcher returns the id for key, creating the watcher or refreshing its name and source.
func (s *Store) Watcher(key, name, source string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`INSERT INTO watcher (key, name, kind, source) VALUES (?, ?, '', ?)
		ON CONFLICT(key) DO UPDATE SET name = excluded.name, source = excluded.source
		RETURNING id`, key, name, source).Scan(&id)
	return id, err
}

// Last returns the watcher's newest health and error, and its newest stored value.
func (s *Store) Last(id int64) (Last, error) {
	var l Last
	err := s.db.QueryRow(`SELECT health, coalesce(error, '') FROM sample WHERE watcher_id = ? ORDER BY at DESC LIMIT 1`, id).
		Scan(&l.Health, &l.Error)
	if errors.Is(err, sql.ErrNoRows) {
		return l, nil
	}
	if err != nil {
		return l, err
	}
	l.Found = true
	err = s.db.QueryRow(`SELECT kind, value FROM sample WHERE watcher_id = ? AND value IS NOT NULL ORDER BY at DESC LIMIT 1`, id).
		Scan(&l.Kind, &l.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return l, nil
	}
	return l, err
}

// Insert records one check.
func (s *Store) Insert(id int64, smp Sample) error {
	_, err := s.db.Exec(`INSERT INTO sample (watcher_id, at, health, ms, num, changed, kind, value, summary, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, smp.At.UnixMilli(), smp.Health, smp.MS, smp.Num, smp.Changed, text([]byte(smp.Kind)), text(smp.Value), text([]byte(smp.Summary)), text([]byte(smp.Error)))
	return err
}

// Prune deletes samples older than before, except each watcher's newest stored value before it:
// the rows after it still rely on it for their value.
func (s *Store) Prune(before time.Time) (int64, error) {
	ms := before.UnixMilli()
	res, err := s.db.Exec(`DELETE FROM sample WHERE at < ? AND NOT (value IS NOT NULL AND at = (
		SELECT max(at) FROM sample s2 WHERE s2.watcher_id = sample.watcher_id AND s2.value IS NOT NULL AND s2.at < ?))`, ms, ms)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// text stores b as TEXT, or NULL when empty.
func text(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}
