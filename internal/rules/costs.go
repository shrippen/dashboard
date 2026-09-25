package rules

// Cost rules of the homelab (phase 13):
//
//	system.unused_service   running stack, guest or app nobody used for months
//	energy.shift_jobs       backups run in expensive hours; cheapest hour named
//	snipe.replace_worth     old device where a frugal one pays back soon
//	energy.weather_adjusted last week used more power than the weather explains

import (
	"strings"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

const bytesPerGB = 1 << 30

func init() {
	Register("system.unused_service", Cross, map[string]any{"days": 90.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		var found []Finding
		for _, u := range metrics.UnusedServices(env.Datasets, Usages(env), env.Today, cfgInt(cfg, "days")) {
			found = append(found, Finding{Fingerprint: "unused:" + u.Kind + ":" + strings.ToLower(u.Name), Rule: "system.unused_service",
				Severity: enums.SeverityInfo, Message: "system.unused_service",
				Params:  map[string]any{"name": u.Name, "kind": u.Kind, "days": cfgInt(cfg, "days"), "gb": Num(u.MemBytes/bytesPerGB, 1)},
				Sources: []string{system}})
		}
		return found
	})

	Register("energy.shift_jobs", Cross, map[string]any{"min_saving": 0.05}, func(_ any, cfg map[string]any, env Env) []Finding {
		tibber, ok := env.Datasets[string(enums.ServiceTibber)].(*sources.TibberDataset)
		if !ok {
			return nil
		}
		var found []Finding
		for _, j := range metrics.JobPrices(tibber, env.Datasets) {
			if j.Price-j.CheapPrice < cfgFloat(cfg, "min_saving") {
				continue
			}
			found = append(found, Finding{Fingerprint: "shift:" + j.Job, Rule: "energy.shift_jobs", Severity: enums.SeverityInfo,
				Message: "energy.shift_jobs", Params: map[string]any{"job": j.Job, "hour": j.Hour, "cheap_hour": j.CheapHour,
					"price": Money(j.Price, tibber.Currency), "cheap_price": Money(j.CheapPrice, tibber.Currency)},
				Sources: []string{string(enums.ServiceTibber)}})
		}
		return found
	})

	Register("snipe.replace_worth", Cross, map[string]any{"new_watts": 15.0, "new_cost": 400.0, "max_months": 36.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		snipe, ok1 := env.Datasets[string(enums.ServiceSnipeIT)].(*sources.SnipeDataset)
		hass, ok2 := env.Datasets[string(enums.ServiceHomeAssistant)].(*sources.HassDataset)
		if !ok1 || !ok2 {
			return nil
		}
		tibber, _ := env.Datasets[string(enums.ServiceTibber)].(*sources.TibberDataset)
		price := metrics.PowerPrice(tibber, metrics.HomelabSettingsOf(env.Settings).PowerPrice)
		var found []Finding
		for _, r := range metrics.Replacements(snipe, hass, price, cfgFloat(cfg, "new_watts"), cfgFloat(cfg, "new_cost"), env.Today) {
			if r.PaybackMonths > cfgInt(cfg, "max_months") {
				continue
			}
			found = append(found, Finding{Fingerprint: "replace:" + strings.ToLower(r.Asset), Rule: "snipe.replace_worth", Severity: enums.SeverityInfo,
				Message: "snipe.replace_worth", Params: map[string]any{"name": r.Asset, "years": Num(r.AgeYears, 0), "watts": Num(r.Watts, 0),
					"yearly": Money(r.YearlyCost, ""), "months": r.PaybackMonths, "new_watts": Num(cfgFloat(cfg, "new_watts"), 0), "new_cost": Money(cfgFloat(cfg, "new_cost"), "")},
				Sources: []string{string(enums.ServiceSnipeIT), string(enums.ServiceHomeAssistant)}})
		}
		return found
	})

	Register("energy.weather_adjusted", Cross, map[string]any{"share": 0.15}, func(_ any, cfg map[string]any, env Env) []Finding {
		tibber, ok := env.Datasets[string(enums.ServiceTibber)].(*sources.TibberDataset)
		if !ok {
			return nil
		}
		w, ok := metrics.WeatherAdjusted(tibber.Days)
		if !ok || w.Deviation < cfgFloat(cfg, "share") {
			return nil
		}
		return []Finding{{Fingerprint: "weather:" + metrics.WeekStart(env.Today).Format(time.DateOnly), Rule: "energy.weather_adjusted",
			Severity: enums.SeverityInfo, Message: "energy.weather_adjusted",
			Params:  map[string]any{"actual": Num(w.ActualKWh, 0), "expected": Num(w.ExpectedKWh, 0), "percent": Num(w.Deviation*100, 0)},
			Sources: []string{string(enums.ServiceTibber)}}}
	})
}
