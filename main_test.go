package main

import (
	"fmt"
	"strings"
	"testing"

	"devwatch/internal/config"
)

func TestScrollViewport(t *testing.T) {
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, fmt.Sprintf("l%02d", i))
	}
	m := model{
		cfg:    &config.Config{Columns: 1, Watchers: []config.Watcher{{Name: "s", Type: "stream"}, {Name: "p"}}},
		panes:  make([]pane, 2),
		width:  30,
		height: 11, // two 5-line panes: 2 body lines each
	}
	m.panes[0].lines = lines
	m.panes[1].seen, m.panes[1].out = true, strings.Join(lines, "\n")

	show := func(i int) string { _, _, w, h := m.cell(); return m.render(i, w, h) }
	has := func(s string, want ...string) {
		t.Helper()
		for _, l := range lines {
			if got, exp := strings.Contains(s, l), contains(want, l); got != exp {
				t.Fatalf("%s: visible=%v want %v in\n%s", l, got, exp, s)
			}
		}
	}

	has(show(0), "l18", "l19") // stream follows the tail
	has(show(1), "l00", "l01") // polled pane starts at the top

	m.scroll(0, -1)
	has(show(0), "l17", "l18")
	m.scroll(0, -1000)
	has(show(0), "l00", "l01")
	m.scroll(0, 1) // overshoot above was clamped, so one step down moves exactly one line
	has(show(0), "l01", "l02")

	m.scroll(1, 1000)
	has(show(1), "l18", "l19")
	m.scroll(1, -1)
	has(show(1), "l17", "l18")
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
