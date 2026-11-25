# devwatch v2 — plan

A local dashboard that watches the state you care about while debugging (database values, containers,
HTTP endpoints, command output), keeps the history in SQLite, and shows each kind of result as a
purpose-built card that updates in real time.

This is a rewrite. The TUI, the old config format and the grid layout are dropped. The few ideas worth
keeping (killing a command's whole process group on timeout, pure-Go single binary, `install.sh`,
the release workflow) carry over.

## Goals and what delivers them

| Goal | Delivered by |
|---|---|
| Easy to watch state while debugging | Typed results instead of raw text; one **Changes** feed across all watchers ("order #42 status pending → paid"); per-watcher history; *run now* button |
| Beautiful, real-time UI | One card design per result kind (big number + sparkline, record with changed fields highlighted, container list, up/down probe); pushed over SSE; light and dark |
| Easy to configure | One-line watchers (`- postgres.count: users`), presets, `${VAR}` from env, `devwatch check`, hot reload, errors that point at `file:line` |
| Presets | Built-in catalog for Postgres, Docker/Podman, HTTP, ports, processes, git, LocalStack S3; `devwatch presets` lists them |

**Not in v1:** auth or remote/multi-user use, alerts beyond the browser tab title, editing watchers from the UI,
user-defined presets, log tailing (see *Later*), Windows.

## Core model: source → value → health → sample

```
watcher (config) ─► source.Fetch ─► raw ─► value:/match: extract ─► infer kind ─► Value{kind, data, num}
                                                                                       │
                                                              thresholds ─► health ────┤
                                                                                       ▼
                                                     store (SQLite) ◄── sample ──► hub ──► SSE ──► browser
```

Every watcher, whether written by hand or expanded from a preset, ends up as one `config.Watcher`
with exactly one source. Everything after the source is shared.

### Sources

| Source | Config | What it does | Default kind |
|---|---|---|---|
| `cmd` | `cmd: <shell>` (+ `env`, `dir`) | `sh -c` in the config's directory, stdout+stderr, own process group killed on timeout, output capped at 64 KB | inferred from output |
| `sql` | `sql: <query>` + `dsn` | Postgres (pgx) or SQLite (`sqlite:` DSN). One pooled `*sql.DB` per DSN, max 200 rows | 1×1 → number/text · 1 row → row · n rows → table |
| `http` | `http: <url>` (+ `method`, `headers`, `body`, `expect`) | `net/http`, body capped at 256 KB | probe; with `value:` → the extracted value; `as: json` → json |
| `docker` | `docker: {container \| compose \| label}` | Engine API over the unix socket with plain `net/http` (no SDK). Same API works for Docker Desktop, Colima and Podman | containers |
| `port` | `port: host:port` | TCP dial | probe |

### Value kinds (these drive the UI)

| Kind | Data | `num` (charted) | Counts as a change when | Card |
|---|---|---|---|---|
| `number` | float | the value | value differs | Big number + unit, Δ since last change, sparkline, threshold color |
| `text` | string | — | string differs | Value in mono, "since 12:03:04", recent values |
| `row` | `{col: value}` | — | any field differs | Key/value list; changed fields highlighted with the old value beside them |
| `table` | `{cols, rows, truncated}` | row count | rows added or removed | Compact table; new rows flash, removed rows shown struck through once |
| `json` | any | — | canonical JSON differs | Collapsible tree, changed paths highlighted |
| `probe` | `{up, code?}` | latency ms | `up` or `code` differs (latency alone does not) | UP/DOWN pill, status code, latency sparkline, uptime strip of the last 60 checks |
| `containers` | `[{name, state, health, restarts, started, image}]` | running count | any state, health or restart count differs | One row per container: state dot, name, uptime, restarts, health |

### Inference and extraction

Applied in this order, each step optional:

1. `value: data.items.0.status` — dotted path into JSON output, an HTTP body, or a `row`'s fields.
2. `match: 'Active: (\w+)'` — regex on text; first capture group (or whole match).
3. Infer, unless `as:` overrides: trimmed output that parses as a float → `number`; valid JSON object/array →
   `json`; anything else → `text` (multi-line text renders as a block).
   `as: table` splits on tab, `|`, or 2+ spaces with the first line as header (good for `docker ps`, `kubectl get`).

### Health

