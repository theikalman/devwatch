package value

import (
	"regexp"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		out, as, path, match string
		kind                 Kind
		want                 string
	}{
		{"  42\n", "", "", "", Number, "42"},
		{"3.5", "", "", "", Number, "3.5"},
		{"NaN", "", "", "", Text, "NaN"},
		{"paid", "", "", "", Text, "paid"},
		{"42", "text", "", "", Text, "42"},
		{`{"b":[1,{"c":"x"}],"a":true}`, "", "", "", JSON, `{"a":true,"b":[1,{"c":"x"}]}`},
		{`{"a":{"b":[1,{"c":"x"}]}}`, "", "a.b.1.c", "", Text, "x"},
		{`{"n": 7}`, "", "n", "", Number, "7"},
		{`{"n": 7}`, "text", "n", "", Text, "7"},
		{`{"ok": true}`, "", "ok", "", Text, "true"},
		{`{"o": {"k": 1}}`, "", "o", "", JSON, `{"k":1}`},
		{"Active: running (since x)", "", "", `Active: (\w+)`, Text, "running"},
		{"load 0.52", "", "", `[\d.]+`, Number, "0.52"},
		{"name\tstate\napi\trunning\nworker\tstopped\n", "table", "", "", Table, "2 rows"},
		{"line one\nline two\nline three", "", "", "", Text, "line one (+2 lines)"},
	}
	for _, tt := range tests {
		var re *regexp.Regexp
		if tt.match != "" {
			re = regexp.MustCompile(tt.match)
		}
		v, err := Parse(tt.out, tt.as, tt.path, re)
		if err != nil || v.Kind != tt.kind || v.String() != tt.want {
			t.Errorf("Parse(%q, as=%q, value=%q, match=%q) = %s %q, %v; want %s %q", tt.out, tt.as, tt.path, tt.match, v.Kind, v.String(), err, tt.kind, tt.want)
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ out, as, path, match, want string }{
		{"abc", "number", "", "", "is not a number"},
		{"abc", "json", "", "", "as json"},
		{"abc", "", "a", "", "not JSON"},
		{`{"a":1}`, "", "b", "", `no "b"`},
		{`{"a":[1]}`, "", "a.3", "", `no item "3"`},
		{`{"a":1}`, "", "a.b", "", "not an object or list"},
		{"hello", "", "", `\d+`, "no match"},
	}
	for _, tt := range tests {
		var re *regexp.Regexp
		if tt.match != "" {
			re = regexp.MustCompile(tt.match)
		}
		if _, err := Parse(tt.out, tt.as, tt.path, re); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Parse(%q, as=%q, value=%q) error = %v, want it to contain %q", tt.out, tt.as, tt.path, err, tt.want)
		}
	}
}

func TestParseTable(t *testing.T) {
	tests := []struct {
		in   string
		cols []string
		rows [][]string
	}{
		{"ID  NAME   STATUS\n1   api    Up 3m\n2   db     Exited (0)\n", []string{"ID", "NAME", "STATUS"}, [][]string{{"1", "api", "Up 3m"}, {"2", "db", "Exited (0)"}}},
		{" id | name\n----+------\n  1 | a\n", []string{"id", "name"}, [][]string{{"1", "a"}}},
		{"| id | name |\n|----|------|\n| 1 | a |", []string{"id", "name"}, [][]string{{"1", "a"}}},
		{"a\tb\n\tx", []string{"a", "b"}, [][]string{{"", "x"}}},
	}
	for _, tt := range tests {
		got := parseTable(tt.in)
		if strings.Join(got.Cols, ",") != strings.Join(tt.cols, ",") || len(got.Rows) != len(tt.rows) {
			t.Errorf("parseTable(%q) = %v %v", tt.in, got.Cols, got.Rows)
			continue
		}
		for i := range tt.rows {
			if strings.Join(got.Rows[i], ",") != strings.Join(tt.rows[i], ",") {
				t.Errorf("parseTable(%q) row %d = %q, want %q", tt.in, i, got.Rows[i], tt.rows[i])
			}
		}
	}
	big := "n\n" + strings.Repeat("x\n", maxRows+5)
	if got := parseTable(big); len(got.Rows) != maxRows || !got.Truncated {
		t.Errorf("big table: %d rows, truncated=%v", len(got.Rows), got.Truncated)
	}
}

