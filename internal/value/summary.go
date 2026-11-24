package value

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
)

// Summary describes the change from prev to cur in a few words for the changes feed: "17 → 20 (+3)",
// "pending → paid", "+2 rows, −1 row", "checkout.new_flow: false → true". prev is nil for a first value.
func Summary(prev *Value, cur Value) string {
	if prev == nil || prev.Kind != cur.Kind {
		return cur.String()
	}
	switch cur.Kind {
	case Number:
		a, b := prev.Data.(float64), cur.Data.(float64)
		return fmt.Sprintf("%s → %s (%s)", formatNum(a), formatNum(b), signed(b-a))
	case Text:
		a, b := prev.Data.(string), cur.Data.(string)
		if strings.Contains(a, "\n") || strings.Contains(b, "\n") {
			return countDiff(a, b, "line")
		}
		return oneLine(a) + " → " + oneLine(b)
	case Table:
		a, b := prev.Data.(TableData), cur.Data.(TableData)
		switch {
		case !slices.Equal(a.Cols, b.Cols):
			return "columns changed"
		case len(a.Rows) == 0:
			return "+" + plural(len(b.Rows), "row")
		case len(b.Rows) == 0:
			return "−" + plural(len(a.Rows), "row")
		}
		return countDiff(rowsText(a), rowsText(b), "row")
	case Probe:
		return probeState(prev.Data.(ProbeData)) + " → " + probeState(cur.Data.(ProbeData))
	}
	return jsonChanges(prev.Data, cur.Data)
}

// signed formats a delta with its sign and a real minus, rounded to hide float noise like 0.30000000000000004.
func signed(d float64) string {
	d = math.Round(d*1e9) / 1e9
	if d < 0 {
		return "−" + formatNum(-d)
	}
	return "+" + formatNum(d)
}

func rowsText(t TableData) string {
	lines := make([]string, len(t.Rows))
	for i, r := range t.Rows {
		lines[i] = strings.Join(r, "\t")
	}
	return strings.Join(lines, "\n")
}

// countDiff counts added and removed lines: "+2 rows, −1 row".
func countDiff(a, b, unit string) string {
	// shortcut: lineDiff is O(n·m); past a million line pairs just say it changed.
	if (strings.Count(a, "\n")+1)*(strings.Count(b, "\n")+1) > 1_000_000 {
		return unit + "s changed"
	}
	var add, del int
	for _, l := range lineDiff(a, b) {
		if strings.HasPrefix(l, "+ ") {
			add++
		} else {
			del++
		}
	}
	var parts []string
	if add > 0 {
		parts = append(parts, "+"+plural(add, unit))
	}
	if del > 0 {
		parts = append(parts, "−"+plural(del, unit))
	}
	if len(parts) == 0 {
		return unit + "s changed"
	}
	return strings.Join(parts, ", ")
}

// jsonChanges lists up to three changed paths: "checkout.new_flow: false → true (+2 more)".
func jsonChanges(a, b any) string {
	var out []string
	diffJSON(a, b, "", &out)
	if len(out) == 0 {
		return "changed"
	}
	s := strings.Join(out[:min(3, len(out))], "; ")
	if len(out) > 3 {
		s += fmt.Sprintf(" (+%d more)", len(out)-3)
	}
	return s
}

func diffJSON(a, b any, path string, out *[]string) {
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := make([]string, 0, len(am)+len(bm))
		for k := range am {
			keys = append(keys, k)
		}
		for k := range bm {
			if _, ok := am[k]; !ok {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		for _, k := range keys {
			p := k
			if path != "" {
				p = path + "." + k
			}
			diffJSON(am[k], bm[k], p, out)
		}
		return
	}
	if reflect.DeepEqual(a, b) {
		return
	}
	change := short(a) + " → " + short(b)
	if path != "" {
		change = path + ": " + change
	}
	*out = append(*out, change)
}

func short(v any) string {
	b, _ := json.Marshal(v)
	if r := []rune(string(b)); len(r) > 40 {
		return string(r[:39]) + "…"
	}
	return string(b)
}
