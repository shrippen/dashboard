package rules

// Freelance rules across services (phase 13):
//
//	in.rate_below             full-cost rate far below the billed rate (Kimai, Ninja, Dawarich)
//	sure.subscription_unused  recurring payment, no use for months (Sure, Paperless, authentik, tiles)
//	sure.spendable_negative   taxes and fixed costs exceed the cash (Sure, Ninja)
//	calendar.unbooked         customer appointment without a booking (calendar, Kimai)
//	kimai.booked_free_day     booking on a holiday or during an absence
//	in.order_gap              far fewer hours than the same weeks last year (Kimai, Ninja)
//	kimai.workload            long weeks, no free day
//	kimai.margin_low          project margin below goal (Kimai, Ninja, cost rate)

import (
	"fmt"
	"strings"
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	"andon/internal/sources"
)

const (
	rateWindowDays   = 90
	marginWindowDays = 180
	orderGapDays     = 28
	unbookedDays     = 14
	defaultTaxRate   = 0.3
)

// hourlyCost is the space's internal cost per hour: costs.hourly_cost, or
// fixed monthly costs spread over the working hours of a month.
func hourlyCost(settings map[string]any) float64 {
	costs, _ := settings["costs"].(map[string]any)
	if v, ok := costs["hourly_cost"].(float64); ok && v > 0 {
		return v
	}
	const monthHours = 21 * 8
	if v, ok := costs["fixed_monthly"].(float64); ok && v > 0 {
		return v / monthHours
	}
	return 0
}

func taxRate(settings map[string]any) float64 {
	tax, _ := settings["tax"].(map[string]any)
	if v, ok := tax["income_tax_rate"].(float64); ok && v > 0 {
		return v
	}
	return defaultTaxRate
}

// Usages collects when things were last used: link tiles by title and
// host, authentik apps with logins this week.
func Usages(env Env) []metrics.Usage {
	var out []metrics.Usage
	if links, ok := boardLinks(env); ok {
		for _, l := range links {
			out = append(out, metrics.Usage{Name: l.Title + " " + hostOf(l.URL), Last: l.LastClick})
		}
	}
	if ak, ok := env.Datasets[string(enums.ServiceAuthentik)].(*sources.AuthentikDataset); ok {
		for _, a := range ak.Apps {
			u := metrics.Usage{Name: a.Name}
			if a.Events > 0 {
				u.Last = env.Today
			}
			out = append(out, u)
		}
	}
	return out
}

