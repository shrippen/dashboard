package rules

// Rules of the network, media and everyday integrations:
//
//	tailscale.key_expiry    device key expires within warn_days (expired: critical)
//	tailscale.offline       devices unseen for days
//	gateway.wan_down        a WAN gateway is down (critical)
//	gateway.devices_offline UniFi devices offline
//	gateway.updates         firmware updates waiting
//	mediaserver.update      Jellyfin update available
//	arr.health              Sonarr/Radarr health checks (error: warn)
//	arr.stuck               downloads stuck with a warning
//	vaultwarden.no_2fa      active users without two-factor login
//	speedtest.slow          below share of the expected speed
//	grocy.expired           expired products
//	grocy.missing           products below minimum stock
//	grocy.chores_overdue    chores past due
//	dwd.warning             weather warning (moderate: info … extreme: critical)
//	github.ci_failed        latest CI run on the default branch failed
//	energy.cost_rising      last 7 days cost more than factor × the 7 before

import (
	"strings"

	"andon/internal/enums"
	"andon/internal/sources"
)

const (
	ciFailure     = "failure"
	arrError      = "error"
	weekDays      = 7
	costRuleID    = "energy.cost_rising"
	dwdWarningKey = "dwd.warning"
)

// dwdLevels maps warning severities to hint levels; minor warnings stay silent.
var dwdLevels = map[string]enums.Severity{
	sources.WarnModerate: enums.SeverityInfo,
	sources.WarnSevere:   enums.SeverityWarn,
	sources.WarnExtreme:  enums.SeverityCritical,
}

func init() {
	registerNetworkRules()
	registerMediaRules()
	registerEverydayRules()
	topicRules[TopicUpdates] = append(topicRules[TopicUpdates], "gateway.updates", "mediaserver.update")
}

func registerNetworkRules() {
	ts := string(enums.ServiceTailscale)
	Register("tailscale.key_expiry", ts, map[string]any{"warn_days": 14}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.TailscaleDataset)
		var found []Finding
		for _, d := range data.Devices {
			if d.KeyExpiry.IsZero() {
				continue
			}
			left := int(d.KeyExpiry.Sub(env.Today).Hours() / hoursPerDay)
			if left > cfgInt(cfg, "warn_days") {
				continue
			}
			level := enums.SeverityWarn
			if left < 0 {
				level = enums.SeverityCritical
			}
			found = append(found, svcFinding(ts, "tailscale.key_expiry", "key:"+d.Name, "tailscale.key_expiry", level, data.URL,
				map[string]any{"name": d.Name, "day": Day(d.KeyExpiry)}))
		}
		return found
	})
	Register("tailscale.offline", ts, map[string]any{"days": 7}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.TailscaleDataset)
		var names []string
		for _, d := range data.Devices {
			if !d.Online && !d.LastSeen.IsZero() && env.Today.Sub(d.LastSeen).Hours()/hoursPerDay > cfgFloat(cfg, "days") {
				names = append(names, d.Name)
			}
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{svcFinding(ts, "tailscale.offline", "offline", "tailscale.offline", enums.SeverityInfo, data.URL,
			map[string]any{"count": len(names), "names": shortList(names), "days": cfgInt(cfg, "days")})}
	})

	gw := string(enums.ServiceGateway)
	Register("gateway.wan_down", gw, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.GatewayDataset)
		var found []Finding
		for _, g := range data.Gateways {
			if !g.Up {
				found = append(found, svcFinding(gw, "gateway.wan_down", "wan:"+g.Name, "gateway.wan_down", enums.SeverityCritical, data.URL,
					map[string]any{"name": g.Name, "loss": Num(g.Loss, 0)}))
			}
		}
		return found
	})
	Register("gateway.devices_offline", gw, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.GatewayDataset)
		var names []string
		for _, d := range data.Devices {
			if !d.Online {
				names = append(names, d.Name)
			}
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{svcFinding(gw, "gateway.devices_offline", "devices", "gateway.devices_offline", enums.SeverityWarn, data.URL,
			map[string]any{"count": len(names), "names": shortList(names)})}
	})
	Register("gateway.updates", gw, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.GatewayDataset)
		if data.Updates == 0 {
			return nil
		}
		return []Finding{svcFinding(gw, "gateway.updates", "updates", "gateway.updates", enums.SeverityInfo, data.URL,
			map[string]any{"count": data.Updates, "version": data.Version})}
	})
}

func registerMediaRules() {
	ms := string(enums.ServiceMediaServer)
	Register("mediaserver.update", ms, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.MediaServerDataset)
		if !data.Update {
			return nil
		}
		return []Finding{svcFinding(ms, "mediaserver.update", "update", "mediaserver.update", enums.SeverityInfo, data.URL,
			map[string]any{"version": data.Version})}
	})

	arr := string(enums.ServiceArr)
	Register("arr.health", arr, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.ArrDataset)
		var found []Finding
		for _, h := range data.Health {
			level := enums.SeverityInfo
			if h.Level == arrError {
				level = enums.SeverityWarn
			}
			found = append(found, svcFinding(arr, "arr.health", "health:"+h.Message, "arr.health", level, data.URL,
				map[string]any{"app": data.App, "message": h.Message}))
		}
		return found
	})
	Register("arr.stuck", arr, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.ArrDataset)
		if len(data.Stuck) == 0 {
			return nil
		}
		return []Finding{svcFinding(arr, "arr.stuck", "stuck", "arr.stuck", enums.SeverityWarn, strings.TrimRight(data.URL, "/")+"/activity/queue",
			map[string]any{"app": data.App, "count": len(data.Stuck), "names": shortList(append([]string(nil), data.Stuck...))})}
	})
}

