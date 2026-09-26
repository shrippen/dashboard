package rules_test

import (
	"testing"

	"andon/internal/enums"
	"andon/internal/rules"
)

func TestTokenAgeAndExpiry(t *testing.T) {
	env := rules.Env{Today: day("2026-09-25"), Datasets: map[string]any{rules.ConnsDataset: []rules.Conn{
		{Name: "fresh", SecretAt: day("2026-06-01")},
		{Name: "old", SecretAt: day("2025-01-01")},
		{Name: "soon", SecretAt: day("2026-06-01"), Expires: "2026-10-10"},
		{Name: "gone", Expires: "2026-09-01"},
		{Name: "unknown"},
	}}}
	got := run(t, "system.token_age", nil, env)
	if len(got) != 3 {
		t.Fatalf("findings: %+v", got)
	}
	want := map[string]enums.Severity{"old": enums.SeverityInfo, "soon": enums.SeverityWarn, "gone": enums.SeverityCritical}
	for _, f := range got {
		if want[f.Params["name"].(string)] != f.Severity {
			t.Fatalf("%v: %v", f.Params["name"], f.Severity)
		}
	}
}
