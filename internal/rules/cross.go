// Package rules: cross-service rules of one space (and one credential owner).
//
//	dawarich <-> kimai      client visit without booking, booking "on site" without visit
//	dawarich                travel costs, per diem, tracking stopped
//	snipeit <-> invoiceninja purchase without expense
package rules

import (
	"fmt"
	"strings"
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	"andon/internal/sources"
)

const (
	reportDays   = 7
	lookbackDays = 30
)

func areaMapping(env Env) map[string]metrics.AreaMapping {
	return metrics.ParseAreaMapping(env.Options[string(enums.ServiceDawarich)])
}

func booked(kimai *sources.KimaiDataset) map[[2]any]bool {
	out := map[[2]any]bool{}
	for _, s := range kimai.Timesheets {
		if d, ok := metrics.ParseDay(s.Begin); ok {
			out[[2]any{d, s.CustomerID}] = true
		}
	}
	return out
}

func kimaiNames(kimai *sources.KimaiDataset) map[int64]string {
	return metrics.KimaiCustomerNames(kimai)
}

func lastMonth(today time.Time) (time.Time, time.Time) {
	start := metrics.AddMonths(today, -1)
	return start, metrics.MonthStart(today).AddDate(0, 0, -1)
}

func init() {
	Register("geo.visit_without_time", Cross, map[string]any{"min_minutes": 120.0},
		func(_ any, cfg map[string]any, env Env) []Finding {
			kimai, ok1 := env.Datasets[string(enums.ServiceKimai)].(*sources.KimaiDataset)
			geo, ok2 := env.Datasets[string(enums.ServiceDawarich)].(*sources.DawarichDataset)
			if !ok1 || !ok2 {
				return nil
			}
			bk, names := booked(kimai), kimaiNames(kimai)
			since := env.Today.AddDate(0, 0, -lookbackDays)

			var found []Finding
			for _, v := range metrics.ClientVisits(geo, areaMapping(env)) {
				if v.Day.Before(since) || !v.Day.Before(env.Today) {
					continue
				}
				if v.Minutes < cfgInt(cfg, "min_minutes") {
					continue
				}
				if bk[[2]any{v.Day, v.CustomerID}] {
					continue
				}
				name := names[v.CustomerID]
				if name == "" {
					name = "?"
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("visit:%s:%d", v.Day.Format("2006-01-02"), v.CustomerID),
					Rule:        "geo.visit_without_time", Severity: enums.SeverityWarn, Message: "geo.visit_without_time",
					Params: map[string]any{
						"customer": name, "day": Day(v.Day), "hours": Num(float64(v.Minutes)/60, 1),
					},
					ActionURL: strings.TrimRight(kimai.URL, "/") + "/timesheet/", ActionLabel: "open_in_kimai",
					Sources: []string{string(enums.ServiceDawarich), string(enums.ServiceKimai)},
				})
			}
			return found
		})

	Register("geo.time_without_visit", Cross, map[string]any{"keywords": []any{"vor ort", "on-site", "onsite"}},
		func(_ any, cfg map[string]any, env Env) []Finding {
			kimai, ok1 := env.Datasets[string(enums.ServiceKimai)].(*sources.KimaiDataset)
			geo, ok2 := env.Datasets[string(enums.ServiceDawarich)].(*sources.DawarichDataset)
			if !ok1 || !ok2 {
				return nil
			}
			mapping := areaMapping(env)
			visited := map[[2]any]bool{}
			for _, v := range metrics.ClientVisits(geo, mapping) {
				visited[[2]any{v.Day, v.CustomerID}] = true
			}
			mapped := map[int64]bool{}
			for _, m := range mapping {
				if m.CustomerID != 0 {
					mapped[m.CustomerID] = true
				}
			}
			names := kimaiNames(kimai)
			since := env.Today.AddDate(0, 0, -lookbackDays)
			words := stringsSlice(cfg["keywords"])

			var found []Finding
			for _, s := range kimai.Timesheets {
				when, ok := metrics.ParseDay(s.Begin)
				if !ok || when.Before(since) || !mapped[s.CustomerID] {
					continue
				}
				if !containsAny(strings.ToLower(s.Activity), words) {
					continue
				}
				if visited[[2]any{when, s.CustomerID}] {
					continue
				}
				name := names[s.CustomerID]
				if name == "" {
					name = "?"
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("novisit:%d", s.ID), Rule: "geo.time_without_visit",
					Severity: enums.SeverityInfo, Message: "geo.time_without_visit",
					Params:  map[string]any{"customer": name, "day": Day(when)},
					Sources: []string{string(enums.ServiceKimai), string(enums.ServiceDawarich)},
				})
			}
			return found
		})

	Register("geo.travel_costs", Cross, map[string]any{"km_rate": 0.30},
		func(_ any, cfg map[string]any, env Env) []Finding {
			geo, ok := env.Datasets[string(enums.ServiceDawarich)].(*sources.DawarichDataset)
			if !ok || env.Today.Day() > reportDays {
				return nil
			}
			start, end := lastMonth(env.Today)
			trips := metrics.Trips(geo, areaMapping(env), start, end)
			var km float64
			for _, t := range trips {
				km += t.KM
			}
			if km == 0 {
				return nil
			}
			return []Finding{{
				Fingerprint: "travel:" + start.Format("2006-01"), Rule: "geo.travel_costs",
				Severity: enums.SeverityInfo, Message: "geo.travel_costs",
				Params: map[string]any{
					"trips": len(trips), "km": Num(km, 0), "amount": Money(km*cfgFloat(cfg, "km_rate"), ""),
					"month": start.Format("01/2006"),
				},
				Sources: []string{string(enums.ServiceDawarich)},
			}}
		})

	Register("geo.per_diem", Cross, map[string]any{"over_8h": 14.0, "full_day": 28.0},
		func(_ any, cfg map[string]any, env Env) []Finding {
			geo, ok := env.Datasets[string(enums.ServiceDawarich)].(*sources.DawarichDataset)
			if !ok || env.Today.Day() > reportDays {
				return nil
			}
			start, end := lastMonth(env.Today)
			var days int
			for _, t := range metrics.Trips(geo, areaMapping(env), start, end) {
				if t.AwayMin > 8*60 {
					days++
				}
			}
			if days == 0 {
				return nil
			}
			return []Finding{{
				Fingerprint: "perdiem:" + start.Format("2006-01"), Rule: "geo.per_diem",
				Severity: enums.SeverityInfo, Message: "geo.per_diem",
				Params: map[string]any{
					"days": days, "amount": Money(float64(days)*cfgFloat(cfg, "over_8h"), ""), "month": start.Format("01/2006"),
				},
				Sources: []string{string(enums.ServiceDawarich)},
			}}
		})

	Register("geo.no_data", string(enums.ServiceDawarich), map[string]any{"hours": 24.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data, ok := raw.(*sources.DawarichDataset)
			if !ok {
				return nil
			}
			last, ok := metrics.ParseTime(data.LastPoint)
			if !ok {
				return nil
			}
			silent := time.Now().UTC().Sub(last).Hours()
			if silent < cfgFloat(cfg, "hours") {
				return nil
			}
			return []Finding{{
				Fingerprint: "nodata", Rule: "geo.no_data", Severity: enums.SeverityWarn, Message: "geo.no_data",
				Params:    map[string]any{"hours": Num(silent, 0)},
				ActionURL: strings.TrimRight(data.URL, "/") + "/map", ActionLabel: "open_in_dawarich",
				Sources: []string{string(enums.ServiceDawarich)},
			}}
		})

	Register("snipe.expense_missing", Cross, map[string]any{"days": 365.0, "tolerance": 0.05, "date_window": 14.0},
		func(_ any, cfg map[string]any, env Env) []Finding {
			assets, ok1 := env.Datasets[string(enums.ServiceSnipeIT)].(*sources.SnipeDataset)
			ninja, ok2 := env.Datasets[string(enums.ServiceInvoiceNinja)].(*sources.NinjaDataset)
			if !ok1 || !ok2 {
				return nil
			}
			var found []Finding
			for _, asset := range RecentPurchases(assets, env.Today, cfgInt(cfg, "days")) {
				bought, ok := metrics.ParseDay(asset.PurchaseDate)
				if !ok || bought.Year() != env.Today.Year() {
					continue
				}
				cost := asset.PurchaseCost
				tolerance := cost * cfgFloat(cfg, "tolerance")
				window := cfgInt(cfg, "date_window")
				matched := false
				for _, e := range ninja.Expenses {
					near := absF(e.Amount-cost) <= tolerance || absF(e.Amount-e.Tax-cost) <= tolerance
					if !near {
						continue
					}
					d, ok := metrics.ParseDay(e.Date)
					if ok && absDays(d, bought) <= window {
						matched = true
						break
					}
				}
				if matched {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("expense:%d", asset.ID), Rule: "snipe.expense_missing",
					Severity: enums.SeverityInfo, Message: "snipe.expense_missing",
					Params:  map[string]any{"asset": asset.Name, "amount": Money(cost, ""), "day": Day(bought)},
					Sources: []string{string(enums.ServiceSnipeIT), string(enums.ServiceInvoiceNinja)},
				})
			}
			return found
		})
}

func stringsSlice(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, s := range list {
		if str, ok := s.(string); ok {
			out = append(out, strings.ToLower(str))
		}
	}
	return out
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}

func absF(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func absDays(a, b time.Time) int {
	d := int(a.Sub(b).Hours() / 24)
	if d < 0 {
		return -d
	}
	return d
}
