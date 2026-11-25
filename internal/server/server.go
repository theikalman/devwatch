// Package server serves the dashboard: the embedded web UI, a server-sent event stream of checks,
// and a small JSON API for history and run-now.
package server

import (
	"encoding/json"
	"log"
	"mime"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"devwatch/internal/config"
	"devwatch/internal/store"
	"devwatch/web"
)

func init() { mime.AddExtensionType(".woff2", "font/woff2") }

type Server struct {
	st       *store.Store
	hub      *hub
	runNow   func(key string) bool
	ids      map[string]int64
	keys     []string
	loopback bool
}

// New loads each watcher's recent state from st. runNow asks the engine for an extra check.
// With loopbackOnly, requests must name a loopback host, which stops DNS-rebinding pages from reading the dashboard.
func New(cfg *config.Config, configPath string, st *store.Store, runNow func(key string) bool, loopbackOnly bool) (*Server, error) {
	s := &Server{st: st, runNow: runNow, ids: map[string]int64{}, loopback: loopbackOnly}
	for _, w := range cfg.Watch {
		id, err := st.Watcher(w.Key, w.Name, w.Source())
		if err != nil {
			return nil, err
		}
		s.ids[w.Key] = id
		s.keys = append(s.keys, w.Key)
	}
	h, err := newHub(cfg, st, s.ids, filepath.Base(configPath))
	if err != nil {
		return nil, err
	}
	s.hub = h
	return s, nil
}

// Report sends a finished check to the browsers. Safe for concurrent use.
func (s *Server) Report(key string, smp store.Sample) { s.hub.report(key, smp) }

// Close ends the browsers' event streams; call it when the HTTP server shuts down.
func (s *Server) Close() { s.hub.closeAll() }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web.FS))
	mux.HandleFunc("GET /api/events", s.events)
	mux.HandleFunc("GET /api/changes", s.changes)
	mux.HandleFunc("GET /api/watchers/{key}/samples", s.samples)
	mux.HandleFunc("POST /api/watchers/{key}/run", s.run)
	h := http.NewCrossOriginProtection().Handler(mux) // POSTs from other sites get 403
	h = securityHeaders(h)
	if s.loopback {
		h = loopbackHost(h)
	}
	return h
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	snap, events, cancel := s.hub.subscribe()
	defer cancel()
	write := func(b []byte) bool {
		_, err := w.Write(b)
		flusher.Flush()
		return err == nil
	}
	if !write(append([]byte("retry: 2000\n\n"), snap...)) {
		return
	}
	ping := time.NewTicker(20 * time.Second) // keeps proxies and idle timeouts from closing the stream
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-events:
			if !ok || !write(msg) {
				return
			}
		case <-ping.C:
			if !write([]byte(": ping\n\n")) {
				return
			}
		}
	}
}

func (s *Server) samples(w http.ResponseWriter, r *http.Request) {
	id, ok := s.ids[r.PathValue("key")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	since := time.Now().Add(-time.Hour)
	if v := r.URL.Query().Get("since"); v != "" {
		ms, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, "since: unix milliseconds expected", http.StatusBadRequest)
			return
		}
		since = time.UnixMilli(ms)
	}
	// shortcut: a day at 5s is ~17k rows, sent whole; downsample here if long ranges get slow.
	rows, err := s.st.Samples(id, since, 20_000)
	if err != nil {
		serverError(w, err)
		return
	}
	out := make([]sampleJSON, len(rows))
	for i, row := range rows {
		out[i] = toJSON("", row)
	}
	writeJSON(w, map[string]any{"samples": out})
}

func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	before := time.Now().Add(time.Minute)
	if v := r.URL.Query().Get("before"); v != "" {
		ms, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, "before: unix milliseconds expected", http.StatusBadRequest)
			return
		}
		before = time.UnixMilli(ms)
	}
	limit := 50
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = min(v, 500)
	}
	cs, err := s.st.Changes(s.keys, before, limit)
	if err != nil {
		serverError(w, err)
		return
	}
	type changeJSON struct {
		Key        string `json:"key"`
		Name       string `json:"name"`
		At         int64  `json:"at"`
		Health     string `json:"health"`
		PrevHealth string `json:"prev_health"`
		Summary    string `json:"summary"`
	}
	out := make([]changeJSON, len(cs))
	for i, c := range cs {
		out[i] = changeJSON{c.Key, c.Name, c.At.UnixMilli(), c.Health, c.PrevHealth, c.Summary}
	}
	writeJSON(w, map[string]any{"changes": out, "more": len(cs) == limit})
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	if !s.runNow(r.PathValue("key")) {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// loopbackHost refuses requests whose Host isn't a loopback name.
func loopbackHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		switch strings.Trim(host, "[]") {
		case "localhost", "127.0.0.1", "::1":
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "devwatch only answers on localhost", http.StatusForbidden)
		}
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func serverError(w http.ResponseWriter, err error) {
	log.Printf("api: %v", err)
	http.Error(w, "internal error, see the devwatch terminal", http.StatusInternalServerError)
}
