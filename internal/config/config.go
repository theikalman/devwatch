package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Watcher struct {
	Name     string        `yaml:"name"`
	Type     string        `yaml:"type"` // command | sql
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`

	Command string `yaml:"command"` // command: run via sh -c

	DSN   string `yaml:"dsn"`   // sql: postgres DSN
	Query string `yaml:"query"` // sql
}

type Config struct {
	Columns  int       `yaml:"columns"`
	LogDir   string    `yaml:"log_dir"` // optional: append every check to <log_dir>/<name>.log
	Watchers []Watcher `yaml:"watchers"`
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := yaml.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, c.validate()
}

func (c *Config) validate() error {
	if c.Columns <= 0 {
		c.Columns = 2
	}
	if len(c.Watchers) == 0 {
		return fmt.Errorf("no watchers configured")
	}
	for i := range c.Watchers {
		w := &c.Watchers[i]
		if w.Name == "" {
			return fmt.Errorf("watcher #%d: name is required", i+1)
		}
		if w.Interval <= 0 {
			w.Interval = 5 * time.Second
		}
		if w.Timeout <= 0 {
			w.Timeout = 5 * time.Second
		}
		var missing string
		switch w.Type {
		case "command":
			if w.Command == "" {
				missing = "command"
			}
		case "sql":
			if w.DSN == "" || w.Query == "" {
				missing = "dsn and query"
			}
		default:
			return fmt.Errorf("watcher %q: unknown type %q", w.Name, w.Type)
		}
		if missing != "" {
			return fmt.Errorf("watcher %q: %s required for type %s", w.Name, missing, w.Type)
		}
	}
	return nil
}
