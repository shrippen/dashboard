// Package summary writes the optional weekly summary in prose. Only
// aggregates leave the instance: open hints counted per rule, never titles,
// names, amounts or location data.
//
//	hints ──count per rule──► facts ("Überfällige Rechnungen: 3, Warnung")
//	      ──llm.Complete──► 3–5 sentences at the top of the weekly digest
package summary

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"andon/internal/drivers/llm"
	"andon/internal/enums"
	"andon/internal/i18n"
	"andon/internal/services/hints"
	"andon/internal/settings"
)

// locationRules are never summarised: rule ids starting with it describe
// where the user was.
const locationRules = "geo."

var apiKey string

// complete is the LLM call; tests swap it.
var complete = llm.Complete

// Init reads the API key; without one the summary stays off.
func Init(cfg settings.Settings) { apiKey = cfg.AnthropicAPIKey }

// Enabled reports whether the instance has an API key.
func Enabled() bool { return apiKey != "" }

// fact is one rule's open hints.
type fact struct {
	label string
	count int
	worst enums.Severity
}

// Facts renders the aggregates sent to the model, one line per rule,
// e.g. "- Überfällige Rechnungen: 3 (Warnung)".
func Facts(views []hints.View, deadlines int, locale enums.Locale) string {
	byRule := map[string]*fact{}
	for _, v := range views {
		if strings.HasPrefix(v.Rule, locationRules) {
			continue
		}
		f, ok := byRule[v.Rule]
		if !ok {
			f = &fact{label: i18n.T("rule_name."+v.Rule, locale, nil)}
			byRule[v.Rule] = f
		}
		f.count++
		f.worst = max(f.worst, v.Severity)
	}

	facts := make([]*fact, 0, len(byRule))
	for _, f := range byRule {
		facts = append(facts, f)
	}
	sort.Slice(facts, func(i, j int) bool {
		if facts[i].worst != facts[j].worst {
			return facts[i].worst > facts[j].worst
		}
		return facts[i].label < facts[j].label
	})

	var b strings.Builder
	for _, f := range facts {
		fmt.Fprintf(&b, "- %s: %d (%s)\n", f.label, f.count, i18n.T("severity."+f.worst.Key(), locale, nil))
	}
	fmt.Fprintf(&b, "- %s: %d\n", i18n.T("summary.deadlines", locale, nil), deadlines)
	return b.String()
}

// Weekly asks the model for a short summary of the facts in the reader's
// language.
func Weekly(ctx context.Context, views []hints.View, deadlines int, locale enums.Locale) (string, error) {
	if !Enabled() {
		return "", nil
	}
	return complete(ctx, apiKey, i18n.T("summary.system", locale, nil), Facts(views, deadlines, locale))
}
