package rules

import (
	"strings"

	"dashboard/internal/enums"
	"dashboard/internal/sources"
)

// Alert levels as TrueNAS and Komodo report them.
var alertSeverity = map[string]enums.Severity{
	"WARNING":  enums.SeverityWarn,
	"ERROR":    enums.SeverityCritical,
	"CRITICAL": enums.SeverityCritical,
}

func init() {
	registerTrueNAS()
	registerKomodo()
	registerPangolin()
	registerAuthentik()
}

func registerTrueNAS() {
	svc := string(enums.ServiceTrueNAS)

	Register("truenas.pool_unhealthy", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.TrueNASDataset)
		var found []Finding
		for _, p := range data.Pools {
			if p.Healthy {
				continue
			}
			found = append(found, svcFinding(svc, "truenas.pool_unhealthy", "pool:"+p.Name, "truenas.pool_unhealthy",
				enums.SeverityCritical, data.URL, map[string]any{"pool": p.Name, "status": p.Status}))
		}
		return found
	})

	// ZFS slows down noticeably above 80 % fill.
	Register("truenas.pool_full", svc, map[string]any{"warn": 0.8, "critical": 0.9}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.TrueNASDataset)
		var found []Finding
		for _, p := range data.Pools {
			if p.Size <= 0 {
				continue
			}
			share := p.Allocated / p.Size
			level := enums.SeverityWarn
			if share >= cfgFloat(cfg, "critical") {
				level = enums.SeverityCritical
			} else if share < cfgFloat(cfg, "warn") {
				continue
			}
			found = append(found, svcFinding(svc, "truenas.pool_full", "full:"+p.Name, "truenas.pool_full",
				level, data.URL, map[string]any{"pool": p.Name, "percent": int(share*percentScale + 0.5)}))
		}
		return found
	})

	Register("truenas.alerts", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.TrueNASDataset)
		var found []Finding
		for _, a := range data.Alerts {
			level, ok := alertSeverity[strings.ToUpper(a.Level)]
			if !ok {
				continue
			}
			found = append(found, svcFinding(svc, "truenas.alerts", "alert:"+a.ID, "truenas.alert",
				level, data.URL, map[string]any{"text": a.Text}))
		}
		return found
	})

	Register("truenas.app_updates", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.TrueNASDataset)
		var names []string
		for _, a := range data.Apps {
			if a.Update {
				names = append(names, a.Name)
			}
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{svcFinding(svc, "truenas.app_updates", "apps", "truenas.app_updates", enums.SeverityInfo,
			strings.TrimRight(data.URL, "/")+"/ui/apps", map[string]any{"count": len(names), "apps": shortList(names)})}
	})
}

// Stack states Komodo reports for a stack that should run but does not.
var komodoBroken = map[string]bool{"down": true, "unhealthy": true, "dead": true, "restarting": true}

func registerKomodo() {
	svc := string(enums.ServiceKomodo)

	Register("komodo.alerts", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.KomodoDataset)
		var found []Finding
		for _, a := range data.Alerts {
			level, ok := alertSeverity[a.Level]
			if !ok {
				continue
			}
			found = append(found, svcFinding(svc, "komodo.alerts", "alert:"+a.Kind+":"+a.Name, "komodo.alert",
				level, data.URL, map[string]any{"kind": a.Kind, "name": a.Name}))
		}
		return found
	})

	Register("komodo.stack_down", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.KomodoDataset)
		var found []Finding
		for _, s := range data.Stacks {
			if !komodoBroken[s.State] {
				continue
			}
			found = append(found, svcFinding(svc, "komodo.stack_down", "stack:"+s.Name, "komodo.stack_down",
				enums.SeverityWarn, strings.TrimRight(data.URL, "/")+"/stacks", map[string]any{"stack": s.Name, "state": s.State}))
		}
		return found
	})

	Register("komodo.updates", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.KomodoDataset)
		var names []string
		for _, s := range data.Stacks {
			if len(s.Updates) > 0 {
				names = append(names, s.Name)
			}
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{svcFinding(svc, "komodo.updates", "updates", "komodo.updates", enums.SeverityInfo,
			strings.TrimRight(data.URL, "/")+"/stacks", map[string]any{"count": len(names), "stacks": shortList(names)})}
	})
}

func registerPangolin() {
	svc := string(enums.ServicePangolin)

	Register("pangolin.site_offline", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.PangolinDataset)
		var found []Finding
		for _, s := range data.Sites {
			if s.Online == nil || *s.Online {
				continue
			}
			found = append(found, svcFinding(svc, "pangolin.site_offline", "site:"+s.Name, "pangolin.site_offline",
				enums.SeverityCritical, data.URL, map[string]any{"site": s.Name}))
		}
		return found
	})

	Register("pangolin.unhealthy", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.PangolinDataset)
		var found []Finding
		for _, r := range data.Resources {
			if !r.Enabled || r.Health != "unhealthy" {
				continue
			}
			found = append(found, svcFinding(svc, "pangolin.unhealthy", "res:"+r.Domain, "pangolin.unhealthy",
				enums.SeverityWarn, "https://"+r.Domain, map[string]any{"name": r.Name, "domain": r.Domain}))
		}
		return found
	})

	Register("pangolin.newt_update", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.PangolinDataset)
		var names []string
		for _, s := range data.Sites {
			if s.Update {
				names = append(names, s.Name)
			}
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{svcFinding(svc, "pangolin.newt_update", "newt", "pangolin.newt_update", enums.SeverityInfo,
			data.URL, map[string]any{"sites": shortList(names)})}
	})
}

func registerAuthentik() {
	svc := string(enums.ServiceAuthentik)
	adminURL := func(data *sources.AuthentikDataset, path string) string {
		return strings.TrimRight(data.URL, "/") + "/if/admin/#/" + path
	}

	Register("authentik.update", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.AuthentikDataset)
		var found []Finding
		if data.Outdated {
			found = append(found, svcFinding(svc, "authentik.update", "update:"+data.Latest, "authentik.update",
				enums.SeverityInfo, adminURL(data, "administration/overview"), map[string]any{"version": data.Latest, "current": data.Version}))
		}
		if data.Outposts {
			found = append(found, svcFinding(svc, "authentik.update", "outposts", "authentik.outposts",
				enums.SeverityWarn, adminURL(data, "outpost/outposts"), nil))
		}
		return found
	})

	// Many failed logins within a day hint at guessing or a broken client.
	Register("authentik.failed_logins", svc, map[string]any{"warn": 20.0, "critical": 100.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.AuthentikDataset)
		n := float64(data.Failed24h)
		level := enums.SeverityWarn
		if n >= cfgFloat(cfg, "critical") {
			level = enums.SeverityCritical
		} else if n < cfgFloat(cfg, "warn") {
			return nil
		}
		return []Finding{svcFinding(svc, "authentik.failed_logins", "failed", "authentik.failed_logins", level,
			adminURL(data, "events/log"), map[string]any{"count": data.Failed24h})}
	})

	// Accounts nobody uses are attack surface.
	Register("authentik.stale_users", svc, map[string]any{"days": 180.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.AuthentikDataset)
		var names []string
		for _, u := range data.Users {
			if !u.LastLogin.IsZero() && env.Today.Sub(u.LastLogin).Hours()/hoursPerDay <= cfgFloat(cfg, "days") {
				continue
			}
			names = append(names, u.Name)
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{svcFinding(svc, "authentik.stale_users", "stale", "authentik.stale_users", enums.SeverityInfo,
			adminURL(data, "identity/users"), map[string]any{"count": len(names), "users": shortList(names), "days": cfgInt(cfg, "days")})}
	})
}
