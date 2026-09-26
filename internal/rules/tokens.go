package rules

// Token hygiene: stored secrets that are old or about to expire.
//
//	secret stored 2024-08-01, max_age_days 365 → system.token_old (info)
//	expires 2026-10-10, today 2026-09-25       → system.token_expires (warn)
//	expired                                     → system.token_expires (critical)

import (
	"time"

	"andon/internal/enums"
)

// ConnsDataset is the Env.Datasets key of the space's connection secrets.
const ConnsDataset = "conns"

// Conn is one connection's secret as seen by a scope.
type Conn struct {
	Name     string
	SecretAt time.Time // zero = unknown
	Expires  string    // "2026-12-31", "" = unknown
}

const tokenRule = "system.token_age"

// tokenFinding checks one connection's secret.
func tokenFinding(c Conn, today time.Time, maxAge, warnDays int) (Finding, bool) {
	if exp, err := time.Parse(time.DateOnly, c.Expires); err == nil {
		left := int(exp.Sub(today).Hours() / 24)
		if left <= warnDays {
			level := enums.SeverityWarn
			if left < 0 {
				level = enums.SeverityCritical
			}
			return Finding{Fingerprint: "expires:" + c.Name, Rule: tokenRule, Severity: level,
				Message: "system.token_expires", Params: map[string]any{"name": c.Name, "day": DayStr(c.Expires)},
				Sources: []string{"system"}}, true
		}
	}
	if c.SecretAt.IsZero() {
		return Finding{}, false
	}
	age := int(today.Sub(c.SecretAt).Hours() / 24)
	if age <= maxAge {
		return Finding{}, false
	}
	return Finding{Fingerprint: "old:" + c.Name, Rule: tokenRule, Severity: enums.SeverityInfo,
		Message: "system.token_old", Params: map[string]any{"name": c.Name, "days": age, "since": Day(c.SecretAt)},
		Sources: []string{"system"}}, true
}

func init() {
	Register(tokenRule, Cross, map[string]any{"max_age_days": 365, "warn_days": 30}, func(_ any, cfg map[string]any, env Env) []Finding {
		conns, _ := env.Datasets[ConnsDataset].([]Conn)
		var found []Finding
		for _, c := range conns {
			if f, ok := tokenFinding(c, env.Today, cfgInt(cfg, "max_age_days"), cfgInt(cfg, "warn_days")); ok {
				found = append(found, f)
			}
		}
		return found
	})
}
