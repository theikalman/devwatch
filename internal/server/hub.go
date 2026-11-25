package server

import (
	"encoding/json"
	"sync"
	"time"

	"devwatch/internal/config"
	"devwatch/internal/store"
	"devwatch/internal/value"
)

const recentN = 60 // checks per watcher kept for sparklines and uptime strips

// sampleJSON is a sample as the browser sees it; times are unix ms.
type sampleJSON struct {
	Key     string          `json:"key,omitempty"`
	At      int64           `json:"at"`
	Health  string          `json:"health"`
	MS      int64           `json:"ms"`
	Num     *float64        `json:"num"`
	Changed bool            `json:"changed,omitempty"`
	Kind    value.Kind      `json:"kind,omitempty"`
	Value   json.RawMessage `json:"value,omitempty"`
	Summary string          `json:"summary,omitempty"`
	Error   string          `json:"error,omitempty"`
	Why     string          `json:"why,omitempty"`
}

func toJSON(key string, s store.Sample) sampleJSON {
	return sampleJSON{key, s.At.UnixMilli(), s.Health, s.MS, s.Num, s.Changed, s.Kind, s.Value, s.Summary, s.Error, s.Why}
}

// point is one check in a watcher's recent history.
type point struct {
	At     int64    `json:"at"`
	Health string   `json:"health"`
	Num    *float64 `json:"num"`
}

// watcherJSON is everything a card needs. The snapshot sends one per watcher; the browser then applies
// each sample event the same way report does.
type watcherJSON struct {
	Key        string            `json:"key"`
	Name       string            `json:"name"`
	Group      string            `json:"group,omitempty"`
	Source     string            `json:"source"`
	Target     string            `json:"target"`
	Unit       string            `json:"unit,omitempty"`
	Every      int64             `json:"every"`   // ms
	Timeout    int64             `json:"timeout"` // ms
	Rules      map[string]string `json:"rules,omitempty"`
	Spec       string            `json:"spec"`
	Kind       value.Kind        `json:"kind,omitempty"`
	Value      json.RawMessage   `json:"value,omitempty"`
	ValueAt    int64             `json:"value_at,omitempty"`
	Prev       json.RawMessage   `json:"prev,omitempty"` // the value before Value
	PrevAt     int64             `json:"prev_at,omitempty"`
	Last       *sampleJSON       `json:"last"`
	ErrorSince int64             `json:"error_since,omitempty"`
	Recent     []point           `json:"recent"`
}

// hub holds every watcher's current state and fans finished checks out to the browsers.
type hub struct {
	mu       sync.Mutex
	title    string
	config   string
	watchers []*watcherJSON
	byKey    map[string]*watcherJSON
	subs     map[chan []byte]struct{}
	closed   bool
	cfgErr   string // why the last edit of the config wasn't loaded; "" once one loads
}

func newHub(cfg *config.Config, st *store.Store, ids map[string]int64, configName string) (*hub, error) {
	ws, err := loadWatchers(cfg, st, ids)
	if err != nil {
		return nil, err
	}
	h := &hub{config: configName, subs: map[chan []byte]struct{}{}}
	h.set(cfg.Title, ws)
	return h, nil
}

func (h *hub) set(title string, ws []*watcherJSON) {
	h.title, h.watchers, h.byKey, h.cfgErr = title, ws, map[string]*watcherJSON{}, ""
	for _, w := range ws {
		h.byKey[w.Key] = w
	}
}

// reset swaps in the watchers of a reloaded config and sends every browser a fresh snapshot.
func (h *hub) reset(title string, ws []*watcherJSON) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.set(title, ws)
	h.broadcast(h.snapshot())
}

// configError tells every browser, now and later, why the edited config wasn't loaded.
func (h *hub) configError(msg string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfgErr = msg
	h.broadcast(frame("config_error", map[string]string{"message": msg}))
}

