package rules

// Custom rules from the space settings, no code needed:
//
//	settings.custom_rules: [{id: q1, title: "Warteschlange voll", service: sabnzbd,
//	                         path: Slots, op: ">", value: 10, severity: 20}]
//
// path walks the dataset's fields (Go names, dots, list indexes);
// a trailing "#" counts a list: "Failures#" > 0.

import (
	"encoding/json"
	"strconv"
	"strings"

	"dashboard/internal/enums"
)

const (
	CustomKey     = "custom_rules"
	customRule    = "custom.rules"
	countSuffix   = "#"
	pathSeparator = "."
)

// CustomRule is one threshold rule.
type CustomRule struct {
	ID, Title, Service, Path, Op string
	Value                        float64
	Severity                     enums.Severity
}

// CustomOps are the supported comparisons.
var CustomOps = []string{">", ">=", "<", "<=", "==", "!="}

// CustomRules reads the rules from space settings.
func CustomRules(settings map[string]any) []CustomRule {
	list, _ := settings[CustomKey].([]any)
	var out []CustomRule
	for _, raw := range list {
		m, _ := raw.(map[string]any)
		str := func(k string) string { s, _ := m[k].(string); return strings.TrimSpace(s) }
		num := func(k string) float64 { f, _ := m[k].(float64); return f }
		r := CustomRule{ID: str("id"), Title: str("title"), Service: str("service"), Path: str("path"), Op: str("op"),
			Value: num("value"), Severity: enums.Severity(num("severity"))}
		if r.ID == "" || r.Path == "" || r.Service == "" {
			continue
		}
		if r.Severity == 0 {
			r.Severity = enums.SeverityWarn
		}
		out = append(out, r)
	}
	return out
}

// Measure reads a number from a dataset by path; false if the path
// names nothing numeric.
func Measure(dataset any, path string) (float64, bool) {
	raw, err := json.Marshal(dataset)
	if err != nil {
		return 0, false
	}
	var tree any
	if json.Unmarshal(raw, &tree) != nil {
		return 0, false
	}

	counting := strings.HasSuffix(path, countSuffix)
	cur := tree
	for _, part := range strings.Split(strings.TrimSuffix(path, countSuffix), pathSeparator) {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return 0, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return 0, false
			}
			cur = node[i]
		default:
			return 0, false
		}
	}

	if counting {
		switch node := cur.(type) {
		case []any:
			return float64(len(node)), true
		case map[string]any:
			return float64(len(node)), true
		case nil:
			return 0, true
		}
		return 0, false
	}
	switch v := cur.(type) {
	case float64:
		return v, true
	case bool:
		if v {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

func compare(a float64, op string, b float64) bool {
	switch op {
	case ">":
		return a > b
	case ">=":
		return a >= b
	case "<":
		return a < b
	case "<=":
		return a <= b
	case "==":
		return a == b
	case "!=":
		return a != b
	}
	return false
}

func init() {
	Register(customRule, Cross, nil, func(_ any, cfg map[string]any, env Env) []Finding {
		var found []Finding
		for _, r := range CustomRules(env.Settings) {
			dataset, ok := env.Datasets[r.Service]
			if !ok {
				continue
			}
			value, ok := Measure(dataset, r.Path)
			if !ok || !compare(value, r.Op, r.Value) {
				continue
			}
			found = append(found, Finding{Fingerprint: "custom:" + r.ID, Rule: customRule, Severity: r.Severity, Message: "custom.rule",
				Params:  map[string]any{"title": r.Title, "path": r.Path, "value": Num(value, 2), "op": r.Op, "threshold": Num(r.Value, 2)},
				Sources: []string{r.Service}})
		}
		return found
	})
}
