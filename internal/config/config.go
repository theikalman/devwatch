// Package config loads and validates devwatch.yaml.
package config

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"devwatch/internal/value"
)

type Config struct {
	Title   string            `yaml:"title"`
	Every   time.Duration     `yaml:"every"`   // default check interval
	Timeout time.Duration     `yaml:"timeout"` // default check timeout
	Keep    time.Duration     `yaml:"keep"`    // history retention
	DB      string            `yaml:"db"`      // SQLite file, relative to the config file
	Vars    map[string]string `yaml:"vars"`    // ${name} values; the environment fills the rest
	Watch   []Watcher         `yaml:"-"`       // read entry by entry, so presets and errors can be placed

	Dir string `yaml:"-"` // directory of the config file; relative paths resolve against it
}

type Watcher struct {
	Name    string        `yaml:"name"`
	Group   string        `yaml:"group"`
	Every   time.Duration `yaml:"every"`
	Timeout time.Duration `yaml:"timeout"`
	Unit    string        `yaml:"unit"`
	As      string        `yaml:"as"`    // number | text | json | table; empty infers the kind
	Value   string        `yaml:"value"` // dotted path into JSON output
	Match   string        `yaml:"match"` // regex; keeps the first group, or the whole match
	Warn    string        `yaml:"warn"`
	Crit    string        `yaml:"crit"`
	Expect  string        `yaml:"expect"` // not met = crit

	// Sources: exactly one.
	Cmd  string            `yaml:"cmd"` // run with sh -c
	Env  map[string]string `yaml:"env"`
	Dir  string            `yaml:"dir"`  // cmd working directory; default: the config's directory
	Port string            `yaml:"port"` // host:port, checked with a TCP dial

	SQL string `yaml:"sql"` // a query, run read-only
	DSN string `yaml:"dsn"` // postgres://… (or key=value), or sqlite:path

	HTTP    string            `yaml:"http"` // URL
	Method  string            `yaml:"method"`
	Headers map[string]string `yaml:"headers"`
	Body    string            `yaml:"body"`
	Status  string            `yaml:"status"` // expected status: 200 or 2xx (default)

	Docker *Docker `yaml:"docker"`
	Socket string  `yaml:"socket"` // Docker or Podman API socket; found on its own when empty

	// Set by Load.
	Key                            string         `yaml:"-"` // slug of Name; history follows it
	MatchRe                        *regexp.Regexp `yaml:"-"`
	WarnRule, CritRule, ExpectRule *value.Rule    `yaml:"-"`
}

// Docker picks containers by exactly one of its fields.
type Docker struct {
	Container string `yaml:"container"` // one container, by name
	Compose   string `yaml:"compose"`   // every container of a compose project
	Label     string `yaml:"label"`     // every container with a label: "key" or "key=value"
}

// Source names the watcher's source: "cmd", "port", "sql", "http" or "docker".
func (w *Watcher) Source() string {
	if s := w.sources(); len(s) == 1 {
		return s[0]
	}
	return ""
}

func (w *Watcher) sources() (s []string) {
	if w.Cmd != "" {
		s = append(s, "cmd")
	}
	if w.Port != "" {
		s = append(s, "port")
	}
	if w.SQL != "" {
		s = append(s, "sql")
	}
	if w.HTTP != "" {
		s = append(s, "http")
	}
	if w.Docker != nil {
		s = append(s, "docker")
	}
	return s
}

// defaults fills in the settings that were left out.
func (c *Config) defaults() {
	c.Every = orDefault(c.Every, 5*time.Second)
	c.Timeout = orDefault(c.Timeout, 5*time.Second)
	c.Keep = orDefault(c.Keep, 24*time.Hour)
	if c.DB == "" {
		c.DB = filepath.Join(".devwatch", "data.db")
	}
	c.DB = c.abs(c.DB)
}

func (w *Watcher) validate(c *Config) error {
	w.Every = orDefault(w.Every, c.Every)
	w.Timeout = orDefault(w.Timeout, c.Timeout)
	switch src := w.sources(); len(src) {
	case 0:
		return errors.New("needs a source: cmd, sql, http, docker or port")
	case 1:
	default:
		return fmt.Errorf("has more than one source: %s", strings.Join(src, ", "))
	}
	switch w.As {
	case "", "number", "text", "json", "table":
	default:
		return fmt.Errorf("as: %q is not number, text, json or table", w.As)
	}
	if w.Match != "" {
		re, err := regexp.Compile(w.Match)
		if err != nil {
			return fmt.Errorf("match: %w", err)
		}
		w.MatchRe = re
	}
	for _, r := range []struct {
		name, text string
		dst        **value.Rule
	}{{"warn", w.Warn, &w.WarnRule}, {"crit", w.Crit, &w.CritRule}, {"expect", w.Expect, &w.ExpectRule}} {
		if r.text == "" {
			continue
		}
		rule, err := value.ParseRule(r.text)
		if err != nil {
			return fmt.Errorf("%s: %w", r.name, err)
		}
		*r.dst = rule
	}
	if w.Port != "" {
		if _, _, err := net.SplitHostPort(w.Port); err != nil {
			return fmt.Errorf("port: %w", err)
		}
	}
	if w.SQL != "" {
		if w.DSN == "" {
			return errors.New("sql needs a dsn: postgres://user:pass@host/db or sqlite:path")
		}
		if path, ok := strings.CutPrefix(w.DSN, "sqlite:"); ok {
			w.DSN = "sqlite:" + c.abs(path)
		}
	}
	if w.HTTP != "" {
		if u, err := url.Parse(w.HTTP); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("http: %q is not an http:// or https:// URL", w.HTTP)
		}
		w.Method = strings.ToUpper(cmp.Or(w.Method, "GET"))
		w.Status = cmp.Or(w.Status, "2xx")
		if !statusPattern.MatchString(w.Status) {
			return fmt.Errorf("status: %q is not a code like 200 or a class like 2xx", w.Status)
		}
	}
	if d := w.Docker; d != nil {
		if n := btoi(d.Container != "") + btoi(d.Compose != "") + btoi(d.Label != ""); n != 1 {
			return errors.New("docker needs exactly one of container, compose or label")
		}
	}
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(w.Socket, "~/") {
		w.Socket = filepath.Join(home, w.Socket[2:])
	}
	w.Dir = c.abs(w.Dir)
	return nil
}

var statusPattern = regexp.MustCompile(`^[1-5](xx|[0-9]{2})$`)

// StatusOK reports whether code matches an expected status: "200" exactly, "2xx" by class.
func StatusOK(want string, code int) bool {
	if strings.HasSuffix(want, "xx") {
		return code/100 == int(want[0]-'0')
	}
	return strconv.Itoa(code) == want
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// abs resolves p against the config's directory; empty means that directory.
func (c *Config) abs(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Dir, p)
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}

var notSlug = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// slug is a name reduced to lower-case letters and digits joined by "-": "Order #42" → "order-42".
func slug(name string) string {
	return strings.Trim(notSlug.ReplaceAllString(strings.ToLower(name), "-"), "-")
}