func registerEverydayRules() {
	vw := string(enums.ServiceVaultwarden)
	Register("vaultwarden.no_2fa", vw, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.VaultwardenDataset)
		var names []string
		for _, u := range data.Users {
			if u.Enabled && !u.TwoFactor {
				names = append(names, u.Email)
			}
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{svcFinding(vw, "vaultwarden.no_2fa", "no_2fa", "vaultwarden.no_2fa", enums.SeverityWarn,
			strings.TrimRight(data.URL, "/")+"/admin/users/overview", map[string]any{"count": len(names), "names": shortList(names)})}
	})

	st := string(enums.ServiceSpeedtest)
	Register("speedtest.slow", st, map[string]any{"share": 0.5}, func(raw any, cfg map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.SpeedtestDataset)
		share := cfgFloat(cfg, "share")
		slowDown := data.ExpectDown > 0 && data.Down < data.ExpectDown*share
		slowUp := data.ExpectUp > 0 && data.Up < data.ExpectUp*share
		if data.At.IsZero() || (!slowDown && !slowUp) {
			return nil
		}
		return []Finding{svcFinding(st, "speedtest.slow", "slow", "speedtest.slow", enums.SeverityWarn, data.URL,
			map[string]any{"down": Num(data.Down, 0), "up": Num(data.Up, 0), "expect_down": Num(data.ExpectDown, 0), "expect_up": Num(data.ExpectUp, 0)})}
	})

	gr := string(enums.ServiceGrocy)
	names := func(list []sources.Product) []string {
		out := make([]string, 0, len(list))
		for _, p := range list {
			out = append(out, p.Name)
		}
		return out
	}
	Register("grocy.expired", gr, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.GrocyDataset)
		if len(data.Expired) == 0 {
			return nil
		}
		return []Finding{svcFinding(gr, "grocy.expired", "expired", "grocy.expired", enums.SeverityWarn, strings.TrimRight(data.URL, "/")+"/stockoverview",
			map[string]any{"count": len(data.Expired), "names": shortList(names(data.Expired))})}
	})
	Register("grocy.missing", gr, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.GrocyDataset)
		if len(data.Missing) == 0 {
			return nil
		}
		return []Finding{svcFinding(gr, "grocy.missing", "missing", "grocy.missing", enums.SeverityInfo, strings.TrimRight(data.URL, "/")+"/shoppinglist",
			map[string]any{"count": len(data.Missing), "names": shortList(names(data.Missing))})}
	})
	Register("grocy.chores_overdue", gr, nil, func(raw any, _ map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.GrocyDataset)
		var due []string
		for _, c := range data.Chores {
			if c.Due.Before(env.Today) {
				due = append(due, c.Name)
			}
		}
		if len(due) == 0 {
			return nil
		}
		return []Finding{svcFinding(gr, "grocy.chores_overdue", "chores", "grocy.chores_overdue", enums.SeverityInfo, strings.TrimRight(data.URL, "/")+"/choresoverview",
			map[string]any{"count": len(due), "names": shortList(due)})}
	})

	dwd := string(enums.ServiceDWD)
	Register(dwdWarningKey, dwd, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.DWDDataset)
		var found []Finding
		for _, w := range data.Warnings {
			level, ok := dwdLevels[w.Severity]
			if !ok {
				continue
			}
			found = append(found, Finding{Fingerprint: "warn:" + w.ID, Rule: dwdWarningKey, Severity: level, Message: dwdWarningKey,
				Params: map[string]any{"event": w.Headline, "place": data.Place, "until": Day(w.Expire)}, Sources: []string{dwd}})
		}
		return found
	})

	gh := string(enums.ServiceGitHub)
	Register("github.ci_failed", gh, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.GitHubDataset)
		var found []Finding
		for _, r := range data.Repos {
			if r.CI == ciFailure {
				found = append(found, Finding{Fingerprint: "ci:" + r.Name, Rule: "github.ci_failed", Severity: enums.SeverityWarn,
					Message: "github.ci_failed", Params: map[string]any{"repo": r.Name}, ActionURL: r.CIURL, ActionLabel: "open_in_github", Sources: []string{gh}})
			}
		}
		return found
	})

	tb := string(enums.ServiceTibber)
	Register(costRuleID, tb, map[string]any{"factor": 1.3}, func(raw any, cfg map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.TibberDataset)
		if len(data.Days) < 2*weekDays {
			return nil
		}
		sum := func(days []sources.EnergyDay) float64 {
			total := 0.0
			for _, d := range days {
				total += d.Cost
			}
			return total
		}
		n := len(data.Days)
		recent, before := sum(data.Days[n-weekDays:]), sum(data.Days[n-2*weekDays:n-weekDays])
		if before <= 0 || recent < before*cfgFloat(cfg, "factor") {
			return nil
		}
		return []Finding{{Fingerprint: "cost", Rule: costRuleID, Severity: enums.SeverityInfo, Message: costRuleID,
			Params: map[string]any{"recent": Money(recent, data.Currency), "before": Money(before, data.Currency)}, Sources: []string{tb}}}
	})
}
