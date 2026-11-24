// Package value turns raw check output into typed values, decides their health and describes their changes.
package value

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Kind is the shape of a value; the UI has one card body per kind.
type Kind string

const (
	Number Kind = "number"
	Text   Kind = "text"
	JSON   Kind = "json"
	Table  Kind = "table"
	Probe  Kind = "probe"
)

// Health of one check.
const (
	OK    = "ok"
	Warn  = "warn"
	Crit  = "crit"
	Error = "error" // devwatch could not get a value
)

// Value is one typed check result.
type Value struct {
	Kind Kind
	Data any      // Number: float64 · Text: string · JSON: decoded JSON · Table: TableData · Probe: ProbeData
	Num  *float64 // the number charted for it: the number itself, a table's row count, a probe's latency in ms
}

// TableData is rows of text under a header.
type TableData struct {
	Cols      []string   `json:"cols"`
	Rows      [][]string `json:"rows"`
	Truncated bool       `json:"truncated,omitempty"`
}

// ProbeData answers "is it up?". Reason says why not.
type ProbeData struct {
	Up     bool   `json:"up"`
	Code   int    `json:"code,omitempty"`
	Reason string `json:"reason,omitempty"`
}

func NewNumber(f float64) Value { return Value{Number, f, &f} }
func NewText(s string) Value    { return Value{Kind: Text, Data: s} }
func NewJSON(d any) Value       { return Value{Kind: JSON, Data: d} }

func NewTable(t TableData) Value {
	n := float64(len(t.Rows))
	return Value{Table, t, &n}
}

// NewProbe makes a probe value; latencyMS is nil when it is down.
func NewProbe(p ProbeData, latencyMS *float64) Value { return Value{Probe, p, latencyMS} }

// JSON is the canonical encoding of v.Data: equal values give equal bytes, since encoding/json sorts map keys.
// It cannot fail: numbers are kept finite and JSON data comes from json.Unmarshal.
func (v Value) JSON() []byte {
	b, _ := json.Marshal(v.Data)
	return b
}

// Decode rebuilds a value from its kind and canonical JSON, as stored.
func Decode(k Kind, raw []byte) (Value, error) {
	var err error
	switch k {
	case Number:
		var f float64
		err = json.Unmarshal(raw, &f)
		return NewNumber(f), err
	case Text:
		var s string
		err = json.Unmarshal(raw, &s)
		return NewText(s), err
	case JSON:
		var d any
		err = json.Unmarshal(raw, &d)
		return NewJSON(d), err
	case Table:
		var t TableData
		err = json.Unmarshal(raw, &t)
		return NewTable(t), err
	case Probe:
		var p ProbeData
		err = json.Unmarshal(raw, &p)
		return NewProbe(p, nil), err
	}
	return Value{}, fmt.Errorf("unknown kind %q", k)
}

// String is v on one line, for the terminal and for a first value's summary.
func (v Value) String() string {
	switch v.Kind {
	case Number:
		return formatNum(v.Data.(float64))
	case Text:
		return oneLine(v.Data.(string))
	case Table:
		return plural(len(v.Data.(TableData).Rows), "row")
	case Probe:
		s := probeState(v.Data.(ProbeData))
		if v.Num != nil {
			s += " · " + formatNum(*v.Num) + " ms"
		}
		return s
	}
	return oneLine(string(v.JSON()))
}

func probeState(p ProbeData) string {
	switch {
	case !p.Up && p.Reason != "":
		return "DOWN: " + p.Reason
	case !p.Up:
		return "DOWN"
	case p.Code > 0:
		return "UP " + strconv.Itoa(p.Code)
	}
	return "UP"
}

func formatNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

// parseNum accepts finite numbers only, so "NaN" and "inf" stay text and JSON encoding never fails.
func parseNum(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
}

// oneLine is the first line of s cut to 80 characters, noting how many lines were left out.
func oneLine(s string) string {
	first, rest, multi := strings.Cut(s, "\n")
	if r := []rune(first); len(r) > 80 {
		first = string(r[:79]) + "…"
	}
	if multi {
		first += fmt.Sprintf(" (+%d lines)", strings.Count(rest, "\n")+1)
	}
	return first
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return strconv.Itoa(n) + " " + unit + "s"
}
