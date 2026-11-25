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
  kind   TEXT NOT NULL,        -- kind of the newest value; '' before the first one
  source TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sample (
  watcher_id INTEGER NOT NULL REFERENCES watcher(id),
  at      INTEGER NOT NULL,    -- unix ms, check start
  health  TEXT    NOT NULL,
  ms      INTEGER NOT NULL,    -- check duration
  num     REAL,
  changed INTEGER NOT NULL,    -- value, health or error differs from the previous sample
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
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
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
	err = s.db.QueryRow(`SELECT w.kind, s.value FROM sample s JOIN watcher w ON w.id = s.watcher_id
		WHERE s.watcher_id = ? AND s.value IS NOT NULL ORDER BY s.at DESC LIMIT 1`, id).Scan(&l.Kind, &l.Value)
	if errors.Is(err, sql.ErrNoRows) {
		return l, nil
	}
	return l, err
}

// Insert records one check. A value of another kind than the watcher's previous one first drops the
// watcher's history: the old rows no longer have the new shape.
func (s *Store) Insert(id int64, smp Sample) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if smp.Value != nil {
		var kind string
		if err := tx.QueryRow(`SELECT kind FROM watcher WHERE id = ?`, id).Scan(&kind); err != nil {
			return err
		}
		if kind != string(smp.Kind) {
			if _, err := tx.Exec(`DELETE FROM sample WHERE watcher_id = ?`, id); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE watcher SET kind = ? WHERE id = ?`, string(smp.Kind), id); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(`INSERT INTO sample (watcher_id, at, health, ms, num, changed, value, summary, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, smp.At.UnixMilli(), smp.Health, smp.MS, smp.Num, smp.Changed, text(smp.Value), text([]byte(smp.Summary)), text([]byte(smp.Error)))
	if err != nil {
		return err
	}
	return tx.Commit()
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
