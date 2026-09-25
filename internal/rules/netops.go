package rules

import (
	"strings"

	"dashboard/internal/enums"
	"dashboard/internal/sources"
)

const gibibyte = 1 << 30

func init() {
	registerDNSFilter(enums.ServicePihole)
	registerDNSFilter(enums.ServiceAdGuard)
	registerNextcloud()
	registerSabnzbd()
	registerGluetun()
	registerDomains()
	registerBlacklist()
}

// registerDNSFilter: Pi-hole and AdGuard share their rules.
func registerDNSFilter(service enums.ServiceType) {
	svc := string(service)

	// Blocking switched off "for five minutes" is easily forgotten.
	Register(svc+".disabled", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.DNSFilterDataset)
		if data.Enabled {
			return nil
		}
		return []Finding{svcFinding(svc, svc+".disabled", "disabled", "dnsfilter.disabled", enums.SeverityWarn, data.URL,
			map[string]any{"service": svc})}
	})

	if service != enums.ServicePihole {
		return
	}
	Register("pihole.lists_old", svc, map[string]any{"days": 14.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.DNSFilterDataset)
		if data.ListsUpdated.Unix() <= 0 {
			return nil
		}
		days := int(env.Today.Sub(data.ListsUpdated).Hours() / hoursPerDay)
		if float64(days) <= cfgFloat(cfg, "days") {
			return nil
		}
		return []Finding{svcFinding(svc, "pihole.lists_old", "gravity", "pihole.lists_old", enums.SeverityInfo, data.URL,
			map[string]any{"days": days})}
	})
}

func registerNextcloud() {
	svc := string(enums.ServiceNextcloud)

	Register("nextcloud.disk_low", svc, map[string]any{"warn_gb": 20.0, "critical_gb": 5.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.NextcloudDataset)
		if data.FreeBytes < 0 {
			return nil
		}
		gb := data.FreeBytes / gibibyte
		level := enums.SeverityWarn
		if gb <= cfgFloat(cfg, "critical_gb") {
			level = enums.SeverityCritical
		} else if gb > cfgFloat(cfg, "warn_gb") {
			return nil
		}
		return []Finding{svcFinding(svc, "nextcloud.disk_low", "disk", "nextcloud.disk_low", level, data.URL,
			map[string]any{"gb": int(gb + 0.5)})}
	})

	Register("nextcloud.app_updates", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.NextcloudDataset)
		if data.AppUpdates == 0 {
			return nil
		}
		return []Finding{svcFinding(svc, "nextcloud.app_updates", "apps", "nextcloud.app_updates", enums.SeverityInfo,
			strings.TrimRight(data.URL, "/")+"/settings/apps/updates", map[string]any{"count": data.AppUpdates})}
	})
}

func registerSabnzbd() {
	svc := string(enums.ServiceSabnzbd)

	Register("sabnzbd.failed", svc, map[string]any{"days": 7.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.SabnzbdDataset)
		var found []Finding
		for _, f := range data.Failures {
			if env.Today.Sub(f.At).Hours()/hoursPerDay > cfgFloat(cfg, "days") {
				continue
			}
			found = append(found, svcFinding(svc, "sabnzbd.failed", "failed:"+f.Name, "sabnzbd.failed", enums.SeverityWarn,
				data.URL, map[string]any{"name": f.Name, "reason": f.Reason}))
		}
		return found
	})

	// A full download disk pauses every job.
	Register("sabnzbd.disk_low", svc, map[string]any{"warn_gb": 25.0, "critical_gb": 5.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.SabnzbdDataset)
		level := enums.SeverityWarn
		if data.FreeGB <= cfgFloat(cfg, "critical_gb") {
			level = enums.SeverityCritical
		} else if data.FreeGB > cfgFloat(cfg, "warn_gb") {
			return nil
		}
		return []Finding{svcFinding(svc, "sabnzbd.disk_low", "disk", "sabnzbd.disk_low", level, data.URL,
			map[string]any{"gb": int(data.FreeGB + 0.5)})}
	})
}

func registerGluetun() {
	svc := string(enums.ServiceGluetun)

	Register("gluetun.vpn", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.GluetunDataset)
		var found []Finding
		if data.Status != "running" {
			found = append(found, svcFinding(svc, "gluetun.vpn", "down", "gluetun.down", enums.SeverityCritical, data.URL,
				map[string]any{"status": data.Status}))
		}

		// Same exit IP as the dashboard's own: traffic leaves unprotected.
		if data.ExitIP != "" && data.ExitIP == data.OwnIP {
			found = append(found, svcFinding(svc, "gluetun.vpn", "leak", "gluetun.leak", enums.SeverityCritical, data.URL,
				map[string]any{"ip": data.ExitIP}))
		}
		if data.ExpectedCountry != "" && data.Country != "" && !strings.EqualFold(data.ExpectedCountry, data.Country) {
			found = append(found, svcFinding(svc, "gluetun.vpn", "country", "gluetun.country", enums.SeverityWarn, data.URL,
				map[string]any{"country": data.Country, "expected": data.ExpectedCountry}))
		}
		return found
	})
}

func registerDomains() {
	svc := string(enums.ServiceDomains)

	Register("domains.expiring", svc, map[string]any{"info_days": 60.0, "warn_days": 30.0, "critical_days": 7.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data, _ := raw.(*sources.DomainsDataset)
			var found []Finding
			for _, d := range data.Domains {
				if d.Expires.IsZero() {
					continue // registry without a date, e.g. .de
				}
				daysLeft := int(d.Expires.Sub(env.Today).Hours() / hoursPerDay)
				level, ok := expiryLevel(daysLeft, cfg)
				if !ok {
					continue
				}
				f := svcFinding(svc, "domains.expiring", "domain:"+d.Name, "domains.expiring", level, "",
					map[string]any{"domain": d.Name, "day": Day(d.Expires), "days": daysLeft})
				// Name what breaks with the domain: resources, monitors, tiles.
				if chain, ok := chainOf(env, data, d.Name); ok && chain.Dependents() > 0 {
					f.Message = "domains.expiring_deps"
					f.Params["deps"] = chain.Dependents()
					f.Params["names"] = shortList(append(append(append([]string(nil), chain.Resources...), chain.Monitors...), chain.Tiles...))
				}
				f.Due = d.Expires.Format("2006-01-02")
				found = append(found, f)
			}
			return found
		})
}

func registerBlacklist() {
	svc := string(enums.ServiceBlacklist)

	Register("blacklist.listed", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.BlacklistDataset)
		var found []Finding
		for _, l := range data.Listings {
			found = append(found, svcFinding(svc, "blacklist.listed", "listed:"+l.IP+":"+l.Zone, "blacklist.listed",
				enums.SeverityCritical, "", map[string]any{"ip": l.IP, "zone": l.Zone}))
		}
		if len(data.Refused) > 0 {
			found = append(found, svcFinding(svc, "blacklist.listed", "refused", "blacklist.refused", enums.SeverityInfo, "",
				map[string]any{"zones": shortList(append([]string(nil), data.Refused...))}))
		}
		return found
	})
}
