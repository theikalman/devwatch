package server

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"devwatch/internal/config"
	"devwatch/internal/value"
)

const hidden = "••••"

var (
	secretAssign = regexp.MustCompile(`(?i)\b(\w*(?:secret|token|passw(?:or)?d|api_?key|access_?key|auth|credential)\w*)=("[^"]*"|'[^']*'|\S+)`)
	urlPassword  = regexp.MustCompile(`(://[^:/@\s]+):[^@\s]+@`)
	secretHeader = regexp.MustCompile(`(?i)auth|cookie|token|key|secret|session|passw`)
	spaces       = regexp.MustCompile(`\s+`)
)

// redact hides secret-looking assignments (API_TOKEN=…) and passwords in URLs (postgres://user:…@host).
func redact(s string) string {
	s = secretAssign.ReplaceAllString(s, "${1}="+hidden)
	return urlPassword.ReplaceAllString(s, "${1}:"+hidden+"@")
}

// target is the one-line "where from" shown under a card's name.
func target(w *config.Watcher) string {
	switch w.Source() {
	case "port":
		return w.Port
	case "sql":
		return redact(spaces.ReplaceAllString(strings.TrimSpace(w.SQL), " "))
	case "http":
		if w.Method != "GET" {
			return w.Method + " " + redact(w.HTTP)
		}
		return redact(w.HTTP)
	case "docker":
		switch d := w.Docker; {
		case d.Container != "":
			return "container " + d.Container
		case d.Compose != "":
			return "compose " + d.Compose
		default:
			return "label " + d.Label
		}
	}
	return redact(w.Cmd)
}

func rules(w *config.Watcher) map[string]string {
	m := map[string]string{}
	add := func(name string, r *value.Rule) {
		if r != nil {
			m[name] = r.String()
		}
	}
	add("warn", w.WarnRule)
	add("crit", w.CritRule)
	add("expect", w.ExpectRule)
	if len(m) == 0 {
		return nil
	}
	return m
}

// spec is the watcher's settings as YAML for the drawer, with env values and secret-looking values hidden.
func spec(cfg *config.Config, w *config.Watcher) string {
	var b strings.Builder
	line := func(k, v string) {
		switch {
		case v == "":
		case strings.Contains(v, "\n"):
			fmt.Fprintf(&b, "%s: |\n  %s\n", k, strings.ReplaceAll(strings.TrimSpace(v), "\n", "\n  "))
		default:
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	mapping := func(k string, m map[string]string, show func(k, v string) string) {
		if len(m) == 0 {
			return
		}
		fmt.Fprintf(&b, "%s:\n", k)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s: %s\n", k, show(k, m[k]))
		}
	}
	line("name", w.Name)
	line("cmd", redact(w.Cmd))
	mapping("env", w.Env, func(string, string) string { return hidden })
	if w.Cmd != "" && w.Dir != cfg.Dir {
		line("dir", w.Dir)
	}
	line("port", w.Port)
	line("sql", redact(w.SQL))
	line("dsn", redact(w.DSN))
	line("http", redact(w.HTTP))
	if w.HTTP != "" && w.Method != "GET" {
		line("method", w.Method)
	}
	mapping("headers", w.Headers, func(k, v string) string {
		if secretHeader.MatchString(k) {
			return hidden
		}
		return v
	})
	line("body", redact(w.Body))
	if w.HTTP != "" && w.Status != "2xx" {
		line("status", w.Status)
	}
	if d := w.Docker; d != nil {
		b.WriteString("docker:\n")
		for _, kv := range [][2]string{{"container", d.Container}, {"compose", d.Compose}, {"label", d.Label}} {
			if kv[1] != "" {
				fmt.Fprintf(&b, "  %s: %s\n", kv[0], kv[1])
			}
		}
	}
	line("socket", w.Socket)
	line("as", w.As)
	line("value", w.Value)
	line("match", w.Match)
	line("warn", w.Warn)
	line("crit", w.Crit)
	line("expect", w.Expect)
	line("unit", w.Unit)
	line("group", w.Group)
	line("every", w.Every.String())
	line("timeout", w.Timeout.String())
	return strings.TrimSuffix(b.String(), "\n")
}
