package rules

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/sources"
)

func init() {
	registerFreshRSS()
	registerGitea()
	registerBorg()
}

func registerFreshRSS() {
	svc := string(enums.ServiceFreshRSS)

	// A reading backlog that only grows: name the feeds causing it, so
	// the fix (unsubscribe, filter, mark read) is obvious.
	Register("freshrss.backlog", svc, map[string]any{"min_count": 500.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.FreshRSSDataset)
		if data.Unread < cfgInt(cfg, "min_count") {
			return nil
		}
		feeds := append([]sources.Feed(nil), data.Feeds...)
		sort.Slice(feeds, func(i, j int) bool { return feeds[i].Unread > feeds[j].Unread })
		var top []string
		for _, f := range feeds[:min(3, len(feeds))] {
			if f.Unread > 0 {
				top = append(top, fmt.Sprintf("%s (%d)", f.Title, f.Unread))
			}
		}
		return []Finding{svcFinding(svc, "freshrss.backlog", "backlog", "freshrss.backlog", enums.SeverityInfo, data.URL,
			map[string]any{"count": data.Unread, "feeds": strings.Join(top, ", ")})}
	})

	Register("freshrss.stale_feed", svc, map[string]any{"days": 180.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.FreshRSSDataset)
		var found []Finding
		for _, f := range data.Feeds {
			if f.Newest.IsZero() || env.Today.Sub(f.Newest).Hours()/hoursPerDay <= cfgFloat(cfg, "days") {
				continue
			}
			found = append(found, svcFinding(svc, "freshrss.stale_feed", "stale:"+f.ID, "freshrss.stale", enums.SeverityInfo,
				data.URL, map[string]any{"feed": f.Title, "day": Day(f.Newest)}))
		}
		return found
	})
}

func registerGitea() {
	svc := string(enums.ServiceGitea)
	issueFinding := func(rule, msg string, level enums.Severity, i sources.Issue, params map[string]any) Finding {
		p := map[string]any{"repo": i.Repo, "number": i.Number, "title": i.Title}
		for k, v := range params {
			p[k] = v
		}
		f := svcFinding(svc, rule, fmt.Sprintf("%s#%d", i.Repo, i.Number), msg, level, i.URL, p)
		return f
	}

	Register("gitea.review_waiting", svc, map[string]any{"days": 2.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.GiteaDataset)
		var found []Finding
		for _, pr := range data.Reviews {
			days := int(env.Today.Sub(pr.Updated).Hours() / hoursPerDay)
			if days < cfgInt(cfg, "days") {
				continue
			}
			found = append(found, issueFinding("gitea.review_waiting", "gitea.review", enums.SeverityWarn, pr, map[string]any{"days": days}))
		}
		return found
	})

	Register("gitea.due", svc, map[string]any{"warn_days": 3.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.GiteaDataset)
		var found []Finding
		for _, i := range data.Assigned {
			if i.Due.IsZero() {
				continue
			}
			left := int(i.Due.Sub(env.Today).Hours() / hoursPerDay)
			level := enums.SeverityCritical
			if left > cfgInt(cfg, "warn_days") {
				continue
			} else if left >= 0 {
				level = enums.SeverityWarn
			}
			f := issueFinding("gitea.due", "gitea.due", level, i, map[string]any{"day": Day(i.Due)})
			f.Due = i.Due.Format("2006-01-02")
			found = append(found, f)
		}
		return found
	})

	Register("gitea.stale_pr", svc, map[string]any{"days": 14.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.GiteaDataset)
		var found []Finding
		for _, i := range data.Assigned {
			days := int(env.Today.Sub(i.Updated).Hours() / hoursPerDay)
			if !i.Pull || days < cfgInt(cfg, "days") {
				continue
			}
			found = append(found, issueFinding("gitea.stale_pr", "gitea.stale_pr", enums.SeverityInfo, i, map[string]any{"days": days}))
		}
		return found
	})

	Register("gitea.actions_failed", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.GiteaDataset)
		var found []Finding
		for _, r := range data.Repos {
			if r.FailedWorkflow == "" {
				continue
			}
			found = append(found, svcFinding(svc, "gitea.actions_failed", "actions:"+r.Name, "gitea.actions", enums.SeverityWarn,
				strings.TrimRight(r.URL, "/")+"/actions", map[string]any{"repo": r.Name, "run": r.FailedWorkflow}))
		}
		return found
	})

	Register("gitea.mirror_stale", svc, map[string]any{"days": 7.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.GiteaDataset)
		var found []Finding
		for _, r := range data.Repos {
			if !r.Mirror || r.MirrorUpdated.IsZero() || env.Today.Sub(r.MirrorUpdated).Hours()/hoursPerDay <= cfgFloat(cfg, "days") {
				continue
			}
			found = append(found, svcFinding(svc, "gitea.mirror_stale", "mirror:"+r.Name, "gitea.mirror", enums.SeverityWarn,
				strings.TrimRight(r.URL, "/")+"/settings", map[string]any{"repo": r.Name, "day": Day(r.MirrorUpdated)}))
		}
		return found
	})
}

