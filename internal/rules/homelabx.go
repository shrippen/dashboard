package rules

// Homelab rules across services and over time (phase 13):
//
//	system.storage_forecast      a storage fills up within weeks (history)
//	backups.unsaved              new photos, documents, files since the last backup
//	system.slower_since_update   a monitor answers slower since its service's update
//	pangolin.exposure            public resource without login and with open updates or cert trouble
//	authentik.login_anomaly      login from a new country or far from where you are
//	dns.device_spike             a device queries far more than usual
//	dns.new_device               an unnamed busy device never seen before
//	speedtest.contract           many days below the booked speed (§ 57 TKG)
//	dwd.storm_prep               storm ahead while the last backup is old
//	gluetun.downloads_exposed    downloads running while the VPN is down or leaking

import (
	"strconv"
	"strings"
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	"andon/internal/sources"
)

const system = "system"

// historyOf returns the scope's recorded history; nil-safe.
func historyOf(env Env) *metrics.History {
	h, _ := env.Datasets[metrics.HistoryDataset].(*metrics.History)
	if h == nil {
		return &metrics.History{}
	}
	return h
}

// tileHosts maps link tile titles to their hosts.
func tileHosts(env Env) map[string]string {
	out := map[string]string{}
	if links, ok := boardLinks(env); ok {
		for _, l := range links {
			out[l.Title] = hostOf(l.URL)
		}
	}
	return out
}

// chainOf builds one domain's dependency chain from the scope.
func chainOf(env Env, domains *sources.DomainsDataset, name string) (metrics.DomainChain, bool) {
	certs, _ := env.Datasets[string(enums.ServiceCerts)].(*sources.CertDataset)
	pangolin, _ := env.Datasets[string(enums.ServicePangolin)].(*sources.PangolinDataset)
	kuma, _ := env.Datasets[string(enums.ServiceUptimeKuma)].(*sources.KumaDataset)
	for _, c := range metrics.DomainChains(domains, certs, pangolin, kuma, tileHosts(env), env.Today) {
		if c.Domain == name {
			return c, true
		}
	}
	return metrics.DomainChain{}, false
}

// dnsFilters returns the scope's Pi-hole and AdGuard datasets.
func dnsFilters(env Env) []*sources.DNSFilterDataset {
	var out []*sources.DNSFilterDataset
	for _, svc := range []enums.ServiceType{enums.ServicePihole, enums.ServiceAdGuard} {
		if d, ok := env.Datasets[string(svc)].(*sources.DNSFilterDataset); ok {
			out = append(out, d)
		}
	}
	return out
}

