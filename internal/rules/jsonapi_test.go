package rules_test

import (
	"testing"

	"dashboard/internal/enums"
	"dashboard/internal/sources"
)

func TestJSONAPIThreshold(t *testing.T) {
	data := &sources.JSONAPIDataset{Fields: []sources.JSONField{
		{Label: "ok", Path: "a", Value: 5, Numeric: true, Warn: 10},
		{Label: "warn", Path: "b", Value: 12, Numeric: true, Warn: 10, Critical: 50},
		{Label: "crit", Path: "c", Value: 60, Numeric: true, Warn: 10, Critical: 50},
		{Label: "text", Path: "d", Text: "x", Warn: 1},
	}}
	got := run(t, "jsonapi.threshold", data, todayEnv(nil))
	if len(got) != 2 || got[0].Severity != enums.SeverityWarn || got[1].Severity != enums.SeverityCritical {
		t.Fatalf("findings: %+v", got)
	}
}
