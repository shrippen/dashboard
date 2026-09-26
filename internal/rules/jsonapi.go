package rules

// Own integrations (JSON API with YAML options): fields with thresholds
// become hints.
//
//	fields: [{label: Warteschlange, path: stats.queue, warn: 10, critical: 50}]
//	stats.queue = 12 → jsonapi.threshold (warn)

import (
	"andon/internal/enums"
	"andon/internal/sources"
)

const thresholdRule = "jsonapi.threshold"

// fieldLevel grades a value against its thresholds (0 = not set).
func fieldLevel(f sources.JSONField) (enums.Severity, float64, bool) {
	if !f.Numeric {
		return 0, 0, false
	}
	if f.Critical > 0 && f.Value >= f.Critical {
		return enums.SeverityCritical, f.Critical, true
	}
	if f.Warn > 0 && f.Value >= f.Warn {
		return enums.SeverityWarn, f.Warn, true
	}
	return 0, 0, false
}

func init() {
	svc := string(enums.ServiceJSONAPI)
	Register(thresholdRule, svc, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, ok := raw.(*sources.JSONAPIDataset)
		if !ok {
			return nil
		}
		var found []Finding
		for _, f := range data.Fields {
			level, limit, over := fieldLevel(f)
			if !over {
				continue
			}
			found = append(found, Finding{Fingerprint: "threshold:" + f.Path, Rule: thresholdRule, Severity: level,
				Message: thresholdRule, Params: map[string]any{"label": f.Label, "value": Num(f.Value, 2), "limit": Num(limit, 2)},
				Sources: []string{svc}})
		}
		return found
	})
}