func TestRules(t *testing.T) {
	for _, bad := range []string{"10", "> abc", ">", "=~ x"} {
		if _, err := ParseRule(bad); err == nil {
			t.Errorf("ParseRule(%q) accepted", bad)
		}
	}
	lat := 12.0
	table := NewTable(TableData{Cols: []string{"a"}, Rows: [][]string{{"1"}, {"2"}}})
	tests := []struct {
		rule string
		v    Value
		want bool
	}{
		{"> 18", NewNumber(20), true},
		{"<= 18", NewNumber(20), false},
		{">= 20", NewNumber(20), true},
		{"!= 0", NewNumber(0), false},
		{"== paid", NewText("paid"), true},
		{"!= paid", NewText("pending"), true},
		{"> 3", NewText("5"), true},
		{"> 3", NewText("paid"), false},
		{"== 5", NewText("5.0"), true},
		{"> 1", table, true},
		{"> 200", NewProbe(ProbeData{Up: true}, &lat), false},
		{"< 200", NewProbe(ProbeData{Up: true}, &lat), true},
		{"== x", NewJSON("x"), false},
		{"== paid", NewNumber(5), false},
	}
	for _, tt := range tests {
		r, err := ParseRule(tt.rule)
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Match(tt.v); got != tt.want {
			t.Errorf("%q matches %s %v = %v, want %v", tt.rule, tt.v.Kind, tt.v.Data, got, tt.want)
		}
	}
}

func TestHealth(t *testing.T) {
	rule := func(s string) *Rule {
		r, err := ParseRule(s)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	warn, crit, expect := rule("> 18"), rule("> 30"), rule("> 0")
	tests := []struct {
		v    Value
		want string
	}{
		{NewNumber(12), OK},
		{NewNumber(20), Warn},
		{NewNumber(34), Crit},
		{NewNumber(0), Crit}, // expect not met
		{NewProbe(ProbeData{Reason: "connection refused"}, nil), Crit},
	}
	for _, tt := range tests {
		if got := Health(tt.v, warn, crit, expect); got != tt.want {
			t.Errorf("Health(%s) = %s, want %s", tt.v, got, tt.want)
		}
	}
	if got := Health(NewProbe(ProbeData{Up: true}, nil), nil, nil, nil); got != OK {
		t.Errorf("up probe without rules = %s", got)
	}
}

func TestSummary(t *testing.T) {
	lat := 0.4
	rows := func(rs ...string) Value {
		t := TableData{Cols: []string{"id"}, Rows: [][]string{}}
		for _, r := range rs {
			t.Rows = append(t.Rows, []string{r})
		}
		return NewTable(t)
	}
	tests := []struct {
		prev *Value
		cur  Value
		want string
	}{
		{nil, NewNumber(20), "20"},
		{ptr(NewNumber(17)), NewNumber(20), "17 → 20 (+3)"},
		{ptr(NewNumber(20)), NewNumber(18), "20 → 18 (−2)"},
		{ptr(NewNumber(0.1)), NewNumber(0.3), "0.1 → 0.3 (+0.2)"},
		{ptr(NewText("pending")), NewText("paid"), "pending → paid"},
		{ptr(NewText("a\nb\nc")), NewText("a\nB\nc\nd"), "+2 lines, −1 line"},
		{ptr(rows("1", "2", "3")), rows("2", "3", "4"), "+1 row, −1 row"},
		{ptr(rows()), rows("1", "2"), "+2 rows"},
		{ptr(rows("1")), rows(), "−1 row"},
		{ptr(NewJSON(map[string]any{"a": map[string]any{"x": false, "y": 1.0}, "r": "eu"})), NewJSON(map[string]any{"a": map[string]any{"x": true, "y": 1.0}, "r": "eu", "n": 2.0}), "a.x: false → true; n: null → 2"},
		{ptr(NewProbe(ProbeData{Reason: "connection refused"}, nil)), NewProbe(ProbeData{Up: true}, &lat), "DOWN: connection refused → UP"},
		{ptr(NewText("5")), NewNumber(5), "5"},
	}
	for _, tt := range tests {
		if got := Summary(tt.prev, tt.cur); got != tt.want {
			t.Errorf("Summary(%v, %v) = %q, want %q", tt.prev, tt.cur, got, tt.want)
		}
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	lat := 3.0
	for _, v := range []Value{
		NewNumber(1.5),
		NewText("paid"),
		NewJSON(map[string]any{"a": []any{1.0, "x"}}),
		NewTable(TableData{Cols: []string{"a", "b"}, Rows: [][]string{{"1", "2"}}, Truncated: true}),
		NewProbe(ProbeData{Up: true, Code: 200}, &lat),
	} {
		got, err := Decode(v.Kind, v.JSON())
		if err != nil || got.Kind != v.Kind || string(got.JSON()) != string(v.JSON()) {
			t.Errorf("Decode(%s, %s) = %s %s, %v", v.Kind, v.JSON(), got.Kind, got.JSON(), err)
		}
	}
	if _, err := Decode("nope", []byte("1")); err == nil {
		t.Error("unknown kind decoded")
	}
}

func ptr(v Value) *Value { return &v }
