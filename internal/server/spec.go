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
)

// redact hides secret-looking assignments (API_TOKEN=…) and passwords in URLs (postgres://user:…@host).
func redact(s string) string {
	s = secretAssign.ReplaceAllString(s, "${1}="+hidden)
	return urlPassword.ReplaceAllString(s, "${1}:"+hidden+"@")
}

// target is the one-line "where from" shown under a card's name.
func target(w *config.Watcher) string {
	if w.Port != "" {
		return w.Port
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
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
		}
	}
	line("name", w.Name)
	line("cmd", redact(w.Cmd))
	if len(w.Env) > 0 {
		b.WriteString("env:\n")
		keys := make([]string, 0, len(w.Env))
		for k := range w.Env {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s: %s\n", k, hidden)
		}
	}
	if w.Cmd != "" && w.Dir != cfg.Dir {
		line("dir", w.Dir)
	}
	line("port", w.Port)
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
