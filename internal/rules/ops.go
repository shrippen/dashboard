package rules

import (
	"fmt"
	"strconv"
	"strings"

	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

const hoursPerDay = 24

// expiryLevel grades days left against info/warn/critical thresholds;
// ok is false when the date is still far away.
func expiryLevel(daysLeft int, cfg map[string]any) (enums.Severity, bool) {
	switch {
	case daysLeft <= cfgInt(cfg, "critical_days"):
		return enums.SeverityCritical, true
	case daysLeft <= cfgInt(cfg, "warn_days"):
		return enums.SeverityWarn, true
	case daysLeft <= cfgInt(cfg, "info_days"):
		return enums.SeverityInfo, true
	}
	return 0, false
}

// idSet reads a list of ids that YAML may give as numbers or strings.
func idSet(v any) map[string]bool {
	out := map[string]bool{}
	list, _ := v.([]any)
	for _, item := range list {
		switch x := item.(type) {
		case string:
			out[strings.TrimSpace(x)] = true
		case float64:
			out[strconv.FormatFloat(x, 'f', -1, 64)] = true
		}
	}
	return out
}

var expiryDefaults = map[string]any{"info_days": 30.0, "warn_days": 14.0, "critical_days": 3.0}

func init() {
	kuma := string(enums.ServiceUptimeKuma)

	Register("kuma.monitor_down", kuma, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.KumaDataset)
		var found []Finding
		for _, m := range data.Monitors {
			if m.Status != sources.KumaDown {
				continue
			}
			found = append(found, Finding{
				Fingerprint: "down:" + m.Name, Rule: "kuma.monitor_down", Severity: enums.SeverityCritical,
				Message: "kuma.down", Params: map[string]any{"monitor": m.Name},
				ActionURL: data.URL, ActionLabel: "open_in_uptimekuma", Sources: []string{kuma},
			})
		}
		return found
	})

	Register("kuma.cert_expiring", kuma, expiryDefaults, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.KumaDataset)
		var found []Finding
		for _, m := range data.Monitors {
			level, ok := expiryLevel(m.CertDays, cfg)
			if m.CertDays < 0 || !ok {
				continue
			}
			found = append(found, Finding{
				Fingerprint: "cert:" + m.Name, Rule: "kuma.cert_expiring", Severity: level,
				Message: "kuma.cert", Params: map[string]any{"monitor": m.Name, "days": m.CertDays},
				ActionURL: data.URL, ActionLabel: "open_in_uptimekuma", Sources: []string{kuma},
			})
		}
		return found
	})

	registerProxmox()

	paperless := string(enums.ServicePaperless)
	Register("paperless.inbox", paperless, map[string]any{"min_count": 1.0, "warn_days": 14.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data, _ := raw.(*sources.PaperlessDataset)
			if data.Inbox < cfgInt(cfg, "min_count") || data.Inbox == 0 {
				return nil
			}
			level, days := enums.SeverityInfo, 0
			if added, ok := metrics.ParseDay(data.OldestAdded); ok {
				days = int(env.Today.Sub(added).Hours() / hoursPerDay)
			}
			if days >= cfgInt(cfg, "warn_days") {
				level = enums.SeverityWarn
			}
			return []Finding{{
				Fingerprint: "inbox", Rule: "paperless.inbox", Severity: level, Message: "paperless.inbox",
				Params:    map[string]any{"count": data.Inbox, "title": data.OldestTitle, "days": days},
				ActionURL: strings.TrimRight(data.URL, "/") + "/documents?sort=added", ActionLabel: "open_in_paperless",
				Sources: []string{paperless},
			}}
		})

	certs := string(enums.ServiceCerts)
	Register("certs.expiring", certs, expiryDefaults, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.CertDataset)
		var found []Finding
		for _, c := range data.Certs {
			if c.Error != "" {
				found = append(found, Finding{
					Fingerprint: "unreachable:" + c.Host, Rule: "certs.expiring", Severity: enums.SeverityWarn,
					Message: "certs.unreachable", Params: map[string]any{"host": c.Host, "error": c.Error},
					Sources: []string{certs},
				})
				continue
			}
			daysLeft := int(c.NotAfter.Sub(env.Today).Hours() / hoursPerDay)
			level, ok := expiryLevel(daysLeft, cfg)
			if !ok {
				continue
			}
			found = append(found, Finding{
				Fingerprint: "cert:" + c.Host, Rule: "certs.expiring", Severity: level, Message: "certs.expiring",
				Params: map[string]any{"host": c.Host, "day": Day(c.NotAfter), "days": daysLeft},
				Due:    c.NotAfter.Format("2006-01-02"), Sources: []string{certs},
			})
		}
		return found
	})
}

