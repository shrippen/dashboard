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
	snipeSource = "snipeit"
	snipeOpen   = "open_in_snipeit"
	gwgLimitNet = 800.0
	vatFactor   = 1.19
)

func snipeURL(data *sources.SnipeDataset, path string) string {
	return strings.TrimRight(data.URL, "/") + "/" + path
}

func sData(data any) *sources.SnipeDataset {
	d, _ := data.(*sources.SnipeDataset)
	return d
}

// RecentPurchases returns assets purchased within `days` with a known
// cost — used by the cross-service snipe.expense_missing rule.
func RecentPurchases(data *sources.SnipeDataset, today time.Time, days int) []sources.SnipeAsset {
	since := today.AddDate(0, 0, -days)
	var out []sources.SnipeAsset
	for _, a := range data.Assets {
		d, ok := metrics.ParseDay(a.PurchaseDate)
		if ok && !d.Before(since) && a.PurchaseCost != 0 {
			out = append(out, a)
		}
	}
	return out
}

func init() {
	Register("snipe.warranty_expiring", string(enums.ServiceSnipeIT), map[string]any{"info_days": 60.0, "warn_days": 14.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := sData(raw)
			var found []Finding
			for _, a := range data.Assets {
				ends, ok := metrics.ParseDay(a.WarrantyExpires)
				if !ok {
					continue
				}
				daysLeft := int(ends.Sub(env.Today).Hours() / 24)
				if daysLeft < 0 || daysLeft > cfgInt(cfg, "info_days") {
					continue
				}
				level := enums.SeverityInfo
				if daysLeft <= cfgInt(cfg, "warn_days") {
					level = enums.SeverityWarn
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("warranty:%d", a.ID), Rule: "snipe.warranty_expiring",
					Severity: level, Message: "snipe.warranty",
					Params:    map[string]any{"asset": a.Name, "tag": a.Tag, "day": Day(ends)},
					ActionURL: snipeURL(data, fmt.Sprintf("hardware/%d", a.ID)), ActionLabel: snipeOpen,
					Due: ends.Format("2006-01-02"), Sources: []string{snipeSource},
				})
			}
			return found
		})

	Register("snipe.eol_reached", string(enums.ServiceSnipeIT), nil,
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := sData(raw)
			var found []Finding
			for _, a := range data.Assets {
				d, ok := metrics.ParseDay(a.EOLDate)
				if !ok || d.After(env.Today) {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("eol:%d", a.ID), Rule: "snipe.eol_reached",
					Severity: enums.SeverityInfo, Message: "snipe.eol",
					Params:    map[string]any{"asset": a.Name, "tag": a.Tag, "day": DayStr(a.EOLDate)},
					ActionURL: snipeURL(data, fmt.Sprintf("hardware/%d", a.ID)), ActionLabel: snipeOpen,
					Sources: []string{snipeSource},
				})
			}
			return found
		})

	Register("snipe.license_expiring", string(enums.ServiceSnipeIT), map[string]any{"days": 30.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := sData(raw)
			var found []Finding
			for _, l := range data.Licenses {
				d, ok := metrics.ParseDay(l.Expires)
				if !ok {
					continue
				}
				daysLeft := int(d.Sub(env.Today).Hours() / 24)
				if daysLeft < 0 || daysLeft > cfgInt(cfg, "days") {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("license:%d", l.ID), Rule: "snipe.license_expiring",
					Severity: enums.SeverityWarn, Message: "snipe.license",
					Params:    map[string]any{"license": l.Name, "day": Day(d)},
					ActionURL: snipeURL(data, fmt.Sprintf("licenses/%d", l.ID)), ActionLabel: snipeOpen,
					Due: l.Expires, Sources: []string{snipeSource},
				})
			}
			return found
		})

	Register("snipe.license_seats", string(enums.ServiceSnipeIT), nil,
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := sData(raw)
			var found []Finding
			for _, l := range data.Licenses {
				if l.Seats == 0 || l.Free != 0 {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("seats:%d", l.ID), Rule: "snipe.license_seats",
					Severity: enums.SeverityInfo, Message: "snipe.seats",
					Params:    map[string]any{"license": l.Name, "seats": l.Seats},
					ActionURL: snipeURL(data, fmt.Sprintf("licenses/%d", l.ID)), ActionLabel: snipeOpen,
					Sources: []string{snipeSource},
				})
			}
			return found
		})

	Register("snipe.audit_overdue", string(enums.ServiceSnipeIT), nil,
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := sData(raw)
			names := map[int64]string{}
			for _, a := range data.Assets {
				names[a.ID] = a.Name
			}
			var found []Finding
			for _, aid := range data.AuditOverdue {
				name := names[aid]
				if name == "" {
					name = fmt.Sprintf("#%d", aid)
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("audit:%d", aid), Rule: "snipe.audit_overdue",
					Severity: enums.SeverityWarn, Message: "snipe.audit",
					Params:    map[string]any{"asset": name},
					ActionURL: snipeURL(data, fmt.Sprintf("hardware/%d", aid)), ActionLabel: snipeOpen,
					Sources: []string{snipeSource},
				})
			}
			return found
		})

	Register("snipe.consumable_low", string(enums.ServiceSnipeIT), nil,
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := sData(raw)
			var found []Finding
			for _, c := range data.Consumables {
				if c.Min == 0 || c.Remaining >= c.Min {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("stock:%d", c.ID), Rule: "snipe.consumable_low",
					Severity: enums.SeverityInfo, Message: "snipe.stock",
					Params:    map[string]any{"item": c.Name, "left": c.Remaining, "min": c.Min},
					ActionURL: snipeURL(data, fmt.Sprintf("consumables/%d", c.ID)), ActionLabel: snipeOpen,
					Sources: []string{snipeSource},
				})
			}
			return found
		})

	Register("snipe.unassigned_deployable", string(enums.ServiceSnipeIT), map[string]any{"days": 90.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := sData(raw)
			var found []Finding
			for _, a := range data.Assets {
				if !a.Deployable || a.Assigned {
					continue
				}
				d, ok := metrics.ParseDay(a.LastChange)
				if !ok {
					continue
				}
				age := int(env.Today.Sub(d).Hours() / 24)
				if age < cfgInt(cfg, "days") {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("unused:%d", a.ID), Rule: "snipe.unassigned_deployable",
					Severity: enums.SeverityInfo, Message: "snipe.unused",
					Params:    map[string]any{"asset": a.Name, "days": age},
					ActionURL: snipeURL(data, fmt.Sprintf("hardware/%d", a.ID)), ActionLabel: snipeOpen,
					Sources: []string{snipeSource},
				})
			}
			return found
		})

	Register("snipe.gwg_hint", string(enums.ServiceSnipeIT), map[string]any{"cost_is_gross": true},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := sData(raw)
			var found []Finding
			for _, a := range data.Assets {
				bought, ok := metrics.ParseDay(a.PurchaseDate)
				if !ok || bought.Year() != env.Today.Year() {
					continue
				}
				net := a.PurchaseCost
				if cfgBool(cfg, "cost_is_gross") {
					net /= vatFactor
				}
				if net <= gwgLimitNet {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("gwg:%d", a.ID), Rule: "snipe.gwg_hint",
					Severity: enums.SeverityInfo, Message: "snipe.gwg",
					Params:    map[string]any{"asset": a.Name, "net": Money(net, "")},
					ActionURL: snipeURL(data, fmt.Sprintf("hardware/%d", a.ID)), ActionLabel: snipeOpen,
					Sources: []string{snipeSource},
				})
			}
			return found
		})
}