func registerBorg() {
	svc := string(enums.ServiceBorgBackup)

	Register("borg.client_offline", svc, map[string]any{"days": 2.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.BorgDataset)
		var found []Finding
		for _, c := range data.Clients {
			switch {
			case c.Status == "error":
				found = append(found, svcFinding(svc, "borg.client_offline", "client:"+c.Name, "borg.client_error",
					enums.SeverityCritical, data.URL, map[string]any{"client": c.Name}))
			case c.Status == "offline" && !c.LastSeen.IsZero() && env.Today.Sub(c.LastSeen).Hours()/hoursPerDay > cfgFloat(cfg, "days"):
				found = append(found, svcFinding(svc, "borg.client_offline", "client:"+c.Name, "borg.client_offline",
					enums.SeverityWarn, data.URL, map[string]any{"client": c.Name, "day": Day(c.LastSeen)}))
			}
		}
		return found
	})

	Register("borg.jobs_failed", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.BorgDataset)
		if data.Failed24h == 0 {
			return nil
		}
		return []Finding{svcFinding(svc, "borg.jobs_failed", "failed", "borg.failed", enums.SeverityWarn,
			strings.TrimRight(data.URL, "/")+"/queue", map[string]any{"count": data.Failed24h, "ok": data.Completed24h})}
	})

	Register("borg.backup_old", svc, map[string]any{"warn_hours": 26.0, "critical_hours": 72.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.BorgDataset)
		if data.LastBackup.IsZero() {
			return nil
		}
		hours := time.Now().UTC().Sub(data.LastBackup).Hours()
		level := enums.SeverityWarn
		if hours >= cfgFloat(cfg, "critical_hours") {
			level = enums.SeverityCritical
		} else if hours < cfgFloat(cfg, "warn_hours") {
			return nil
		}
		return []Finding{svcFinding(svc, "borg.backup_old", "last", "borg.old", level, data.URL,
			map[string]any{"hours": int(hours)})}
	})

	Register("borg.storage", svc, map[string]any{"warn": 0.85, "critical": 0.95}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.BorgDataset)
		if data.TotalBytes == 0 {
			return nil
		}
		share := data.UsedBytes / data.TotalBytes
		level := enums.SeverityWarn
		if share >= cfgFloat(cfg, "critical") {
			level = enums.SeverityCritical
		} else if share < cfgFloat(cfg, "warn") {
			return nil
		}
		return []Finding{svcFinding(svc, "borg.storage", "storage", "borg.storage", level, data.URL,
			map[string]any{"percent": int(share*percentScale + 0.5)})}
	})

	Register("borg.update", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.BorgDataset)
		if !data.ServerUpdate && data.AgentsOutdated == 0 {
			return nil
		}
		return []Finding{svcFinding(svc, "borg.update", "update", "borg.update", enums.SeverityInfo, data.URL,
			map[string]any{"agents": data.AgentsOutdated})}
	})
}
