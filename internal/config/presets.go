package config

import (
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Param is one setting a preset takes.
type Param struct {
	Name, Default string
	Required      bool
}

// Preset is a watcher written in one line: `- postgres.count: users`. It builds a plain Watcher, so nothing
// after loading knows presets exist; settings next to it (name, warn, every…) override what it sets.
type Preset struct {
	Name, Doc, Example string
	Main               string // the param a plain string fills
	Params             []Param
	watcher            func(p map[string]string) Watcher
}

const dsnFromEnv = "${DATABASE_URL}"

var catalog = []*Preset{
	{
		Name: "postgres.count", Doc: "rows in a table, as a number", Example: "- postgres.count: users",
		Main: "table", Params: []Param{{Name: "table", Required: true}, {Name: "where"}, {Name: "dsn", Default: dsnFromEnv}},
		watcher: func(p map[string]string) Watcher {
			return Watcher{Name: p["table"] + " count", SQL: "SELECT count(*) FROM " + p["table"] + where(p), DSN: p["dsn"]}
		},
	},
	{
		Name: "postgres.row", Doc: "one record, every column; changed fields are highlighted",
		Example: `- postgres.row: {table: orders, where: "id = 42"}`,
		Params:  []Param{{Name: "table", Required: true}, {Name: "where", Required: true}, {Name: "dsn", Default: dsnFromEnv}},
		watcher: func(p map[string]string) Watcher {
			return Watcher{Name: p["table"] + " where " + p["where"], SQL: "SELECT * FROM " + p["table"] + where(p) + " LIMIT 1", DSN: p["dsn"]}
		},
	},
	{
		Name: "postgres.rows", Doc: "the latest rows of a table", Example: "- postgres.rows: orders",
		Main: "table", Params: []Param{{Name: "table", Required: true}, {Name: "where"}, {Name: "order", Default: "1 DESC"}, {Name: "limit", Default: "20"}, {Name: "dsn", Default: dsnFromEnv}},
		watcher: func(p map[string]string) Watcher {
			return Watcher{Name: "latest " + p["table"], As: "table", DSN: p["dsn"],
				SQL: "SELECT * FROM " + p["table"] + where(p) + " ORDER BY " + p["order"] + " LIMIT " + p["limit"]}
		},
	},
	{
		Name: "postgres.activity", Doc: "queries running longer than min; warn when there are any", Example: "- postgres.activity:",
		Main: "dsn", Params: []Param{{Name: "dsn", Default: dsnFromEnv}, {Name: "min", Default: "1s"}},
		watcher: func(p map[string]string) Watcher {
			return Watcher{Name: "active queries", As: "table", Warn: "> 0", DSN: p["dsn"], SQL: `SELECT pid, state,
  round(extract(epoch FROM now() - query_start)::numeric, 1) AS seconds, left(query, 200) AS query
FROM pg_stat_activity
WHERE state <> 'idle' AND pid <> pg_backend_pid() AND query_start < now() - interval '` + sqlQuote(p["min"]) + `'
ORDER BY query_start`}
		},
	},
	{
		Name: "postgres.locks", Doc: "queries blocked by another session, and who blocks them", Example: "- postgres.locks:",
		Main: "dsn", Params: []Param{{Name: "dsn", Default: dsnFromEnv}},
		watcher: func(p map[string]string) Watcher {
			return Watcher{Name: "blocked queries", As: "table", Warn: "> 0", DSN: p["dsn"], SQL: `SELECT pid, pg_blocking_pids(pid)::text AS blocked_by,
  round(extract(epoch FROM now() - query_start)::numeric, 1) AS seconds, left(query, 200) AS query
FROM pg_stat_activity
WHERE cardinality(pg_blocking_pids(pid)) > 0`}
		},
	},
	{
		Name: "docker.container", Doc: "one container: state, health, restarts, uptime", Example: "- docker.container: checkout-api",
		Main: "name", Params: []Param{{Name: "name", Required: true}, {Name: "socket"}},
		watcher: func(p map[string]string) Watcher {
			return Watcher{Name: p["name"], Docker: &Docker{Container: p["name"]}, Socket: p["socket"]}
		},
	},
	{
		Name: "docker.compose", Doc: "every container of a compose project", Example: "- docker.compose: checkout",
		Main: "project", Params: []Param{{Name: "project", Required: true}, {Name: "socket"}},
		watcher: func(p map[string]string) Watcher {
			return Watcher{Name: p["project"] + " stack", Docker: &Docker{Compose: p["project"]}, Socket: p["socket"]}
		},
	},
	{
		Name: "http.health", Doc: "up or down, status code and latency", Example: "- http.health: http://localhost:8080/health",
		Main: "url", Params: []Param{{Name: "url", Required: true}, {Name: "status", Default: "2xx"}},
		watcher: func(p map[string]string) Watcher {
			return Watcher{Name: urlName(p["url"]), HTTP: p["url"], Status: p["status"]}
		},
	},
	{
		Name: "http.json", Doc: "one value from a JSON response", Example: "- http.json: {url: http://localhost:8080/version, path: build.sha}",
		Params: []Param{{Name: "url", Required: true}, {Name: "path", Required: true}},
		watcher: func(p map[string]string) Watcher {
			return Watcher{Name: p["path"], HTTP: p["url"], Value: p["path"]}
		},
	},
	{
		Name: "process", Doc: "how many processes have this text in their command line; crit when none", Example: "- process: autofsd",
		Main: "pattern", Params: []Param{{Name: "pattern", Required: true}},
		watcher: func(p map[string]string) Watcher {
			// The pattern travels in the environment, so no command line in this pipeline contains it and counts
			// itself. ps rather than pgrep: pgrep sees no processes under some launchers (sandboxed app helpers).
			return Watcher{Name: p["pattern"] + " processes", Cmd: `ps -A -o args= | awk 'index($0, ENVIRON["DEVWATCH_PATTERN"]) { n++ } END { print n + 0 }'`,
				Env: map[string]string{"DEVWATCH_PATTERN": p["pattern"]}, Expect: "> 0", Unit: "processes"}
		},
	},
	{
		Name: "git.branch", Doc: "the current branch of a repository", Example: "- git.branch:",
		Main: "dir", Params: []Param{{Name: "dir"}},
		watcher: func(p map[string]string) Watcher {
			return Watcher{Name: "git branch", Cmd: "git rev-parse --abbrev-ref HEAD", Dir: p["dir"]}
		},
	},
	{
		Name: "s3.count", Doc: "objects in an S3 bucket (LocalStack by default), with the aws CLI", Example: "- s3.count: uploads",
		Main: "bucket", Params: []Param{{Name: "bucket", Required: true}, {Name: "prefix"}, {Name: "endpoint", Default: "http://localhost:4566"}},
		watcher: func(p map[string]string) Watcher {
			env := map[string]string{"DEVWATCH_BUCKET": p["bucket"], "DEVWATCH_PREFIX": p["prefix"], "DEVWATCH_ENDPOINT": p["endpoint"]}
			for k, v := range map[string]string{"AWS_ACCESS_KEY_ID": "test", "AWS_SECRET_ACCESS_KEY": "test", "AWS_DEFAULT_REGION": "us-east-1"} {
				if _, set := os.LookupEnv(k); !set {
					env[k] = v // LocalStack takes any credentials; real ones from the environment win
				}
			}
			return Watcher{Name: p["bucket"] + " objects", Unit: "objects", Env: env,
				Cmd: `command -v aws >/dev/null || { echo "aws CLI not found" >&2; exit 127; }
aws --endpoint-url "$DEVWATCH_ENDPOINT" s3 ls "s3://$DEVWATCH_BUCKET/$DEVWATCH_PREFIX" --recursive | wc -l`}
		},
	},
}

var presets = func() map[string]*Preset {
	m := map[string]*Preset{}
	for _, p := range catalog {
		m[p.Name] = p
	}
	return m
}()

//go:embed starter.yaml
var Starter []byte // what `devwatch init` writes

// Presets lists the catalog, for `devwatch presets`.
func Presets() []*Preset { return catalog }

// build reads the preset's value, a plain string for its main param or a map of params, and makes the watcher.
func (p *Preset) build(v *yaml.Node, lookup func(string) (string, bool)) (Watcher, error) {
	params := map[string]string{}
	switch {
	case v.Kind == yaml.ScalarNode && v.Tag == "!!null":
	case v.Kind == yaml.ScalarNode && p.Main == "":
		return Watcher{}, &posError{v, fmt.Errorf("takes settings, as in %s", p.Example)}
	case v.Kind == yaml.ScalarNode:
		params[p.Main] = v.Value
	case v.Kind == yaml.MappingNode:
		for i := 0; i < len(v.Content); i += 2 {
			k, val := v.Content[i], v.Content[i+1]
			if !p.has(k.Value) {
				return Watcher{}, &posError{k, fmt.Errorf("no setting %q; it takes %s", k.Value, p.ParamList())}
			}
			if val.Kind != yaml.ScalarNode {
				return Watcher{}, &posError{val, fmt.Errorf("%s: expected a single value", k.Value)}
			}
			params[k.Value] = val.Value
		}
	default:
		return Watcher{}, &posError{v, fmt.Errorf("expected a value or settings, as in %s", p.Example)}
	}
	for _, prm := range p.Params {
		if params[prm.Name] != "" {
			continue
		}
		if prm.Required {
			return Watcher{}, &posError{v, fmt.Errorf("needs %s, as in %s", prm.Name, p.Example)}
		}
		d, err := expandString(prm.Default, lookup)
		if err != nil {
			return Watcher{}, &posError{v, fmt.Errorf("%s: %w", prm.Name, err)}
		}
		params[prm.Name] = d
	}
	return p.watcher(params), nil
}

func (p *Preset) has(name string) bool {
	for _, prm := range p.Params {
		if prm.Name == name {
			return true
		}
	}
	return false
}

// ParamList describes the params: "table (the plain value), where, dsn (default ${DATABASE_URL})".
func (p *Preset) ParamList() string {
	parts := make([]string, len(p.Params))
	for i, prm := range p.Params {
		parts[i] = prm.Name
		switch {
		case prm.Name == p.Main:
			parts[i] += " (the plain value)"
		case prm.Required:
			parts[i] += " (required)"
		case prm.Default != "":
			parts[i] += " (default " + prm.Default + ")"
		}
	}
	return strings.Join(parts, ", ")
}

func where(p map[string]string) string {
	if p["where"] == "" {
		return ""
	}
	return " WHERE " + p["where"]
}

func sqlQuote(s string) string { return strings.ReplaceAll(s, "'", "''") }

// urlName is a short name for a URL: "localhost:8080/health".
func urlName(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return raw
	}
	return u.Host + u.Path
}