func init() {
	kimaiKey, ninjaKey := string(enums.ServiceKimai), string(enums.ServiceInvoiceNinja)

	Register("in.rate_below", Cross, map[string]any{"share": 0.75, "min_hours": 10.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		kimai, ok1 := env.Datasets[kimaiKey].(*sources.KimaiDataset)
		ninja, ok2 := env.Datasets[ninjaKey].(*sources.NinjaDataset)
		if !ok1 || !ok2 {
			return nil
		}
		geo, _ := env.Datasets[string(enums.ServiceDawarich)].(*sources.DawarichDataset)
		var found []Finding
		for _, r := range metrics.FullCostRates(kimai, ninja, geo, areaMapping(env), env.Today, rateWindowDays) {
			if r.BillableH < cfgFloat(cfg, "min_hours") || r.Full >= r.Nominal*cfgFloat(cfg, "share") {
				continue
			}
			found = append(found, Finding{
				Fingerprint: "rate:" + strings.ToLower(r.Customer), Rule: "in.rate_below", Severity: enums.SeverityInfo,
				Message: "in.rate_below", Params: map[string]any{"client": r.Customer, "full": Money(r.Full, ninja.Currency),
					"nominal": Money(r.Nominal, ninja.Currency), "other": Num(r.OtherH, 0), "travel": Num(r.TravelH, 0)},
				Sources: []string{kimaiKey, ninjaKey},
			})
		}
		return found
	})

	Register("sure.subscription_unused", Cross, map[string]any{"days": 60.0, "min_amount": 5.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		sure, ok := env.Datasets[string(enums.ServiceSure)].(*sources.SureDataset)
		if !ok {
			return nil
		}
		var contracts []sources.PaperlessContract
		if p, ok := env.Datasets[string(enums.ServicePaperless)].(*sources.PaperlessDataset); ok {
			contracts = p.Contracts
		}
		var found []Finding
		for _, s := range metrics.Subscriptions(sure, contracts, Usages(env)) {
			// Without a usage signal nothing can be said about use.
			if !s.UseKnown || s.Monthly < cfgFloat(cfg, "min_amount") {
				continue
			}
			if !s.LastUse.IsZero() && env.Today.Sub(s.LastUse).Hours()/hoursPerDay < cfgFloat(cfg, "days") {
				continue
			}
			msg, params := "sure.subscription_unused", map[string]any{"name": s.Name, "amount": Money(s.Monthly, sure.Currency), "days": cfgInt(cfg, "days")}
			if !s.Deadline.IsZero() {
				msg, params["deadline"] = "sure.subscription_unused_deadline", Day(s.Deadline)
			}
			found = append(found, Finding{Fingerprint: "sub:" + strings.ToLower(s.Name), Rule: "sure.subscription_unused",
				Severity: enums.SeverityInfo, Message: msg, Params: params, Sources: []string{string(enums.ServiceSure)}})
		}
		return found
	})

	Register("sure.spendable_negative", Cross, nil, func(_ any, _ map[string]any, env Env) []Finding {
		sure, ok1 := env.Datasets[string(enums.ServiceSure)].(*sources.SureDataset)
		ninja, ok2 := env.Datasets[ninjaKey].(*sources.NinjaDataset)
		if !ok1 || !ok2 {
			return nil
		}
		s := metrics.SafeToSpend(sure, ninja, env.Today, metrics.TaxVATInterval(env.Settings), metrics.TaxVATMethod(env.Settings), taxRate(env.Settings))
		if s.Free >= 0 {
			return nil
		}
		return []Finding{{Fingerprint: "spendable", Rule: "sure.spendable_negative", Severity: enums.SeverityWarn,
			Message: "sure.spendable_negative", Params: map[string]any{"cash": Money(s.Cash, sure.Currency),
				"reserved": Money(s.VAT+s.IncomeTax+s.Fixed, sure.Currency), "missing": Money(-s.Free, sure.Currency)},
			Sources: []string{string(enums.ServiceSure), ninjaKey}}}
	})

	Register("calendar.unbooked", Cross, nil, func(_ any, _ map[string]any, env Env) []Finding {
		cal, ok1 := env.Datasets[string(enums.ServiceCalendar)].(*sources.CalendarResult)
		kimai, ok2 := env.Datasets[kimaiKey].(*sources.KimaiDataset)
		if !ok1 || !ok2 {
			return nil
		}
		return unbooked(cal, kimai, env.Today)
	})

	Register("kimai.booked_free_day", string(enums.ServiceKimai), nil, func(raw any, _ map[string]any, env Env) []Finding {
		data := kimaiData(raw)
		free := metrics.KimaiFreeDays(data)
		minutes := map[time.Time]int{}
		since := env.Today.AddDate(0, 0, -lookbackDays)
		for _, s := range data.Timesheets {
			if d, ok := metrics.ParseDay(s.Begin); ok && !d.Before(since) && free[d] {
				minutes[d] += s.Minutes
			}
		}
		var found []Finding
		for d, m := range minutes {
			found = append(found, Finding{Fingerprint: "free:" + d.Format(time.DateOnly), Rule: "kimai.booked_free_day",
				Severity: enums.SeverityInfo, Message: "kimai.booked_free_day", Params: map[string]any{"day": Day(d), "hours": hoursParam(float64(m))},
				Sources: []string{kimaiSource}})
		}
		return found
	})

	Register("in.order_gap", Cross, map[string]any{"ratio": 0.6, "min_hours": 20.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		kimai, ok := env.Datasets[kimaiKey].(*sources.KimaiDataset)
		if !ok {
			return nil
		}
		ninja, _ := env.Datasets[ninjaKey].(*sources.NinjaDataset)
		g, ok := metrics.OrderGap(kimai, ninja, env.Today, orderGapDays)
		if !ok || g.LastYearH < cfgFloat(cfg, "min_hours") || g.RecentH >= g.LastYearH*cfgFloat(cfg, "ratio") {
			return nil
		}
		return []Finding{{Fingerprint: "gap:" + env.Today.Format("2006-01"), Rule: "in.order_gap", Severity: enums.SeverityWarn,
			Message: "in.order_gap", Params: map[string]any{"recent": Num(g.RecentH, 0), "last_year": Num(g.LastYearH, 0),
				"ahead": Num(g.AheadLastYearH, 0), "quotes": g.OpenQuotes, "quote_sum": Money(g.QuoteSum, "")},
			Sources: []string{kimaiKey, ninjaKey}}}
	})

	Register("kimai.workload", string(enums.ServiceKimai), map[string]any{"max_week_hours": 50.0, "weeks": 3.0, "days": 14.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			w := metrics.WorkloadOf(kimaiData(raw), env.Today, cfgFloat(cfg, "max_week_hours"))
			if w.LongWeeks < cfgInt(cfg, "weeks") && w.DaysSinceFree < cfgInt(cfg, "days") {
				return nil
			}
			return []Finding{{Fingerprint: "workload:" + metrics.WeekStart(env.Today).Format(time.DateOnly), Rule: "kimai.workload",
				Severity: enums.SeverityInfo, Message: "kimai.workload",
				Params: map[string]any{"weeks": w.LongWeeks, "last_week": Num(w.LastWeekH, 0), "late": w.LateEvenings,
					"weekend": w.WeekendDays, "free": w.DaysSinceFree},
				Sources: []string{kimaiSource}}}
		})

	Register("kimai.margin_low", Cross, map[string]any{"goal": 0.2, "min_amount": 1000.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		kimai, ok := env.Datasets[kimaiKey].(*sources.KimaiDataset)
		cost := hourlyCost(env.Settings)
		if !ok || cost == 0 {
			return nil
		}
		ninja, _ := env.Datasets[ninjaKey].(*sources.NinjaDataset)
		var found []Finding
		for _, m := range metrics.ProjectMargins(kimai, ninja, cost, env.Today, marginWindowDays) {
			if m.Revenue < cfgFloat(cfg, "min_amount") || m.Margin >= cfgFloat(cfg, "goal") {
				continue
			}
			found = append(found, Finding{Fingerprint: "margin:" + strings.ToLower(m.Project), Rule: "kimai.margin_low",
				Severity: enums.SeverityInfo, Message: "kimai.margin_low",
				Params: map[string]any{"project": m.Project, "margin": Num(m.Margin*100, 0), "revenue": Money(m.Revenue, ""),
					"expenses": Money(m.Expenses, ""), "time_cost": Money(m.TimeCost, "")},
				Sources: []string{kimaiKey}})
		}
		return found
	})
}

