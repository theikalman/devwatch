// Package watcher runs a single check for a configured watcher and returns its text output.
package watcher

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"

	"devwatch/internal/config"
)

// Check runs one check. A non-nil error means the watched thing is unhealthy.
func Check(ctx context.Context, w config.Watcher) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	switch w.Type {
	case "command":
		out, err := exec.CommandContext(ctx, "sh", "-c", w.Command).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	case "sql":
		return checkSQL(ctx, w)
	}
	return "", fmt.Errorf("unknown type %q", w.Type)
}

// shortcut: opens a new connection per check, fine at seconds-level intervals.
func checkSQL(ctx context.Context, w config.Watcher) (string, error) {
	db, err := sql.Open("pgx", w.DSN)
	if err != nil {
		return "", err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, w.Query)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	lines := []string{strings.Join(cols, " | ")}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return "", err
		}
		cells := make([]string, len(vals))
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			cells[i] = fmt.Sprint(v)
		}
		lines = append(lines, strings.Join(cells, " | "))
	}
	return strings.Join(lines, "\n"), rows.Err()
}
