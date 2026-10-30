package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"devwatch/internal/config"
	"devwatch/internal/watcher"
)

type result struct {
	idx int
	out string
	err error
	at  time.Time
}

type pane struct {
	result
	seen bool
}

type model struct {
	cfg           *config.Config
	panes         []pane
	width, height int
}

// poll runs one check for watcher i, then reports back; Update re-arms it after the interval.
func poll(cfg *config.Config, i int, delay time.Duration) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(delay)
		out, err := watcher.Check(context.Background(), cfg.Watchers[i])
		return result{i, out, err, time.Now()}
	}
}

func (m model) Init() tea.Cmd {
	cmds := make([]tea.Cmd, len(m.cfg.Watchers))
	for i := range cmds {
		cmds[i] = poll(m.cfg, i, 0)
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		if s := msg.String(); s == "q" || s == "ctrl+c" {
			return m, tea.Quit
		}
	case result:
		m.panes[msg.idx] = pane{msg, true}
		return m, poll(m.cfg, msg.idx, m.cfg.Watchers[msg.idx].Interval)
	}
	return m, nil
}

var (
	okStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	errStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	dim      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

func (m model) View() string {
	if m.width == 0 {
		return ""
	}
	cols := min(m.cfg.Columns, len(m.panes))
	rows := (len(m.panes) + cols - 1) / cols
	w, h := m.width/cols, (m.height-1)/rows // -1 for the footer
	var lines []string
	for r := 0; r < rows; r++ {
		var row []string
		for c := 0; c < cols; c++ {
			if i := r*cols + c; i < len(m.panes) {
				row = append(row, m.render(i, w, h))
			}
		}
		lines = append(lines, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	}
	return lipgloss.JoinVertical(lipgloss.Left, lines...) + "\n" + dim.Render("q: quit")
}

func (m model) render(i, w, h int) string {
	wc, p := m.cfg.Watchers[i], m.panes[i]
	status, color := dim.Render("…"), lipgloss.Color("8")
	if p.seen {
		status, color = okStyle.Render("● OK"), lipgloss.Color("2")
		if p.err != nil {
			status, color = errStyle.Render("● ERR"), lipgloss.Color("1")
		}
	}
	body := p.out
	if p.err != nil {
		body = strings.TrimSpace(p.err.Error() + "\n" + body)
	}
	innerW, innerH := max(w-4, 1), max(h-2, 1) // border + padding
	head := fmt.Sprintf("%s %s", lipgloss.NewStyle().Bold(true).Render(wc.Name), status)
	if p.seen {
		head += dim.Render(" " + p.at.Format("15:04:05"))
	}
	all := append([]string{head}, strings.Split(body, "\n")...)
	if len(all) > innerH {
		all = all[:innerH]
	}
	for j, l := range all {
		all[j] = lipgloss.NewStyle().MaxWidth(innerW).Render(l)
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color).
		Padding(0, 1).Width(w - 2).Height(h - 2).Render(strings.Join(all, "\n"))
}

func main() {
	path := flag.String("c", "devwatch.yaml", "config file")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	m := model{cfg: cfg, panes: make([]pane, len(cfg.Watchers))}
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
