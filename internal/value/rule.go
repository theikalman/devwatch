package value

import (
	"fmt"
	"strings"
)

// Rule is one threshold: an operator and an argument, such as "> 10" or "== paid".
type Rule struct {
	Op, Arg string
	num     float64
	isNum   bool
}

var ops = []string{">=", "<=", "==", "!=", ">", "<"} // two-character operators first

// ParseRule reads a threshold such as "> 10", "<= 0", "== paid" or "!= 0".
func ParseRule(s string) (*Rule, error) {
	s = strings.TrimSpace(s)
	for _, op := range ops {
		arg, ok := strings.CutPrefix(s, op)
		if !ok {
			continue
		}
		r := &Rule{Op: op, Arg: strings.TrimSpace(arg)}
		if r.Arg == "" {
			return nil, fmt.Errorf("%q: nothing after %s", s, op)
		}
		r.num, r.isNum = parseNum(r.Arg)
		if !r.isNum && op != "==" && op != "!=" {
			return nil, fmt.Errorf("%q: %s needs a number", s, op)
		}
		return r, nil
	}
	return nil, fmt.Errorf("%q: start with one of > >= < <= == !=", s)
}

func (r *Rule) String() string { return r.Op + " " + r.Arg }

// Match reports whether v meets r. A number, a table's row count and a probe's latency compare as numbers.
// Text compares as a number when both sides are numbers, else as a string (== and != only). JSON never matches.
func (r *Rule) Match(v Value) bool {
	var (
		n       float64
		numeric bool
		s       string
		text    bool
	)
	switch v.Kind {
	case Text:
		s, text = v.Data.(string), true
		n, numeric = parseNum(s)
	case Number, Table, Probe:
		if v.Num != nil {
			n, numeric = *v.Num, true
		}
	}
	if numeric && r.isNum {
		switch r.Op {
		case ">":
			return n > r.num
		case ">=":
			return n >= r.num
		case "<":
			return n < r.num
		case "<=":
			return n <= r.num
		case "==":
			return n == r.num
		case "!=":
			return n != r.num
		}
	}
	if text {
		switch r.Op {
		case "==":
			return s == r.Arg
		case "!=":
			return s != r.Arg
		}
	}
	return false
}

// Health decides a value's health and, when it isn't OK, why: a down probe or an unmet expect is crit,
// then crit, then warn. Any rule may be nil.
func Health(v Value, warn, crit, expect *Rule) (health, why string) {
	if p, ok := v.Data.(ProbeData); ok && !p.Up {
		return Crit, "it is down"
	}
	if expect != nil && !expect.Match(v) {
		return Crit, "expect " + expect.String() + " is not met"
	}
	if crit != nil && crit.Match(v) {
		return Crit, "it matches crit " + crit.String()
	}
	if warn != nil && warn.Match(v) {
		return Warn, "it matches warn " + warn.String()
	}
	return OK, ""
}