| Health | When |
|---|---|
| `error` | The source itself failed: timeout, non-zero exit, SQL error, unexpected HTTP status. The card keeps the last good value, dimmed and marked stale |
| `crit` | `crit:` matched, or `expect:` not met. For probes, "down" is the answer, not an error, so connection refused → `up: false` → `crit` |
| `warn` | `warn:` matched |
| `ok` | otherwise |

Threshold syntax is one comparison: `"> 10"`, `">= 1"`, `"< 5"`, `"<= 0"`, `"== paid"`, `"!= 0"`.
It applies to a number, to text (`==`/`!=` only), to a table's row count, or to a containers list's running count.
For a `row` or `json`, pick a field with `value:` first.

## Config format

```yaml
title: checkout-service
every: 5s            # default check interval (per watcher: every:)
timeout: 5s
keep: 24h            # history retention
# db: .devwatch/data.db   (default, next to this file)

vars:
  pg: postgres://postgres:postgres@localhost:5432/app?sslmode=disable

watch:
  # presets: "<preset>: <main argument>" or "<preset>: {params}"
  - docker.compose: checkout
  - http.health: http://localhost:8080/healthz
  - port: localhost:5432
  - postgres.count: { dsn: "${pg}", table: orders, where: "status = 'pending'" }
  - postgres.row: { dsn: "${pg}", table: orders, where: "id = 42" }
    name: "order #42"
    group: data

  # raw sources
  - name: failed jobs (5m)
    sql: SELECT count(*) FROM jobs WHERE state = 'failed' AND updated_at > now() - interval '5 min'
    dsn: ${pg}
    warn: "> 0"
    crit: "> 10"
    group: queue

  - name: api build
    http: http://localhost:8080/version
    value: build.sha

  - name: autofsd processes
    cmd: pgrep -f autofsd | wc -l
    expect: "> 0"

  - name: uploads in S3
    cmd: aws --endpoint-url=http://localhost:4566 s3 ls s3://uploads --recursive | wc -l
    env: { AWS_ACCESS_KEY_ID: test, AWS_SECRET_ACCESS_KEY: test, AWS_DEFAULT_REGION: us-east-1 }
    unit: objects
```

Rules:

- Each entry has exactly one **type key**: a source (`cmd`, `sql`, `http`, `docker`, `port`) or a preset name.
  Its value is the main argument (a string) or a map of params.
- Common fields on any entry: `name`, `group`, `every`, `timeout`, `unit`, `as`, `value`, `match`, `warn`, `crit`, `expect`.
  On a preset they override the preset's defaults.
- `name` defaults to the preset's name template (`orders count`) or the main argument, shortened. Names must be unique.
- `${x}` resolves from `vars`, then the environment. Unknown → config error. Postgres presets default `dsn` to `${DATABASE_URL}`,
  so `- postgres.count: users` works with nothing else when that variable is set.
- No layout settings. Cards appear in config order, grouped by `group`; the card size follows the kind.
- Unknown fields are rejected, and every error names its position:
  `devwatch.yaml:14:5: watcher "orders": unknown field "intervall"`.
- YAML traps to explain in `devwatch init` and in error hints (M4): a ` #` starts a comment, so quote names like
  `"order #42"`; a value containing `: ` (any JSON) must be quoted or written as a `|` block, as `examples/demo.yaml` does.

## Presets (v1 catalog)

Presets are Go functions in `internal/config/presets.go`. Each returns a plain `Watcher`, so nothing downstream
knows presets exist. Each has a doc line and an example that `devwatch presets` prints.

| Preset | Main arg | Other params | Watches | Kind |
|---|---|---|---|---|
| `postgres.count` | table | `dsn`, `where` | `count(*)` | number |
| `postgres.row` | — | `dsn`, `table`, `where` | one record, e.g. `id = 42` | row |
| `postgres.rows` | table | `dsn`, `where`, `order`, `limit` (20) | latest rows | table |
| `postgres.activity` | dsn | `min` (1s) | non-idle queries: pid, state, duration, query; warn when any > 5s | table |
| `postgres.locks` | dsn | — | blocked queries and who blocks them; warn when any | table |
| `docker.container` | name | `socket` | state, health, restarts, uptime | containers |
| `docker.compose` | project | `socket` | every service of a compose project; crit if any is not running or unhealthy | containers |
| `docker.running` | — | `filter`, `socket` | count of running containers | number |
| `http.health` | url | `expect` (2xx) | up/down, code, latency | probe |
| `http.json` | url | `path`, `headers` | one value from a JSON response | number/text |
| `process` | pattern | — | `pgrep -f … \| wc -l` (macOS `pgrep` has no `-c`); crit when 0 | number |
| `git.branch` | — | `dir` | current branch | text |
| `s3.count` | bucket | `prefix`, `endpoint` (LocalStack default) | object count via `aws s3 ls --recursive` | number |