func init() {
	Register("system.storage_forecast", Cross, map[string]any{"warn_days": 60.0, "critical_days": 14.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		var found []Finding
		for _, f := range metrics.StorageForecasts(historyOf(env), env.Today) {
			if f.FullIn < 0 || f.FullIn > cfgInt(cfg, "warn_days") {
				continue
			}
			level := enums.SeverityWarn
			if f.FullIn <= cfgInt(cfg, "critical_days") {
				level = enums.SeverityCritical
			}
			found = append(found, Finding{Fingerprint: "full:" + f.Key, Rule: "system.storage_forecast", Severity: level,
				Message: "system.storage_forecast", Params: map[string]any{"name": f.Label, "percent": Num(f.Used*100, 0),
					"days": f.FullIn, "day": Day(env.Today.AddDate(0, 0, f.FullIn))},
				Due: env.Today.AddDate(0, 0, f.FullIn).Format(time.DateOnly), Sources: []string{system}})
		}
		return found
	})

	Register("backups.unsaved", Cross, map[string]any{"hours": 36.0, "warn_days": 7.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		last, tool := metrics.LastBackup(env.Datasets)
		if last.IsZero() || env.Today.Sub(last).Hours() < cfgFloat(cfg, "hours") {
			return nil
		}
		unsaved := metrics.UnsavedSince(historyOf(env), last)
		if len(unsaved) == 0 {
			return nil
		}
		var parts []string
		for _, u := range unsaved {
			parts = append(parts, u.What+": "+strconv.Itoa(u.Count))
		}
		level := enums.SeverityInfo
		if env.Today.Sub(last).Hours()/hoursPerDay > cfgFloat(cfg, "warn_days") {
			level = enums.SeverityWarn
		}
		return []Finding{{Fingerprint: "unsaved", Rule: "backups.unsaved", Severity: level, Message: "backups.unsaved",
			Params:  map[string]any{"items": strings.Join(parts, ", "), "tool": tool, "day": Day(last)},
			Sources: []string{system}}}
	})

	Register("system.slower_since_update", Cross, map[string]any{"factor": 1.5}, func(_ any, cfg map[string]any, env Env) []Finding {
		var found []Finding
		for _, s := range metrics.Slowdowns(historyOf(env), env.Today, cfgFloat(cfg, "factor")) {
			found = append(found, Finding{Fingerprint: "slower:" + s.Monitor + ":" + s.At.Format(time.DateOnly), Rule: "system.slower_since_update",
				Severity: enums.SeverityWarn, Message: "system.slower_since_update",
				Params:  map[string]any{"subject": s.Subject, "monitor": s.Monitor, "before": Num(s.BeforeMS, 0), "now": Num(s.NowMS, 0), "day": Day(s.At)},
				Sources: []string{system, string(enums.ServiceUptimeKuma)}})
		}
		return found
	})

	Register("pangolin.exposure", Cross, nil, func(_ any, _ map[string]any, env Env) []Finding {
		pangolin, ok := env.Datasets[string(enums.ServicePangolin)].(*sources.PangolinDataset)
		if !ok {
			return nil
		}
		certs, _ := env.Datasets[string(enums.ServiceCerts)].(*sources.CertDataset)
		var found []Finding
		for _, r := range metrics.Exposure(pangolin, certs, metrics.PendingUpdates(env.Datasets), env.Today) {
			if r.Risk < 2 {
				continue
			}
			found = append(found, Finding{Fingerprint: "exposed:" + r.Domain, Rule: "pangolin.exposure", Severity: enums.SeverityWarn,
				Message: "pangolin.exposure", Params: map[string]any{"name": r.Name, "domain": r.Domain, "updates": shortList(r.Updates),
					"cert": r.CertDays},
				Sources: []string{string(enums.ServicePangolin)}})
		}
		return found
	})

	Register("authentik.login_anomaly", Cross, nil, func(_ any, _ map[string]any, env Env) []Finding {
		ak, ok := env.Datasets[string(enums.ServiceAuthentik)].(*sources.AuthentikDataset)
		if !ok {
			return nil
		}
		geo, _ := env.Datasets[string(enums.ServiceDawarich)].(*sources.DawarichDataset)
		var found []Finding
		for _, a := range metrics.LoginAnomalies(ak, historyOf(env), geo, env.Today) {
			level, msg := enums.SeverityWarn, "authentik.new_country"
			if a.Reason == metrics.LoginFar {
				level, msg = enums.SeverityCritical, "authentik.far_login"
			}
			found = append(found, Finding{Fingerprint: "login:" + a.User + ":" + a.At.Format(time.RFC3339), Rule: "authentik.login_anomaly",
				Severity: level, Message: msg, Params: map[string]any{"user": a.User, "country": a.Country, "city": a.City, "ip": a.IP,
					"km": Num(a.KM, 0), "when": Day(a.At)},
				ActionURL: strings.TrimRight(ak.URL, "/") + "/if/admin/#/events/log", ActionLabel: "open_in_authentik",
				Sources: []string{string(enums.ServiceAuthentik)}})
		}
		return found
	})

	Register("dns.device_spike", Cross, map[string]any{"factor": 5.0, "min_queries": 1000.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		var found []Finding
		for _, dns := range dnsFilters(env) {
			for _, s := range metrics.DeviceSpikes(dns, historyOf(env), env.Today, cfgFloat(cfg, "factor"), cfgInt(cfg, "min_queries")) {
				found = append(found, Finding{Fingerprint: "spike:" + s.IP + ":" + env.Today.Format(time.DateOnly), Rule: "dns.device_spike",
					Severity: enums.SeverityWarn, Message: "dns.device_spike",
					Params:    map[string]any{"device": deviceName(s.DNSClient), "queries": s.Queries, "usual": Num(s.Usual, 0), "blocked": s.Blocked},
					ActionURL: dns.URL, Sources: []string{system}})
			}
		}
		return found
	})

	Register("dns.new_device", Cross, nil, func(_ any, _ map[string]any, env Env) []Finding {
		var found []Finding
		for _, dns := range dnsFilters(env) {
			for _, c := range metrics.NewDevices(dns, historyOf(env), env.Today) {
				found = append(found, Finding{Fingerprint: "new:" + c.IP, Rule: "dns.new_device", Severity: enums.SeverityInfo,
					Message: "dns.new_device", Params: map[string]any{"ip": c.IP, "queries": c.Queries, "blocked": c.Blocked},
					ActionURL: dns.URL, Sources: []string{system}})
			}
		}
		return found
	})

	Register("speedtest.contract", Cross, map[string]any{"share": 0.9, "min_count": 3.0, "days": 30.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		st, ok := env.Datasets[string(enums.ServiceSpeedtest)].(*sources.SpeedtestDataset)
		if !ok || st.ExpectDown <= 0 {
			return nil
		}
		r := metrics.SpeedDays(historyOf(env), st.ExpectDown, cfgFloat(cfg, "share"), env.Today, cfgInt(cfg, "days"))
		if r.BelowDays < cfgInt(cfg, "min_count") {
			return nil
		}
		return []Finding{{Fingerprint: "contract:" + env.Today.Format("2006-01"), Rule: "speedtest.contract", Severity: enums.SeverityInfo,
			Message: "speedtest.contract", Params: map[string]any{"below": r.BelowDays, "measured": len(r.Days), "outages": len(r.Outages),
				"expect": Num(st.ExpectDown, 0), "share": Num(cfgFloat(cfg, "share")*100, 0)},
			ActionURL: "/reports/isp", ActionLabel: "isp_report", Sources: []string{string(enums.ServiceSpeedtest)}}}
	})

	Register("dwd.storm_prep", Cross, map[string]any{"hours": 12.0, "backup_hours": 12.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		dwd, ok := env.Datasets[string(enums.ServiceDWD)].(*sources.DWDDataset)
		if !ok {
			return nil
		}
		warnings := metrics.StormWarnings(dwd, env.Today, cfgInt(cfg, "hours"))
		last, tool := metrics.LastBackup(env.Datasets)
		if len(warnings) == 0 || (!last.IsZero() && env.Today.Sub(last).Hours() < cfgFloat(cfg, "backup_hours")) {
			return nil
		}
		w := warnings[0]
		params := map[string]any{"event": w.Headline, "from": Day(w.Onset), "tool": tool, "day": Day(last), "ups": ""}
		if hass, ok := env.Datasets[string(enums.ServiceHomeAssistant)].(*sources.HassDataset); ok {
			params["ups"] = upsState(hass)
		}
		msg := "dwd.storm_prep"
		if last.IsZero() {
			msg = "dwd.storm_prep_nobackup"
		}
		return []Finding{{Fingerprint: "storm:" + w.ID, Rule: "dwd.storm_prep", Severity: enums.SeverityWarn, Message: msg,
			Params: params, Sources: []string{string(enums.ServiceDWD), system}}}
	})

	Register("gluetun.downloads_exposed", Cross, nil, func(_ any, _ map[string]any, env Env) []Finding {
		vpn, ok1 := env.Datasets[string(enums.ServiceGluetun)].(*sources.GluetunDataset)
		sab, ok2 := env.Datasets[string(enums.ServiceSabnzbd)].(*sources.SabnzbdDataset)
		if !ok1 || !ok2 || sab.Paused || (sab.Slots == 0 && sab.SpeedKB == 0) {
			return nil
		}
		wrongCountry := vpn.ExpectedCountry != "" && vpn.Country != "" && !strings.EqualFold(vpn.ExpectedCountry, vpn.Country)
		leaking := vpn.ExitIP != "" && vpn.ExitIP == vpn.OwnIP
		if vpn.Status == "running" && !leaking && !wrongCountry {
			return nil
		}
		return []Finding{{Fingerprint: "exposed", Rule: "gluetun.downloads_exposed", Severity: enums.SeverityCritical,
			Message: "gluetun.downloads_exposed", Params: map[string]any{"status": vpn.Status, "queue": sab.Slots},
			ActionURL: sab.URL, ActionLabel: "open_in_sabnzbd", Sources: []string{string(enums.ServiceGluetun), string(enums.ServiceSabnzbd)}}}
	})
}

// deviceName shows a DNS client's name, else its address.
func deviceName(c sources.DNSClient) string {
	if c.Name != "" {
		return c.Name
	}
	return c.IP
}

// upsState reads a UPS battery sensor ("ups" in the entity id) as "87 %".
func upsState(hass *sources.HassDataset) string {
	for _, e := range hass.Entities {
		if strings.Contains(e.ID, "ups") && e.DeviceClass == "battery" {
			return e.State + " " + e.Unit
		}
	}
	return ""
}
