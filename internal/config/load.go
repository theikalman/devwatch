package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads and validates a config file. Errors say where: "devwatch.yaml:14:5: watcher "orders": …".
// Unknown fields are errors, so typos don't pass silently.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(b, &root); err != nil {
		return nil, fmt.Errorf("%s:%s", path, yamlHint(err))
	}
	if len(root.Content) == 0 {
		return nil, fmt.Errorf("%s: file is empty", path)
	}
	at := func(n *yaml.Node, err error) error {
		var pe *posError
		if errors.As(err, &pe) {
			n, err = pe.n, pe.err
		}
		return fmt.Errorf("%s:%d:%d: %w", path, n.Line, n.Column, err)
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return nil, at(doc, errors.New("expected settings such as title: and watch:"))
	}
	var watch *yaml.Node
	for i := 0; i < len(doc.Content); i += 2 {
		k := doc.Content[i]
		switch {
		case k.Value == "watch":
			watch = doc.Content[i+1]
		case !configKeys[k.Value]:
			return nil, at(k, fmt.Errorf("unknown setting %q", k.Value))
		}
	}
	var c Config
	if err := doc.Decode(&c); err != nil {
		return nil, fmt.Errorf("%s: %v", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	c.Dir = filepath.Dir(abs)
	c.defaults()
	if watch == nil || watch.Kind != yaml.SequenceNode || len(watch.Content) == 0 {
		return nil, at(doc, errors.New("no watchers under watch:"))
	}
	seen := map[string]string{}
	for _, n := range watch.Content {
		w, err := c.entry(n)
		if err != nil {
			if w.Name != "" {
				err = wrap(err, fmt.Sprintf("watcher %q", w.Name))
			}
			return nil, at(n, err)
		}
		if other, ok := seen[w.Key]; ok {
			return nil, at(n, fmt.Errorf("watcher %q: name is too close to %q (both become %q)", w.Name, other, w.Key))
		}
		seen[w.Key] = w.Name
		c.Watch = append(c.Watch, w)
	}
	return &c, nil
}

// entry reads one watcher: a preset line or a source, plus the common settings.
func (c *Config) entry(n *yaml.Node) (Watcher, error) {
	var w Watcher
	if n.Kind != yaml.MappingNode {
		return w, errors.New("each watcher is a map of settings, such as name: and cmd:")
	}
	if err := c.expand(n); err != nil {
		return w, err
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == "name" {
			w.Name = n.Content[i+1].Value // known early, so errors below can say which watcher
		}
	}
	rest := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	var preset *Preset
	var presetValue *yaml.Node
	for i := 0; i < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if p, ok := presets[k.Value]; ok {
			if preset != nil {
				return w, &posError{k, fmt.Errorf("one preset per watcher; this one already uses %s", preset.Name)}
			}
			preset, presetValue = p, v
			continue
		}
		if !watcherKeys[k.Value] {
			if strings.Contains(k.Value, ".") {
				return w, &posError{k, fmt.Errorf("unknown preset %q; devwatch presets lists them", k.Value)}
			}
			return w, &posError{k, fmt.Errorf("unknown field %q", k.Value)}
		}
		if k.Value == "docker" && v.Kind == yaml.MappingNode {
			for j := 0; j < len(v.Content); j += 2 {
				if !dockerKeys[v.Content[j].Value] {
					return w, &posError{v.Content[j], fmt.Errorf("docker: unknown field %q; use container, compose or label", v.Content[j].Value)}
				}
			}
		}
		rest.Content = append(rest.Content, k, v)
	}
	if preset != nil {
		var err error
		if w, err = preset.build(presetValue, c.lookup); err != nil {
			return w, wrap(err, preset.Name)
		}
	}
	if err := rest.Decode(&w); err != nil {
		return w, err
	}
	if w.Name == "" {
		return w, errors.New("name is required")
	}
	if w.Key = slug(w.Name); w.Key == "" {
		return w, errors.New("name needs a letter or digit")
	}
	return w, w.validate(c)
}

// lookup resolves ${name}: vars: first, then the environment.
func (c *Config) lookup(name string) (string, bool) {
	if v, ok := c.Vars[name]; ok {
		return v, true
	}
	return os.LookupEnv(name)
}

var varRef = regexp.MustCompile(`\$(\$?)\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// expandString replaces ${name}; $${name} stays as ${name}, for the shell.
func expandString(s string, lookup func(string) (string, bool)) (string, error) {
	var missing string
	out := varRef.ReplaceAllStringFunc(s, func(m string) string {
		g := varRef.FindStringSubmatch(m)
		if g[1] == "$" {
			return "${" + g[2] + "}"
		}
		v, ok := lookup(g[2])
		if !ok && missing == "" {
			missing = g[2]
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("${%s} is not in vars: or the environment (write $${%s} to leave it to the shell)", missing, missing)
	}
	return out, nil
}

// expand replaces ${name} in every value under n.
func (c *Config) expand(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		v, err := expandString(n.Value, c.lookup)
		if err != nil {
			return &posError{n, err}
		}
		n.Value = v
	}
	for i, ch := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 {
			continue // keys stay as written
		}
		if err := c.expand(ch); err != nil {
			return err
		}
	}
	return nil
}

// posError places an error at a node deeper than the watcher entry.
type posError struct {
	n   *yaml.Node
	err error
}

func (e *posError) Error() string { return e.err.Error() }
func (e *posError) Unwrap() error { return e.err }

// wrap prefixes err's message and keeps its position.
func wrap(err error, prefix string) error {
	var pe *posError
	if errors.As(err, &pe) {
		return &posError{pe.n, fmt.Errorf("%s: %w", prefix, pe.err)}
	}
	return fmt.Errorf("%s: %w", prefix, err)
}

var yamlLine = regexp.MustCompile(`^yaml: line (\d+): `)

// yamlHint turns "yaml: line 3: …" into "3: …", explaining the two mistakes YAML reports most cryptically.
func yamlHint(err error) string {
	msg := err.Error()
	if yamlLine.MatchString(msg) {
		msg = yamlLine.ReplaceAllString(msg, "$1: ")
	} else {
		msg = " " + msg
	}
	switch {
	case strings.Contains(msg, "mapping values are not allowed"):
		msg += ` (a value containing ": ", such as JSON, needs quotes or a | block)`
	case strings.Contains(msg, "did not find expected comment or line break"):
		msg += ` (a value starting with > or | needs quotes, as in warn: "> 18")`
	}
	return msg
}

func keysOf(t reflect.Type, extra ...string) map[string]bool {
	keys := map[string]bool{}
	for i := range t.NumField() {
		if name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ","); name != "" && name != "-" {
			keys[name] = true
		}
	}
	for _, k := range extra {
		keys[k] = true
	}
	return keys
}

var (
	configKeys  = keysOf(reflect.TypeFor[Config]())
	watcherKeys = keysOf(reflect.TypeFor[Watcher]())
	dockerKeys  = keysOf(reflect.TypeFor[Docker]())
)
