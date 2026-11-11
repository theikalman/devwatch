package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"sync"
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

type change struct {
	at    time.Time
	lines []string
}

const maxHist = 100

type pane struct {
	result
	seen bool
	hist []change // oldest first; rendered newest first

	// lines scrolled away from the default view: down from the top, or up from the bottom for streams
	scroll int

	// stream watchers only
	lines []string
	done  bool
}

type model struct {
	cfg           *config.Config
	panes         []pane
	width, height int
	focus         int          // pane the keyboard scrolls
	events        chan tea.Msg // stream watcher events
}

// cell returns the grid shape and the size of one pane.
func (m model) cell() (cols, rows, w, h int) {
	cols = min(m.cfg.Columns, len(m.panes))
	rows = (len(m.panes) + cols - 1) / cols
	return cols, rows, m.width / cols, (m.height - 1) / rows // -1 for the footer
}

// viewH is how many body lines fit in a pane h tall: minus border (2) and header (1).
func viewH(h int) int { return max(h-3, 0) }

// scroll moves pane i's view by d lines (positive = down).
func (m model) scroll(i, d int) {
	_, _, _, h := m.cell()
	p := &m.panes[i]
	hi := max(len(m.content(i))-viewH(h), 0)
	if m.cfg.Watchers[i].Type == "stream" {
		d = -d // streams are anchored to the bottom
	}
	p.scroll = max(min(min(p.scroll, hi)+d, hi), 0)
}

// poll runs one check for watcher i, then reports back; Update re-arms it after the interval.
func poll(cfg *config.Config, i int, delay time.Duration) tea.Cmd {
	return func() tea.Msg {
		time.Sleep(delay)
		start := time.Now()
		out, err := watcher.Check(context.Background(), cfg.Watchers[i])
		if cfg.LogDir != "" {
			err = errors.Join(err, appendLog(cfg.LogDir, cfg.Watchers[i].Name, start, out, err))
		}
		return result{i, out, err, start}
	}
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{waitEvent(m.events)}
	for i, w := range m.cfg.Watchers {
		if w.Type != "stream" {
			cmds = append(cmds, poll(m.cfg, i, 0))
		}
	}
	return tea.Batch(cmds...)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		_, _, _, h := m.cell()
		n := len(m.panes)
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.focus = (m.focus + 1) % n
		case "shift+tab":
			m.focus = (m.focus + n - 1) % n
		case "up", "k":
			m.scroll(m.focus, -1)
		case "down", "j":
			m.scroll(m.focus, 1)
		case "pgup":
			m.scroll(m.focus, -viewH(h))
		case "pgdown":
			m.scroll(m.focus, viewH(h))
		case "home":
			m.scroll(m.focus, -maxLines*maxHist)
		case "end":
			m.scroll(m.focus, maxLines*maxHist)
		}
	case tea.MouseMsg:
		d := 0
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			d = -3
		case tea.MouseButtonWheelDown:
			d = 3
		}
		cols, rows, w, h := m.cell()
		if c, r := msg.X/max(w, 1), msg.Y/max(h, 1); d != 0 && c < cols && r < rows && r*cols+c < len(m.panes) {
			m.focus = r*cols + c
			m.scroll(m.focus, d)
		}
	case streamLine:
		p := &m.panes[msg.idx]
		p.seen, p.at = true, msg.at
		if p.scroll > 0 {
			p.scroll++ // keep the lines being read in place while new ones arrive
		}
		p.lines = append(p.lines, msg.line)
		if len(p.lines) > maxLines {
			p.lines = p.lines[len(p.lines)-maxLines:]
		}
		return m, waitEvent(m.events)
	case streamExit:
		p := &m.panes[msg.idx]
		p.seen, p.done, p.err = true, true, msg.err
		return m, waitEvent(m.events)
	case result:
		old := m.panes[msg.idx]
		p := pane{result: msg, seen: true}
		p.hist, p.scroll = old.hist, old.scroll
		if old.seen {
			if d := lineDiff(old.out, msg.out); d != nil {
				p.hist = append(p.hist, change{msg.at, d})
				if len(p.hist) > maxHist {
					p.hist = p.hist[1:]
				}
			}
		}
		m.panes[msg.idx] = p
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
	cols, rows, w, h := m.cell()
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
	return lipgloss.JoinVertical(lipgloss.Left, lines...) + "\n" + dim.Render("tab: focus pane · ↑/↓ pgup/pgdn home/end wheel: scroll · q: quit")
}

