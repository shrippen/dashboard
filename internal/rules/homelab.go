package rules

import (
	"fmt"
	"sort"
	"strings"

	"andon/internal/enums"
	"andon/internal/sources"
)

const percentScale = 100

// finding builds a Finding for a service with its "open_in_<service>" action.
func svcFinding(service, rule, fp, msg string, level enums.Severity, url string, params map[string]any) Finding {
	return Finding{Fingerprint: fp, Rule: rule, Severity: level, Message: msg, Params: params,
		ActionURL: url, ActionLabel: "open_in_" + service, Sources: []string{service}}
}

// newerVersion compares "v1.2.3" strings numerically.
func newerVersion(latest, current string) bool {
	parse := func(v string) [3]int {
		var out [3]int
		fmt.Sscanf(strings.TrimPrefix(v, "v"), "%d.%d.%d", &out[0], &out[1], &out[2])
		return out
	}
	a, b := parse(latest), parse(current)
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

func init() {
	registerScrutiny()
	registerImmich()
	registerUmami()
}

func registerScrutiny() {
	svc := string(enums.ServiceScrutiny)

	Register("scrutiny.disk_failed", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.ScrutinyDataset)
		var found []Finding
		for _, d := range data.Disks {
			if d.Status == sources.ScrutinyPassed {
				continue
			}
			found = append(found, svcFinding(svc, "scrutiny.disk_failed", "failed:"+d.Name, "scrutiny.failed",
				enums.SeverityCritical, data.URL, map[string]any{"disk": d.Name, "model": d.Model, "hours": d.Hours}))
		}
		return found
	})

	Register("scrutiny.disk_hot", svc, map[string]any{"warn": 50.0, "critical": 60.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.ScrutinyDataset)
		var found []Finding
		for _, d := range data.Disks {
			level := enums.SeverityWarn
			if d.Temp >= cfgFloat(cfg, "critical") {
				level = enums.SeverityCritical
			} else if d.Temp < cfgFloat(cfg, "warn") {
				continue
			}
			found = append(found, svcFinding(svc, "scrutiny.disk_hot", "hot:"+d.Name, "scrutiny.hot",
				level, data.URL, map[string]any{"disk": d.Name, "temp": int(d.Temp)}))
		}
		return found
	})

	// A collector that stopped reporting hides every other problem.
	Register("scrutiny.stale", svc, map[string]any{"days": 2.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.ScrutinyDataset)
		var found []Finding
		for _, d := range data.Disks {
			if d.Seen.IsZero() || env.Today.Sub(d.Seen).Hours()/hoursPerDay <= cfgFloat(cfg, "days") {
				continue
			}
			found = append(found, svcFinding(svc, "scrutiny.stale", "stale:"+d.Name, "scrutiny.stale",
				enums.SeverityWarn, data.URL, map[string]any{"disk": d.Name, "day": Day(d.Seen)}))
		}
		return found
	})
}

func registerImmich() {
	svc := string(enums.ServiceImmich)

	Register("immich.storage", svc, map[string]any{"warn": 0.85, "critical": 0.95}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.ImmichDataset)
		share := data.DiskPercent / percentScale
		level := enums.SeverityWarn
		if share >= cfgFloat(cfg, "critical") {
			level = enums.SeverityCritical
		} else if share < cfgFloat(cfg, "warn") {
			return nil
		}
		return []Finding{svcFinding(svc, "immich.storage", "storage", "immich.storage", level, data.URL,
			map[string]any{"percent": int(data.DiskPercent + 0.5), "free": data.DiskAvailable})}
	})

	Register("immich.jobs_failed", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.ImmichDataset)
		total := 0
		var queues []string
		for q, n := range data.FailedJobs {
			total += n
			queues = append(queues, q)
		}
		if total == 0 {
			return nil
		}
		sort.Strings(queues)
		return []Finding{svcFinding(svc, "immich.jobs_failed", "jobs", "immich.jobs", enums.SeverityWarn,
			strings.TrimRight(data.URL, "/")+"/admin/jobs-status", map[string]any{"count": total, "queues": strings.Join(queues, ", ")})}
	})

	Register("immich.update", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.ImmichDataset)
		if data.Latest == "" || data.Version == "" || !newerVersion(data.Latest, data.Version) {
			return nil
		}
		return []Finding{svcFinding(svc, "immich.update", "update:"+data.Latest, "immich.update", enums.SeverityInfo,
			data.URL, map[string]any{"version": data.Version, "latest": data.Latest})}
	})
}

func registerUmami() {
	svc := string(enums.ServiceUmami)

	// Visitors fell by more than "drop" against the week before; with no
	// views at all tracking is more likely broken than the site.
	Register("umami.traffic_drop", svc, map[string]any{"drop": 0.5, "min_visitors": 20.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data, _ := raw.(*sources.UmamiDataset)
			var found []Finding
			for _, s := range data.Sites {
				if float64(s.PrevVisit) < cfgFloat(cfg, "min_visitors") {
					continue
				}
				url := strings.TrimRight(data.URL, "/") + "/websites/" + s.ID
				if s.Views == 0 {
					found = append(found, svcFinding(svc, "umami.traffic_drop", "none:"+s.ID, "umami.no_data",
						enums.SeverityWarn, url, map[string]any{"site": s.Name, "prev": s.PrevVisit}))
					continue
				}
				change := 1 - float64(s.Visitors)/float64(s.PrevVisit)
				if change < cfgFloat(cfg, "drop") {
					continue
				}
				found = append(found, svcFinding(svc, "umami.traffic_drop", "drop:"+s.ID, "umami.drop",
					enums.SeverityWarn, url, map[string]any{"site": s.Name, "percent": int(change*percentScale + 0.5),
						"visitors": s.Visitors, "prev": s.PrevVisit}))
			}
			return found
		})
}