Docker socket lookup order: `socket:` → `DOCKER_HOST` (unix) → `/var/run/docker.sock` → `~/.docker/run/docker.sock`
→ `~/.colima/default/docker.sock` → the path `podman machine inspect` reports (asked once at startup).

## Storage (SQLite)

- Driver `modernc.org/sqlite`: pure Go, so the `CGO_ENABLED=0` cross-compile in the release workflow keeps working.
  WAL mode. The file lives in `.devwatch/` next to the config with mode 0600, and devwatch writes
  `.devwatch/.gitignore` containing `*` so stored output never gets committed.

```sql
CREATE TABLE watcher (
  id     INTEGER PRIMARY KEY,
  key    TEXT NOT NULL UNIQUE,   -- slug of name: history follows the name
  name   TEXT NOT NULL,
  kind   TEXT NOT NULL,
  source TEXT NOT NULL           -- cmd | sql | http | docker | port
);
CREATE TABLE sample (
  watcher_id INTEGER NOT NULL REFERENCES watcher(id),
  at      INTEGER NOT NULL,      -- unix ms, check start
  health  TEXT    NOT NULL,      -- ok | warn | crit | error
  ms      INTEGER NOT NULL,      -- check duration
  num     REAL,                  -- charted number (see kinds table)
  changed INTEGER NOT NULL,      -- value or health differs from the previous sample
  value   TEXT,                  -- canonical JSON, only when changed = 1
  summary TEXT,                  -- "pending → paid", only when changed = 1
  error   TEXT,
  PRIMARY KEY (watcher_id, at)
) WITHOUT ROWID;
CREATE INDEX sample_changes ON sample(at) WHERE changed = 1;
```

- **Every check is one row** (health, duration, num), so uptime strips and charts have real data.
  The value itself is stored only when it changed, so a 200-row table polled every 2 s is stored once per change, not per poll.
- **Size:** 20 watchers every 5 s for 24 h ≈ 350k rows, roughly 15–25 MB. `keep:` bounds it.
- **Retention:** every 5 min, delete rows older than `keep`, except each watcher's newest changed row (later rows rely on it for the value).
- **Startup:** load each watcher's last changed row, so the dashboard shows the last known state at once (marked stale until the first check)
  and change detection continues across restarts.
- **Identity:** `key` is the slugged name. Renaming starts a new history. If a watcher's kind changes, its history is dropped (the stored shapes no longer match).
  Watchers removed from the config keep their history until retention removes it, and are hidden from the UI.
- **Summaries** are computed in Go when a change is stored, per kind: `17 → 20 (+3)`, `pending → paid`,
  `status: pending → paid`, `+2 rows, −1 row`, `DOWN: connection refused`, `checkout-api: running → exited, restarts 0 → 1`, `ok → warn`.
  The feed just renders them.

## Runtime

- **Engine:** one goroutine per watcher: check → evaluate → store → publish → wait `every` (or a run-now signal).
  Checks of one watcher never overlap. First checks get 0–500 ms jitter so they don't all fire at once.
- **Timeouts:** context per check. `cmd` runs in its own process group and the group is killed on timeout (reuses the old `Setpgid` + `cmd.Cancel` approach).
- **Connection reuse:** one `*sql.DB` per DSN, one HTTP client per watcher, one Docker client per socket.
- **Hub:** latest state per watcher in memory behind a mutex. Each browser gets a buffered channel. If the channel is full, that browser is disconnected;
  `EventSource` reconnects and gets a fresh snapshot. A slow browser never slows the checks.
- **Hot reload:** check the config's mtime every second. If the new config is invalid, keep running the old one and show the error in the UI.
  If it is valid, compare watchers by key: stop the removed ones, start the new ones, restart the ones whose settings changed, then push a new snapshot.
- **Shutdown:** SIGINT/SIGTERM → stop the HTTP server → cancel the engine → wait for child processes → close the DB.

## HTTP API

| Route | Purpose |
|---|---|
| `GET /` | Embedded UI |
| `GET /api/events` | SSE stream |
| `GET /api/watchers/{key}/samples?since=&until=&limit=` | History for the drawer (chart, change log) |
| `GET /api/changes?before=&limit=` | Changes feed, paginated across all watchers |
| `POST /api/watchers/{key}/run` | Run a check now |

