# devwatch

Multi-pane terminal dashboard for local development. Watch Postgres query results
or any shell command (e.g. `pgrep -fl my-api`, `aws --endpoint-url http://localhost:4566 s3 ls`) — all on one screen, driven by a YAML file.

Install (macOS Apple Silicon):

```sh
curl -fsSL https://raw.githubusercontent.com/theikalman/devwatch/main/install.sh | sh
```

It installs to `~/.local/bin` (override with `BIN_DIR=...`; pin a release with `VERSION=v0.1.0`).
Then `devwatch -c devwatch.yaml`, or from source:

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
| `stream`  | `command` (long-running, `sh -c`)       | command exits (non-zero = red) |

## Layout

- `main.go` — Bubble Tea model and grid rendering
- `internal/config` — YAML loading and validation
- `internal/watcher` — one `Check` per watcher type; add a type by adding a case

## Streaming

`stream` runs the command once and keeps it running, e.g. `podman logs -f my-container`.
stdout+stderr are appended to the pane (last 1000 lines kept, newest at the bottom), with no polling and no diffing.
By default it is not restarted if it exits; set `restart: 3s` to run it again that long after it exits
(clean exit or not), with a `── restarted HH:MM:SS ──` marker line between runs. On quit, its whole process group is stopped.

## Logging

Set `log_dir` to append every check to `<log_dir>/<watcher name>.log`, one file per watcher.
Each check is a section headed by `=== YYYY-MM-DD HH:MM:SS ===` (when the check started), followed by
its output, with an `ERROR:` line first if it failed. `stream` watchers log one `[timestamp] line` per line instead. Logs grow with every poll and are never rotated.

## Release

Pushing a tag like `v0.1.0` runs `.github/workflows/release.yml`: it vets and tests, builds
`devwatch_darwin_arm64.tar.gz` (macOS Apple Silicon only) with a `.sha256` file, and attaches both to a GitHub release.

```sh
git tag v0.1.0 && git push origin v0.1.0
```