func registerProxmox() {
	pve := string(enums.ServiceProxmox)
	finding := func(data *sources.ProxmoxDataset, rule, fp, msg string, level enums.Severity, params map[string]any) Finding {
		return Finding{Fingerprint: fp, Rule: rule, Severity: level, Message: msg, Params: params,
			ActionURL: data.URL, ActionLabel: "open_in_proxmox", Sources: []string{pve}}
	}

	Register("proxmox.node_offline", pve, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.ProxmoxDataset)
		var found []Finding
		for _, n := range data.Nodes {
			if !n.Online {
				found = append(found, finding(data, "proxmox.node_offline", "offline:"+n.Name, "proxmox.offline",
					enums.SeverityCritical, map[string]any{"node": n.Name}))
			}
		}
		return found
	})

	Register("proxmox.updates", pve, map[string]any{"min_count": 1.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.ProxmoxDataset)
		var found []Finding
		for _, n := range data.Nodes {
			if n.Updates < cfgInt(cfg, "min_count") || n.Updates <= 0 {
				continue
			}
			found = append(found, finding(data, "proxmox.updates", "updates:"+n.Name, "proxmox.updates",
				enums.SeverityInfo, map[string]any{"node": n.Name, "count": n.Updates}))
		}
		return found
	})

	Register("proxmox.storage_full", pve, map[string]any{"warn": 0.8, "critical": 0.9},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data, _ := raw.(*sources.ProxmoxDataset)
			var found []Finding
			for _, n := range data.Nodes {
				for _, s := range n.Storages {
					share := s.Used / s.Total
					level := enums.SeverityWarn
					if share >= cfgFloat(cfg, "critical") {
						level = enums.SeverityCritical
					} else if share < cfgFloat(cfg, "warn") {
						continue
					}
					found = append(found, finding(data, "proxmox.storage_full", fmt.Sprintf("storage:%s:%s", n.Name, s.Name),
						"proxmox.storage", level, map[string]any{"node": n.Name, "storage": s.Name, "percent": int(share*100 + 0.5)}))
				}
			}
			return found
		})

	Register("proxmox.backup_old", pve, map[string]any{"days": 2.0, "ignore": []any{}},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data, _ := raw.(*sources.ProxmoxDataset)
			ignore := idSet(cfg["ignore"])

			var found []Finding
			for _, g := range data.Guests {
				vmid := strconv.FormatInt(g.VMID, 10)
				if g.Template || ignore[vmid] {
					continue
				}
				fp := "backup:" + vmid
				last, ok := data.Backups[g.VMID]
				if !ok {
					found = append(found, finding(data, "proxmox.backup_old", fp, "proxmox.backup_missing",
						enums.SeverityWarn, map[string]any{"guest": g.Name, "vmid": vmid}))
					continue
				}
				if env.Today.Sub(last).Hours()/hoursPerDay <= cfgFloat(cfg, "days") {
					continue
				}
				found = append(found, finding(data, "proxmox.backup_old", fp, "proxmox.backup_old",
					enums.SeverityWarn, map[string]any{"guest": g.Name, "vmid": vmid, "day": Day(last)}))
			}
			return found
		})
}