// content is pane i's body below the header: every line, unclipped.
func (m model) content(i int) []string {
	p := m.panes[i]
	if m.cfg.Watchers[i].Type == "stream" {
		if p.done && p.err != nil {
			return append(append([]string(nil), p.lines...), errStyle.Render(p.err.Error()))
		}
		return p.lines
	}
	body := p.out
	if p.err != nil {
		body = strings.TrimSpace(p.err.Error() + "\n" + body)
	}
	all := strings.Split(body, "\n")
	for k := len(p.hist) - 1; k >= 0; k-- { // newest change first
		c := p.hist[k]
		all = append(all, dim.Render("── change "+c.at.Format("15:04:05")+" ──"))
		for _, l := range c.lines {
			st := errStyle
			if strings.HasPrefix(l, "+ ") {
				st = okStyle
			}
			all = append(all, st.Render(l))
		}
	}
	return all
}

// render draws pane i as a viewport onto content(i); scroll moves the window.
func (m model) render(i, w, h int) string {
	wc, p := m.cfg.Watchers[i], m.panes[i]
	stream := wc.Type == "stream"
	status, color := dim.Render("…"), lipgloss.Color("8")
	switch {
	case stream && p.done && p.err != nil:
		status, color = errStyle.Render("● EXITED"), lipgloss.Color("1")
	case stream && p.done:
		status = dim.Render("● EXITED")
	case stream:
		status, color = okStyle.Render("● LIVE"), lipgloss.Color("2")
	case p.seen && p.err != nil:
		status, color = errStyle.Render("● ERR"), lipgloss.Color("1")
	case p.seen:
		status, color = okStyle.Render("● OK"), lipgloss.Color("2")
	}
	head := fmt.Sprintf("%s %s", lipgloss.NewStyle().Bold(true).Render(wc.Name), status)
	if p.seen {
		head += dim.Render(" " + p.at.Format("15:04:05"))
	}
	all, vh := m.content(i), viewH(h)
	top := p.scroll
	if stream {
		top = len(all) - vh - p.scroll
	}
	top = max(min(top, len(all)-vh), 0)
	end := min(top+vh, len(all))
	if len(all) > vh {
		head += dim.Render(fmt.Sprintf(" ↕ %d-%d/%d", top+1, end, len(all)))
	}
	line := lipgloss.NewStyle().MaxWidth(max(w-4, 1)) // border + padding
	rows := []string{line.Render(head)}
	for _, l := range all[top:end] {
		rows = append(rows, line.Render(l))
	}
	border := lipgloss.RoundedBorder()
	if i == m.focus {
		border = lipgloss.ThickBorder()
	}
	return lipgloss.NewStyle().Border(border).BorderForeground(color).
		Padding(0, 1).Width(w - 2).Height(h - 2).Render(strings.Join(rows, "\n"))
}

func main() {
	path := flag.String("c", "devwatch.yaml", "config file")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	if cfg.LogDir != "" {
		if err := os.MkdirAll(cfg.LogDir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "log_dir:", err)
			os.Exit(1)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	events := make(chan tea.Msg, 256)
	startStreams(ctx, &wg, cfg, events)
	m := model{cfg: cfg, panes: make([]pane, len(cfg.Watchers)), events: events}
	_, err = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion()).Run()
	cancel()
	wg.Wait() // stream commands are killed before we exit
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
