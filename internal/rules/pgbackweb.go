package rules

import (
	"time"

	"andon/internal/enums"
	"andon/internal/sources"
)

func init() {
	svc := string(enums.ServicePGBackWeb)
	data := func(raw any) *sources.PGBackDataset { d, _ := raw.(*sources.PGBackDataset); return d }

	Register("pgbackweb.failed", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		d := data(raw)
		var found []Finding
		for _, b := range d.Backups {
			if !b.Failing() {
				continue
			}
			found = append(found, svcFinding(svc, "pgbackweb.failed", "failed:"+b.Name, "pgbackweb.failed",
				enums.SeverityCritical, d.URL, map[string]any{"backup": b.Name, "day": Day(b.LastFailure)}))
		}
		return found
	})

	// Only backups that succeeded before: a backup without success events
	// may simply have no success webhook configured.
	Register("pgbackweb.stale", svc, map[string]any{"warn_hours": 26.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		d := data(raw)
		var found []Finding
		for _, b := range d.Backups {
			hours := time.Now().UTC().Sub(b.LastSuccess).Hours()
			if b.LastSuccess.IsZero() || b.Failing() || hours < cfgFloat(cfg, "warn_hours") {
				continue
			}
			found = append(found, svcFinding(svc, "pgbackweb.stale", "stale:"+b.Name, "pgbackweb.stale",
				enums.SeverityWarn, d.URL, map[string]any{"backup": b.Name, "hours": int(hours)}))
		}
		return found
	})

	Register("pgbackweb.unhealthy", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		d := data(raw)
		var found []Finding
		for _, h := range d.Unhealthy {
			found = append(found, svcFinding(svc, "pgbackweb.unhealthy", "health:"+h.Kind+":"+h.Name, "pgbackweb.unhealthy",
				enums.SeverityWarn, d.URL, map[string]any{"name": h.Name, "kind": h.Kind, "day": Day(h.Since)}))
		}
		return found
	})

	Register("pgbackweb.silent", svc, map[string]any{"days": 3.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		d := data(raw)
		if !d.LastEvent.IsZero() && time.Now().UTC().Sub(d.LastEvent).Hours()/hoursPerDay < cfgFloat(cfg, "days") {
			return nil
		}
		return []Finding{svcFinding(svc, "pgbackweb.silent", "silent", "pgbackweb.silent", enums.SeverityInfo, d.URL,
			map[string]any{"days": cfgInt(cfg, "days")})}
	})
}
