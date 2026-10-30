# devwatch

Multi-pane terminal dashboard for local development. Watch Postgres query results
or any shell command (e.g. `pgrep -fl my-api`, `aws --endpoint-url http://localhost:4566 s3 ls`) — all on one screen, driven by a YAML file.

```sh
cp devwatch.example.yaml devwatch.yaml
go run . -c devwatch.yaml   # q to quit
```

## Config

Top level: `columns` (default 2), `log_dir` (optional) and `watchers`. Every watcher takes `name`, `type`,
`interval` (default 5s) and `timeout` (default 5s).

| type      | fields                                  | unhealthy when                |
|-----------|-----------------------------------------|-------------------------------|
| `command` | `command` (run with `sh -c`)            | non-zero exit                 |
| `sql`     | `dsn`, `query` (Postgres)               | connect/query error           |

## Layout

- `main.go` — Bubble Tea model and grid rendering
- `internal/config` — YAML loading and validation
- `internal/watcher` — one `Check` per watcher type; add a type by adding a case

## Logging

Set `log_dir` to append every check to `<log_dir>/<watcher name>.log`, one file per watcher.
Each check is a section headed by `=== YYYY-MM-DD HH:MM:SS ===` (when the check started), followed by
its output, with an `ERROR:` line first if it failed. Logs grow with every poll and are never rotated.
