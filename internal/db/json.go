package db

import (
	"encoding/json"
	"time"
)

// TimeStr formats t for storage in a TEXT timestamp column.
func TimeStr(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// ParseTime parses a TEXT timestamp column back into UTC time.
func ParseTime(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

// NullTimeStr formats *t, or returns nil for a NULL column.
func NullTimeStr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return TimeStr(*t)
}

// ScanTime parses a nullable TEXT timestamp scanned into ns (via sql.NullString).
func ScanTime(ns *string) (*time.Time, error) {
	if ns == nil {
		return nil, nil
	}
	t, err := ParseTime(*ns)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ToJSON marshals v for storage in a TEXT column. A nil map/slice becomes
// "{}" or "[]" via the zero-value default passed by the caller.
func ToJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// FromJSON unmarshals a TEXT column into dst. An empty string leaves dst
// untouched (its zero value stands).
func FromJSON(text string, dst any) error {
	if text == "" {
		return nil
	}
	return json.Unmarshal([]byte(text), dst)
}