// unbooked finds past appointments naming a Kimai customer or project on
// a day without a booking for that customer.
func unbooked(cal *sources.CalendarResult, kimai *sources.KimaiDataset, today time.Time) []Finding {
	type target struct {
		name       string
		customerID int64
	}
	var targets []target
	for _, c := range kimai.Customers {
		targets = append(targets, target{c.Name, c.ID})
	}
	for _, p := range kimai.Projects {
		targets = append(targets, target{p.Name, p.CustomerID})
	}
	bookedDays := booked(kimai)
	since := today.AddDate(0, 0, -unbookedDays)

	var found []Finding
	for _, e := range cal.Events {
		if e.AllDay || e.Start.Before(since) || !e.Start.Before(today) {
			continue
		}
		title := strings.ToLower(e.Title)
		for _, t := range targets {
			if len(t.name) < minMatchLen || !strings.Contains(title, strings.ToLower(t.name)) {
				continue
			}
			day := metrics.Today(e.Start)
			if bookedDays[[2]any{day, t.customerID}] {
				break
			}
			found = append(found, Finding{Fingerprint: fmt.Sprintf("unbooked:%s:%s", day.Format(time.DateOnly), title),
				Rule: "calendar.unbooked", Severity: enums.SeverityInfo, Message: "calendar.unbooked",
				Params:  map[string]any{"title": e.Title, "day": Day(day)},
				Sources: []string{string(enums.ServiceCalendar), string(enums.ServiceKimai)}})
			break
		}
	}
	return found
}

// minMatchLen keeps short names ("IT") from matching every title.
const minMatchLen = 4