// loadWatchers builds each watcher's card state from its stored history.
func loadWatchers(cfg *config.Config, st *store.Store, ids map[string]int64) ([]*watcherJSON, error) {
	var out []*watcherJSON
	for i := range cfg.Watch {
		w := &cfg.Watch[i]
		id := ids[w.Key]
		ws := &watcherJSON{
			Key: w.Key, Name: w.Name, Group: w.Group, Source: w.Source(), Target: target(w), Unit: w.Unit,
			Every: w.Every.Milliseconds(), Timeout: w.Timeout.Milliseconds(), Rules: rules(w), Spec: spec(cfg, w),
			Recent: []point{},
		}
		recent, err := st.Samples(id, time.Time{}, recentN)
		if err != nil {
			return nil, err
		}
		for _, s := range recent {
			ws.Recent = append(ws.Recent, point{s.At.UnixMilli(), s.Health, s.Num})
		}
		if len(recent) > 0 {
			last := lastOf(toJSON("", recent[len(recent)-1]))
			ws.Last = &last
		}
		vals, err := st.Values(id, 2)
		if err != nil {
			return nil, err
		}
		if len(vals) > 0 {
			ws.Kind, ws.Value, ws.ValueAt = vals[0].Kind, vals[0].Value, vals[0].At.UnixMilli()
		}
		if len(vals) > 1 {
			ws.Prev, ws.PrevAt = vals[1].Value, vals[1].At.UnixMilli()
		}
		since, err := st.ErrorSince(id)
		if err != nil {
			return nil, err
		}
		if !since.IsZero() {
			ws.ErrorSince = since.UnixMilli()
		}
		out = append(out, ws)
	}
	return out, nil
}

// lastOf keeps what a card's "last check" needs: the value travels separately.
func lastOf(s sampleJSON) sampleJSON {
	s.Key, s.Kind, s.Value, s.Summary, s.Changed = "", "", nil, "", false
	return s
}

// report applies a finished check and sends it to every browser.
func (h *hub) report(key string, s store.Sample) {
	ev := toJSON(key, s)
	msg := frame("sample", ev)
	h.mu.Lock()
	defer h.mu.Unlock()
	w := h.byKey[key]
	if w == nil {
		return
	}
	last := lastOf(ev)
	w.Last = &last
	w.Recent = append(w.Recent, point{ev.At, ev.Health, ev.Num})
	if len(w.Recent) > recentN {
		w.Recent = w.Recent[len(w.Recent)-recentN:]
	}
	if s.Value != nil {
		w.Prev, w.PrevAt = w.Value, w.ValueAt
		if s.Kind != w.Kind {
			w.Prev, w.PrevAt = nil, 0
		}
		w.Kind, w.Value, w.ValueAt = s.Kind, s.Value, ev.At
	}
	switch {
	case s.Health != value.Error:
		w.ErrorSince = 0
	case w.ErrorSince == 0:
		w.ErrorSince = ev.At
	}
	h.broadcast(msg)
}

// broadcast sends msg to every browser; the caller holds h.mu.
func (h *hub) broadcast(msg []byte) {
	for ch := range h.subs {
		select {
		case ch <- msg:
		default: // too slow: drop it; its EventSource reconnects and gets a fresh snapshot
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// snapshot is the whole dashboard as one event; the caller holds h.mu.
func (h *hub) snapshot() []byte {
	return frame("snapshot", struct {
		Title       string         `json:"title"`
		Config      string         `json:"config"`
		ConfigError string         `json:"config_error,omitempty"`
		Watchers    []*watcherJSON `json:"watchers"`
	}{h.title, h.config, h.cfgErr, h.watchers})
}

// subscribe returns the current snapshot and a channel of the events after it; cancel stops them.
func (h *hub) subscribe() (snapshot []byte, events <-chan []byte, cancel func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan []byte, 256)
	if h.closed {
		close(ch)
	} else {
		h.subs[ch] = struct{}{}
	}
	return h.snapshot(), ch, func() { h.drop(ch) }
}

func (h *hub) drop(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
}

// closeAll ends every browser's stream, so the HTTP server can shut down.
func (h *hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for ch := range h.subs {
		delete(h.subs, ch)
		close(ch)
	}
}

// frame is one server-sent event. encoding/json never writes a raw newline, so one data line is enough.
func frame(event string, v any) []byte {
	b, _ := json.Marshal(v) // cannot fail: plain structs, and stored values are valid JSON
	return []byte("event: " + event + "\ndata: " + string(b) + "\n\n")
}
