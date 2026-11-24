package value

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const maxRows = 200 // rows kept per table value

// Parse turns command or HTTP output into a value. Each step is optional and runs in order: path picks a field
// from JSON output ("data.items.0.status"), match keeps a regex's first group (or the whole match), then as
// ("number", "text", "json", "table") sets the kind. An empty as infers a number, then JSON, then text.
func Parse(out, as, path string, match *regexp.Regexp) (Value, error) {
	if path != "" {
		var doc any
		if err := json.Unmarshal([]byte(out), &doc); err != nil {
			return Value{}, fmt.Errorf("value %s: output is not JSON", path)
		}
		got, err := lookup(doc, path)
		if err != nil {
			return Value{}, err
		}
		switch g := got.(type) {
		case float64:
			if as == "" || as == "number" {
				return NewNumber(g), nil
			}
			out = formatNum(g)
		case string:
			out = g
		case map[string]any, []any:
			if as == "" || as == "json" {
				return NewJSON(g), nil
			}
			b, _ := json.Marshal(g)
			out = string(b)
		case nil:
			out = "null"
		default: // bool
			out = fmt.Sprint(g)
		}
	}
	if match != nil {
		m := match.FindStringSubmatch(out)
		if m == nil {
			return Value{}, fmt.Errorf("match %s: no match in output", match)
		}
		out = m[min(1, len(m)-1)]
	}
	s := strings.TrimSpace(out)
	switch as {
	case "number":
		if f, ok := parseNum(s); ok {
			return NewNumber(f), nil
		}
		return Value{}, fmt.Errorf("as number: %q is not a number", oneLine(s))
	case "text":
		return NewText(s), nil
	case "json":
		var d any
		if err := json.Unmarshal([]byte(s), &d); err != nil {
			return Value{}, fmt.Errorf("as json: %v", err)
		}
		return NewJSON(d), nil
	case "table":
		return NewTable(parseTable(s)), nil
	}
	if f, ok := parseNum(s); ok {
		return NewNumber(f), nil
	}
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		var d any
		if json.Unmarshal([]byte(s), &d) == nil {
			return NewJSON(d), nil
		}
	}
	return NewText(s), nil
}

// lookup follows a dotted path through decoded JSON: object keys and list indexes.
func lookup(doc any, path string) (any, error) {
	cur := doc
	for _, part := range strings.Split(path, ".") {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[part]
			if !ok {
				return nil, fmt.Errorf("value %s: no %q in output", path, part)
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(c) {
				return nil, fmt.Errorf("value %s: no item %q in a list of %d", path, part, len(c))
			}
			cur = c[i]
		default:
			return nil, fmt.Errorf("value %s: %q is inside a value that is not an object or list", path, part)
		}
	}
	return cur, nil
}

var wideSpace = regexp.MustCompile(`\s{2,}`)

// parseTable reads aligned text with the header on the first line. Columns split on tabs, on "|", or on runs
// of 2+ spaces, whichever the header uses. Separator lines such as "----+----" are skipped.
func parseTable(s string) TableData {
	t := TableData{Cols: []string{}, Rows: [][]string{}}
	var split func(string) []string
	for _, line := range strings.Split(s, "\n") {
		if strings.Trim(line, "-+|: \t\r") == "" {
			continue
		}
		if split == nil {
			split = splitter(line)
			t.Cols = split(line)
			continue
		}
		if len(t.Rows) == maxRows {
			t.Truncated = true
			break
		}
		t.Rows = append(t.Rows, split(line))
	}
	return t
}

func splitter(header string) func(string) []string {
	var sep func(string) []string
	switch {
	case strings.Contains(header, "\t"):
		sep = func(l string) []string { return strings.Split(l, "\t") }
	case strings.Contains(header, "|"):
		sep = func(l string) []string { return strings.Split(strings.Trim(strings.TrimSpace(l), "|"), "|") }
	default:
		sep = func(l string) []string { return wideSpace.Split(strings.TrimSpace(l), -1) }
	}
	return func(l string) []string {
		cells := sep(l)
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		return cells
	}
}
