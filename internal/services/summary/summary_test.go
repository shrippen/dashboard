package summary

import (
	"context"
	"strings"
	"testing"

	"andon/internal/enums"
	"andon/internal/services/hints"
	"andon/internal/settings"
)

func TestWeeklySendsOnlyCounts(t *testing.T) {
	views := []hints.View{
		{Rule: "in.invoice_overdue", Severity: enums.SeverityWarn, Title: "Rechnung R-17 an Muster GmbH überfällig"},
		{Rule: "in.invoice_overdue", Severity: enums.SeverityCritical, Title: "Rechnung R-18 an Beispiel AG überfällig"},
		{Rule: "geo.visit_without_time", Severity: enums.SeverityInfo, Title: "Besuch bei Muster GmbH, Hauptstraße 1"},
	}

	var gotSystem, gotPrompt string
	real := complete
	complete = func(_ context.Context, key, system, prompt string) (string, error) {
		gotSystem, gotPrompt = system, prompt
		return "Zwei Rechnungen sind überfällig.", nil
	}
	t.Cleanup(func() { complete = real; Init(settings.Settings{}) })

	if text, _ := Weekly(context.Background(), views, 1, enums.LocaleDE); text != "" {
		t.Fatalf("summary without API key: %q", text)
	}
	Init(settings.Settings{AnthropicAPIKey: "k"})

	text, err := Weekly(context.Background(), views, 1, enums.LocaleDE)
	if err != nil || text == "" || gotSystem == "" {
		t.Fatalf("text=%q err=%v", text, err)
	}
	if !strings.Contains(gotPrompt, ": 2 (") {
		t.Fatalf("count missing:\n%s", gotPrompt)
	}
	for _, leak := range []string{"Muster", "R-17", "Hauptstraße", "Besuch"} {
		if strings.Contains(gotPrompt, leak) {
			t.Fatalf("prompt leaks %q:\n%s", leak, gotPrompt)
		}
	}
}