SSE events (JSON in `data:`):

```
snapshot      {title, watchers: [{key, name, group, source, kind, unit, every, spec, last: sample|null, checked_at}]}
sample        {key, at, health, ms, num, changed, value?, summary?, error?}
config_error  {file, line, col, message}     # cleared by the next snapshot
```

`spec` is the watcher's settings for display, with secrets removed (URL passwords, `Authorization`/`Cookie`/`*token*` headers, `env` values).

## UI

The reviewed design is in Claude Design: [devwatch dashboard](https://claude.ai/artifact/DAHZBDk2bxPp6xiiurGm98), with four
artboards: Dashboard, Watcher detail, Design guideline, and Cards: kinds and states. Use the canvas as the reference for layout
and visuals. This section records the decisions behind it.

### Design rules

- **Quiet when healthy, loud when not.** OK cards are neutral. Only warn, crit and error color the card border, the pill
  and the value, so a glance finds the problem.
- **Health is never shown by color alone:** always an icon plus a word: OK (check-circle), Warn (triangle), Crit (octagon-x), Error (ban).
- **Crit is red, error is violet.** Red means the watched thing is bad. Violet means devwatch could not get a value.
- **The value is the biggest thing on the card**, in mono, with its unit after it in `text-3`.
- **Two fonts:** Inter for devwatch's own words. JetBrains Mono with tabular figures for anything the watched system produced
  (values, output, column names, times). Both are OFL-licensed and vendored as woff2 files in `web/vendor/fonts/`, so nothing loads from the network.
- **One accent color (blue)** for actions, focus, change highlights and neutral charts.
- **Flat cards:** 1 px border, radius 10, no shadow. Only floating layers (drawer, tooltip) get a shadow. Spacing uses a 4 px grid:
  16 between cards, 24 around the page, 28 between groups.
- **Tokens** live in `web/tokens.css` as CSS custom properties, light and dark, switched by `prefers-color-scheme`.
  Components use tokens only, never raw hex. Every text token passes 4.5:1 on `surface` and `surface-2` in its theme.

| Token | Light | Dark | Use |
|---|---|---|---|
| `bg` | `#F5F6F8` | `#0D1015` | Page |
| `surface` | `#FFFFFF` | `#151920` | Cards, header, drawer |
| `surface-2` | `#F0F2F5` | `#1C212A` | Code, stats, segmented control |
| `border` / `border-strong` | `#E2E5EA` / `#CBD0D8` | `#262C36` / `#363E4B` | Lines / inputs, chart axis |
| `text` / `text-2` / `text-3` | `#14171C` / `#4B5361` / `#5F6775` | `#E7EAF0` / `#B0B8C5` / `#8E97A7` | Values / secondary / meta, old values |
| `accent` / `accent-soft` | `#2F5BEA` / `#E8EEFE` | `#7C9BFF` / `#1C2648` | Actions, focus / changed field, new row |
| `ok` / `ok-soft` | `#13723A` / `#E5F4EA` | `#4CC47A` / `#11291B` | OK |
| `warn` / `warn-soft` | `#9A5800` / `#FDF0D8` | `#E9A23B` / `#2E220D` | Warn |
| `crit` / `crit-soft` | `#C62F2C` / `#FCE7E6` | `#FF6E69` / `#341716` | Crit |
| `err` / `err-soft` | `#6A45C2` / `#EFE9FB` | `#AE90FB` / `#241C3D` | Error |

### Screens

- **Top bar:** brand, project `title`, config file and watcher count; health counts (click one to filter); a filter field (`/`);
  a live indicator. The tab title becomes `✖1 ▲1 · checkout-service`, so a background tab still shows trouble.
- **Grid:** groups and cards in config order, `repeat(auto-fill, minmax(280px, 1fr))`, gap 16 (240 px, as first drawn, wrapped the footer and delta). Wide kinds (`table`, `row`,
  `containers`, `json`) span two columns. Under 720 px: one column, nothing spans, and tables scroll inside their card.
- **Card:** source icon (sql: database, docker: box, http: globe, cmd: terminal, port: plug); the name, plus a mono source line
  (`postgres.count · orders`); health pill; the kind's body; and a footer with "checked 2s ago · 14 ms · every 5s" and a 44 px run-now button.
  Clicking the card opens the drawer.
- **States:**
  - Before the first check: skeleton plus "waiting for first check".
  - Just changed: an `accent-soft` highlight that fades over 1.5 s, plus a Δ chip.
  - Warn or crit: the matching rule printed under the value.
  - Error: the error message verbatim in mono, then the last good value dimmed with its time, and "failing for 1m 22s".
  - Stale (no check for 3× `every`): dashed border, a Stale pill, the value dimmed.
- **Kind bodies:** see the kinds table above. A `row` card tints changed fields and shows the old value struck through beside the new one.
  A `table` card tags new rows NEW and shows removed rows struck through once. A `json` tree opens changed paths by itself.
- **Changes feed:** a right rail (at least 300 px wide; it moves below the grid when narrow), newest first, built from `summary`.
  Health transitions show as a small pill (`OK → Warn`). "Show older changes" loads more from `/api/changes`.
- **Drawer:** right side, up to 600 px wide, over a scrim, full width on a phone. It has:
  - the big value and the reason for its health ("Warn because 20 is above 18");
  - Run now and Copy value buttons;
  - a history chart (15m / 1h / 24h) with the threshold dashed and a hover tooltip;
  - min, max, change count, checks and failures;
  - the change log;
  - the watcher's YAML with secrets hidden, and its `file:line`.
- **Banners** under the header, about devwatch itself: "Reconnecting to devwatch…" (neutral, shows the time the values are from)
  and "Config not reloaded" with `file:line` (error colors).
- **Copy:**
  - Relative time for freshness, `HH:MM:SS` for events.
  - `old → new` with the old value muted.
  - Signed deltas with a real minus (`+3`, `−2`).
  - Durations as `14 ms`, `1.8s`, `3m 12s`.
  - `—` or "no check", never N/A.
  - Sentence case, except the all-caps group labels.
- **Motion** only reports something: the change highlight, the run-now spinner, the drawer sliding in over 200 ms.
  `prefers-reduced-motion` turns off the slide and the spin.
- **Accessibility:**
  - Real buttons and labelled inputs.
  - `aria-label` on every icon-only button.
  - A text summary for every chart.
  - Keyboard: `/` focuses the filter, Enter opens the drawer, Esc closes it.
- **Stack:** no build step. Preact + htm as ES modules (about 15 KB, MIT) vendored into `web/vendor/`, hand-written CSS from the tokens,
  hand-drawn SVG for sparklines, uptime strips and the drawer chart. Everything is embedded with `go:embed`.
- **Safety:** all watcher output is rendered as text (`textContent` / Preact text nodes), never as HTML.

## CLI

```
devwatch                     serve ./devwatch.yaml on http://127.0.0.1:7777
devwatch -c FILE --addr ADDR --open
devwatch check  [-c FILE]    run every watcher once, print name / health / value; exit 1 on error or crit
devwatch presets             list presets with params and an example line each
devwatch init                write a commented starter devwatch.yaml (refuses to overwrite)
```

## Repo layout

```
main.go                 flags + subcommands, wiring, signals
internal/config/        schema, shorthand, presets.go, ${vars}, validation with line numbers
internal/source/        cmd.go sql.go http.go docker.go port.go — Fetch(ctx, w) (value.Value, error)
internal/value/         kinds, extraction, inference, thresholds, change summaries
internal/store/         schema, insert, history and feed queries, retention
internal/engine/        per-watcher loops, run-now, hot reload
internal/server/        hub, routes, SSE, Host/Origin checks, redaction
web/                    index.html, app.js, cards.js, drawer.js, ui.js, format.js, tokens.css, style.css, web.go (embed), vendor/
examples/demo.yaml      every kind using plain shell commands only (no Docker or Postgres needed)
```

Presets live inside `config` because expanding them is part of loading the config, and a separate package would import `config` back (an import cycle).

## Security

- Listens on `127.0.0.1:7777` by default. Any other address is an explicit `--addr`.
- When listening on loopback, reject requests whose `Host` is not `localhost` / `127.0.0.1` / `[::1]`. This blocks DNS-rebinding sites from reading the dashboard.
- `POST` requires `Sec-Fetch-Site: same-origin` (or a matching `Origin`), so another site can't trigger checks. No CORS headers.
- Secrets are removed from everything the API returns (see `spec`). The UI only ever shows output, not the settings that produced it.
- Command and query output is stored on disk: `.devwatch/` with mode 0600, gitignored, bounded by `keep`.
- The config is trusted input (it already runs shell commands), so preset params like `where` are inserted as written.

## Testing

| Package | Tests |
|---|---|
| `value` | Table-driven inference, `value:`/`match:`, thresholds, summaries per kind. Most of the logic lives here |
| `config` | Shorthand forms, every preset expands, `${vars}`/env, unknown-field and duplicate-name errors with line numbers |
| `source` | `cmd` with real `sh` (timeout kills children), `http` with `httptest`, `sql` with in-memory SQLite (Postgres when `DEVWATCH_TEST_PG` is set), `docker` against a fake Engine API on a temp unix socket, `port` with a local listener |
| `store` | Value stored only on change, retention keeps the newest changed row, state reloads after restart |
| `engine` | Fake source → samples and events in order; run-now; reload adds, removes and restarts watchers |
| `server` | First SSE frame is `snapshot`; run-now rejects a foreign `Origin` and a foreign `Host` |
| UI | Manual pass with `examples/demo.yaml` against the design artboards, in light, dark and at 375 px. `check` itself is covered by `go test`; the demo exits 1 on purpose (it shows crit and error) |

## Milestones

| | Scope | Done when |
|---|---|---|
| **M0 Fresh start** | Tag the current `main` as `v0-tui`, then on a `v2` branch delete the old code (keep `LICENSE`, `install.sh`, the release workflow), create the new layout, `go mod tidy` | `go build` passes, `devwatch --help` works. **Done** |
| **M1 Core, no UI** | Long-form config, `cmd` + `port` sources, kinds `number`/`text`/`json`/`table`/`probe`, thresholds, SQLite store, engine, `devwatch check`; plain `devwatch` prints each change until M2 | `devwatch check -c examples/demo.yaml` prints typed values; tests pass. **Done** |
| **M2 Realtime UI** | Server, SSE, `tokens.css` and vendored fonts, cards for M1's kinds with every state, changes feed, drawer with history, run-now, filter, banners, tab title | Demo matches the Claude Design artboards in light and dark and works at phone width; changing a demo value updates the card and the feed within one interval. **Done**, except the config-error banner, which needs M4's hot reload |
| **M3 Real sources** | `sql` (Postgres, SQLite), `http`, `docker` (Docker/Colima/Podman), kinds `row` and `containers` | Against a local compose stack (Postgres + an API), every source shows values; stopping a container flips its card and logs a change |
| **M4 Easy config** | Shorthand, preset catalog, `${vars}`/env, positioned errors, `devwatch presets`, `devwatch init`, hot reload | A config of only preset one-liners works; editing the YAML updates the dashboard without a restart; a typo shows a banner with `file:line` |
| **M5 Ship** | README rewrite, check binary size and the `CGO_ENABLED=0` build, tag `v1.0.0` | `install.sh` installs a working v1 |

## Later (not v1)

Log tailing (`docker logs -f`, `tail -f`; the old tool's `stream` type) · desktop notifications · loading `.env` ·
MySQL, Redis, Kafka, Kubernetes presets · user-defined presets in YAML · thresholds on one field of a `row`/`json` ·
pausing a watcher · exporting history · an `ignore:` regex for noisy output.

## Decisions to confirm

1. **Frontend stack:** no-build Preact + htm (recommended: nothing extra to install, one binary) vs Vite + React + TypeScript
   (nicer for a large UI, but needs Node in development and CI).
2. **Log tailing:** later (recommended) vs in v1. The old tool had it, but it is a log viewer, not a state watcher.
3. **Databases:** Postgres + SQLite in v1 (recommended; SQLite comes free with the storage driver). Add MySQL now?
4. **Container runtime** you use day to day (Docker Desktop, Colima or Podman): it gets tested first.
5. **History:** keep 24 h in `.devwatch/data.db` beside the config (recommended).
6. **Old TUI:** tag it `v0-tui` and delete it (recommended).

## Risks and trade-offs

- `modernc.org/sqlite` adds roughly 6–10 MB to the binary and slows the first compile. The cgo driver would break cross-compiling from Linux in CI.
- Docker socket paths differ between Docker Desktop, Colima and Podman. Handled by the lookup list and the `socket:` override.
- Output that embeds a timestamp changes on every check and floods the feed. Narrow it with `value:`/`match:` (an `ignore:` regex is listed under *Later*).
- Big queries polled often cost the database you are debugging. Rows are capped at 200; use `LIMIT` and a longer `every`.
- Vendored fonts add a few hundred KB to the binary (Inter 400–700 and JetBrains Mono 400–600, Latin subset).
