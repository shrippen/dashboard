package sources

import (
	"strconv"
	"strings"
)

// Small, forgiving readers for the loosely-typed JSON these APIs return
// (a field that is usually a number might arrive as a numeric string, a
// nested object might be a bare id, etc.).

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func asStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	default:
		return ""
	}
}

func asFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(strings.ReplaceAll(t, ",", ""), 64)
		return f
	default:
		return 0
	}
}

func asInt64(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	default:
		return 0
	}
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

// refID reads an id from either a nested object ({"id": 5, ...}) or a bare
// scalar (5), matching Kimai's inconsistent embedding.
func refID(v any) int64 {
	if m, ok := v.(map[string]any); ok {
		return asInt64(m["id"])
	}
	return asInt64(v)
}

// day trims a timestamp down to its date part (YYYY-MM-DD), reading either
// a string or a {"date": ...} / {"datetime": ...} object.
func day(v any) string {
	if m, ok := v.(map[string]any); ok {
		if d := asStr(m["date"]); d != "" {
			v = d
		} else {
			v = m["datetime"]
		}
	}
	s := asStr(v)
	if len(s) > 10 {
		return s[:10]
	}
	return s
}
