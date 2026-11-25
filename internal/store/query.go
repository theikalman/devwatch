package store

import (
	"database/sql"
	"slices"
	"strings"
	"time"

	"devwatch/internal/value"
)

// Change is one entry of the changes feed.
type Change struct {
	Key, Name  string
	At         time.Time
	Health     string
	PrevHealth string // "" for a watcher's first sample
	Summary    string
}

const sampleCols = `SELECT s.at, s.health, s.ms, s.num, s.changed, w.kind, s.value, s.summary, s.error
	FROM sample s JOIN watcher w ON w.id = s.watcher_id`

// Samples returns the watcher's samples from since on, oldest first: at most the newest limit of them.
func (s *Store) Samples(id int64, since time.Time, limit int) ([]Sample, error) {
	out, err := s.query(sampleCols+` WHERE s.watcher_id = ? AND s.at >= ? ORDER BY s.at DESC LIMIT ?`, id, since.UnixMilli(), limit)
	slices.Reverse(out)
	return out, err
}

// Values returns the watcher's newest n stored values, newest first.
func (s *Store) Values(id int64, n int) ([]Sample, error) {
	return s.query(sampleCols+` WHERE s.watcher_id = ? AND s.value IS NOT NULL ORDER BY s.at DESC LIMIT ?`, id, n)
}

// ErrorSince returns when the watcher's current run of errors started; zero if its newest sample isn't an error.
func (s *Store) ErrorSince(id int64) (time.Time, error) {
	var ms sql.NullInt64
	err := s.db.QueryRow(`SELECT min(at) FROM sample WHERE watcher_id = ? AND at > coalesce(
		(SELECT max(at) FROM sample WHERE watcher_id = ? AND health <> 'error'), -1)`, id, id).Scan(&ms)
	if err != nil || !ms.Valid {
		return time.Time{}, err
	}
	return time.UnixMilli(ms.Int64), nil
}

// Changes returns the newest changes of the watchers with keys before before, newest first.
func (s *Store) Changes(keys []string, before time.Time, limit int) ([]Change, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	args := []any{before.UnixMilli()}
	for _, k := range keys {
		args = append(args, k)
	}
	args = append(args, limit)
	rows, err := s.db.Query(`SELECT w.key, w.name, s.at, s.health, coalesce(s.summary, ''),
		coalesce((SELECT p.health FROM sample p WHERE p.watcher_id = s.watcher_id AND p.at < s.at ORDER BY p.at DESC LIMIT 1), '')
		FROM sample s JOIN watcher w ON w.id = s.watcher_id
		WHERE s.changed = 1 AND s.at < ? AND w.key IN (?`+strings.Repeat(", ?", len(keys)-1)+`)
		ORDER BY s.at DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Change
	for rows.Next() {
		var c Change
		var at int64
		if err := rows.Scan(&c.Key, &c.Name, &at, &c.Health, &c.Summary, &c.PrevHealth); err != nil {
			return nil, err
		}
		c.At = time.UnixMilli(at)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) query(q string, args ...any) ([]Sample, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sample
	for rows.Next() {
		var (
			smp                  Sample
			at                   int64
			num                  sql.NullFloat64
			kind                 string
			val, summary, errMsg sql.NullString
		)
		if err := rows.Scan(&at, &smp.Health, &smp.MS, &num, &smp.Changed, &kind, &val, &summary, &errMsg); err != nil {
			return nil, err
		}
		smp.At = time.UnixMilli(at)
		if num.Valid {
			smp.Num = &num.Float64
		}
		if val.Valid {
			smp.Kind, smp.Value = value.Kind(kind), []byte(val.String)
		}
		smp.Summary, smp.Error = summary.String, errMsg.String
		out = append(out, smp)
	}
	return out, rows.Err()
}
