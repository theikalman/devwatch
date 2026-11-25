package source

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"

	"devwatch/internal/config"
	"devwatch/internal/value"
)

var (
	poolsMu sync.Mutex
	pools   = map[string]*sql.DB{}
)

// pool returns the one *sql.DB for dsn, opened read-only.
// shortcut: pools live until exit; fine for one devwatch process watching a few databases.
func pool(dsn string) (*sql.DB, error) {
	poolsMu.Lock()
	defer poolsMu.Unlock()
	if db, ok := pools[dsn]; ok {
		return db, nil
	}
	driver, name := "pgx", dsn
	if path, ok := strings.CutPrefix(dsn, "sqlite:"); ok {
		driver, name = "sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(2000)"
	}
	db, err := sql.Open(driver, name)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetConnMaxIdleTime(time.Minute)
	pools[dsn] = db
	return db, nil
}

// runSQL runs w.SQL read-only, so a watcher can never change the database it watches: SQLite opens the file
// in read-only mode, Postgres runs the query in a read-only transaction.
func runSQL(ctx context.Context, w config.Watcher) (value.Value, error) {
	db, err := pool(w.DSN)
	if err != nil {
		return value.Value{}, err
	}
	q := interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	}(db)
	if !strings.HasPrefix(w.DSN, "sqlite:") {
		tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			return value.Value{}, err
		}
		defer tx.Rollback()
		q = tx
	}
	rows, err := q.QueryContext(ctx, w.SQL)
	if err != nil {
		return value.Value{}, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return value.Value{}, err
	}
	var data [][]*string
	truncated := false
	for rows.Next() {
		if len(data) == value.MaxRows {
			truncated = true
			break
		}
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return value.Value{}, err
		}
		cells := make([]*string, len(cols))
		for i, v := range raw {
			cells[i] = cellText(v)
		}
		data = append(data, cells)
	}
	if err := rows.Err(); err != nil {
		return value.Value{}, err
	}
	return shape(cols, data, truncated, w)
}

// shape turns a result into a value. One cell is parsed like command output, so as, value and match apply.
// One row is a row; value picks a column of it, plus a path into that column's JSON ("payload.status").
// Anything else is a table.
func shape(cols []string, data [][]*string, truncated bool, w config.Watcher) (value.Value, error) {
	if len(data) == 1 && len(cols) == 1 && w.As != "table" {
		return value.Parse(text(data[0][0]), w.As, w.Value, w.MatchRe)
	}
	if len(data) == 1 && w.Value != "" {
		col, rest, _ := strings.Cut(w.Value, ".")
		i := slices.Index(cols, col)
		if i < 0 {
			return value.Value{}, fmt.Errorf("value %s: no column %q in %s", w.Value, col, strings.Join(cols, ", "))
		}
		return value.Parse(text(data[0][i]), w.As, rest, w.MatchRe)
	}
	if len(data) == 1 && w.As == "" {
		return value.NewRow(value.RowData{Cols: cols, Vals: data[0]}), nil
	}
	if w.As != "" && w.As != "table" {
		return value.Value{}, fmt.Errorf("as %s: the query returned %d rows of %d columns", w.As, len(data), len(cols))
	}
	t := value.TableData{Cols: cols, Rows: make([][]string, len(data)), Truncated: truncated}
	for i, r := range data {
		t.Rows[i] = make([]string, len(r))
		for j, c := range r {
			t.Rows[i][j] = text(c)
		}
	}
	return value.NewTable(t), nil
}

// cellText is a database value as text: nil for NULL, timestamps in RFC 3339.
func cellText(v any) *string {
	var s string
	switch v := v.(type) {
	case nil:
		return nil
	case []byte:
		s = string(v)
	case time.Time:
		s = v.Format(time.RFC3339Nano)
	default:
		s = fmt.Sprint(v)
	}
	return &s
}

func text(s *string) string {
	if s == nil {
		return "NULL"
	}
	return *s
}
