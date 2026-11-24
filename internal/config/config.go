// Package config loads and validates devwatch.yaml.
package config

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"devwatch/internal/value"
)

type Config struct {
	Title   string        `yaml:"title"`
	Every   time.Duration `yaml:"every"`   // default check interval
	Timeout time.Duration `yaml:"timeout"` // default check timeout
	Keep    time.Duration `yaml:"keep"`    // history retention
	DB      string        `yaml:"db"`      // SQLite file, relative to the config file
	Watch   []Watcher     `yaml:"watch"`

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

	// Set by Load.
	Key                            string         `yaml:"-"` // slug of Name; history follows it
	MatchRe                        *regexp.Regexp `yaml:"-"`
	WarnRule, CritRule, ExpectRule *value.Rule    `yaml:"-"`
}

// Source names the watcher's source: "cmd" or "port".
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
	return s
}

// Load reads and validates a config file. Unknown fields are errors, so typos don't pass silently.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: file is empty", path)
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c.Dir = filepath.Dir(abs)
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) validate() error {
	c.Every = orDefault(c.Every, 5*time.Second)
	c.Timeout = orDefault(c.Timeout, 5*time.Second)
	c.Keep = orDefault(c.Keep, 24*time.Hour)
	if c.DB == "" {
		c.DB = filepath.Join(".devwatch", "data.db")
	}
	c.DB = c.abs(c.DB)
	if len(c.Watch) == 0 {
		return errors.New("no watchers under watch:")
	}
	seen := map[string]string{}
	for i := range c.Watch {
		w := &c.Watch[i]
		if w.Name == "" {
			return fmt.Errorf("watch #%d: name is required", i+1)
		}
		if w.Key = slug(w.Name); w.Key == "" {
			return fmt.Errorf("watcher %q: name needs a letter or digit", w.Name)
		}
		if other, ok := seen[w.Key]; ok {
			return fmt.Errorf("watcher %q: name is too close to %q (both become %q)", w.Name, other, w.Key)
		}
		seen[w.Key] = w.Name
		if err := w.validate(c); err != nil {
			return fmt.Errorf("watcher %q: %w", w.Name, err)
		}
	}
	return nil
}

func (w *Watcher) validate(c *Config) error {
	w.Every = orDefault(w.Every, c.Every)
	w.Timeout = orDefault(w.Timeout, c.Timeout)
	switch src := w.sources(); len(src) {
	case 0:
		return errors.New("needs a source: cmd or port")
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
	w.Dir = c.abs(w.Dir)
	return nil
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
