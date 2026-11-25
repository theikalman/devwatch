# devwatch

A live dashboard for the local state you check while debugging: SQL query results, Docker containers, HTTP
endpoints and command output, on one page in your browser. Each result is shown by its kind (a number with a
sparkline, a record with changed fields highlighted, a container list, an up/down probe), every change is kept
in a local SQLite history, and the config is one YAML file that reloads while devwatch runs.

## Install

macOS Apple Silicon:

```sh
curl -fsSL https://raw.githubusercontent.com/theikalman/devwatch/main/install.sh | sh
```

It installs to `~/.local/bin` (override with `BIN_DIR=...`; pin a release with `VERSION=v1.0.0`).
Or from source, with Go 1.27+: clone the repo and run `go build -o ~/.local/bin/devwatch .`.

## Start

```sh
devwatch init      # writes a commented devwatch.yaml
devwatch --open    # serves http://127.0.0.1:7777 and opens it
```

To see every kind of card without Docker or Postgres: `go run . -c examples/demo.yaml`.

```
devwatch [-c FILE] [--addr ADDR] [--open]   run every watcher and serve the live dashboard
devwatch check [-c FILE]                    run every watcher once and print the results; exit 1 on error or crit
devwatch presets                            list the presets and their settings
devwatch init [-c FILE]                     write a starter config (never over an existing file)
```

## Config

```yaml
title: checkout-service
every: 5s            # default check interval; per watcher: every:
timeout: 5s
keep: 24h            # history retention

vars:
  pg: postgres://postgres:postgres@localhost:5432/app?sslmode=disable

watch:
  # presets: one line each
  - docker.compose: checkout
  - http.health: http://localhost:8080/healthz
  - postgres.count: {dsn: "${pg}", table: orders, where: "status = 'pending'"}
  - postgres.row: {dsn: "${pg}", table: orders, where: "id = 42"}
    name: "order #42"

  # or a source directly
  - name: failed jobs
    sql: SELECT count(*) FROM jobs WHERE state = 'failed'
    dsn: ${pg}
    warn: "> 0"
    crit: "> 10"
    group: queue

  - name: api build
    http: http://localhost:8080/version
    value: build.sha
```

Each entry has one source (`cmd`, `sql`, `http`, `docker`, `port`) or one preset. Any entry also takes `name`,
`group`, `every`, `timeout`, `unit`, `as`, `value`, `match`, `warn`, `crit` and `expect`; next to a preset they
override its own settings.

| Source | Example | Result |
|---|---|---|
| `cmd` | `cmd: redis-cli llen jobs` (runs in `sh -c`; `env:`, `dir:`) | number, text or JSON, inferred from the output; `as: table` splits columns |
| `sql` | `sql: SELECT ...` + `dsn:` (Postgres URL, or `sqlite:path`) | one value → number/text, one row → record, more → table. Read-only |
| `http` | `http: URL` (`method:`, `headers:`, `body:`, `status:`) | up/down probe, or with `value:` one field of the JSON body |
| `docker` | `docker: {container: NAME}`, `{compose: PROJECT}` or `{label: k=v}` | container list: state, health, restarts, uptime |
| `port` | `port: localhost:5432` | up/down probe of a TCP connect |

- **Extract:** `value: data.items.0.status` picks a field from JSON or a record; `match: 'Active: (\w+)'` takes the first regex capture.
- **Health:** `warn:` and `crit:` are one comparison: `"> 10"`, `"<= 0"`, `"== paid"`, `"!= 0"`. `expect:` is crit when it does not hold.
  A failing check (timeout, non-zero exit, SQL error) is `error` and the card keeps its last good value, marked stale.
  A probe that is down, and a container that is not running or unhealthy, is `crit`.
- **Variables:** `${name}` comes from `vars:`, then the environment; an unknown name is an error. `$${name}` keeps it literal for the shell.
- **Errors** name the position: `devwatch.yaml:14:5: watcher "orders": unknown field "intervall"`.
  While devwatch runs, a broken edit keeps the old config and shows the error on the dashboard.
- **YAML traps:** ` #` starts a comment, so quote `"order #42"`; quote values containing `: ` or write them as a `|` block.

## Presets

`devwatch presets` prints each with its settings. Postgres presets use `${DATABASE_URL}` unless you give `dsn`.

| Preset | Example | Watches |
|---|---|---|
| `postgres.count` | `- postgres.count: users` | rows in a table (`where`) |
| `postgres.row` | `- postgres.row: {table: orders, where: "id = 42"}` | one record, changed fields highlighted |
| `postgres.rows` | `- postgres.rows: orders` | the latest rows (`where`, `order`, `limit`) |
| `postgres.activity` | `- postgres.activity:` | queries running longer than `min` (1s) |
| `postgres.locks` | `- postgres.locks:` | blocked queries and who blocks them |
| `docker.container` | `- docker.container: checkout-api` | one container |
| `docker.compose` | `- docker.compose: checkout` | every container of a compose project |
| `http.health` | `- http.health: http://localhost:8080/health` | up/down, status, latency |
| `http.json` | `- http.json: {url: http://localhost:8080/version, path: build.sha}` | one JSON field |
| `process` | `- process: autofsd` | processes whose command line contains the text; crit at 0 |
| `git.branch` | `- git.branch:` | current branch (`dir`) |
| `s3.count` | `- s3.count: uploads` | objects in a bucket, LocalStack by default (`prefix`, `endpoint`) |

Docker is found through `socket:`, `DOCKER_HOST` (unix), `/var/run/docker.sock`, Docker Desktop, Colima or Podman.

## History and safety

History lives in `.devwatch/data.db` next to the config (`db:` to move it), with its own `.gitignore`. Every check is
one row; a value is stored only when it changes. The dashboard binds to `127.0.0.1` and accepts only loopback
`Host` headers; `--addr` on another interface exposes your query results to the network. Passwords in URLs,
auth headers and `env` values are hidden in the UI. SQL runs in read-only transactions.

## Release

Pushing a version tag runs `.github/workflows/release.yml`: it vets and tests, builds
`devwatch_darwin_arm64.tar.gz` with a `.sha256`, and attaches both to a GitHub release.

```sh
git tag v1.0.0 && git push origin v1.0.0
```
