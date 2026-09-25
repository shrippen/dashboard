package rules

import (
	"fmt"
	"strings"

	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

const (
	ninjaSource     = "invoiceninja"
	ninjaOpen       = "open_in_invoiceninja"
	goalFromMonth   = 3
	ninjaDaysPerYr  = 365
	ninjaRecentDays = 90
)

func ninjaURL(data *sources.NinjaDataset, path string) string {
	return strings.TrimRight(data.URL, "/") + "/#/" + path
}

func ninjaCurrency(data *sources.NinjaDataset) string {
	if data.Currency == "" {
		return "EUR"
	}
	return data.Currency
}

func ninjaClientMap(data *sources.NinjaDataset) map[int64]sources.NinjaClient {
	out := make(map[int64]sources.NinjaClient, len(data.Clients))
	for _, c := range data.Clients {
		out[c.ID] = c
	}
	return out
}

func clientName(clients map[int64]sources.NinjaClient, id int64) string {
	if c, ok := clients[id]; ok {
		return c.Name
	}
	return "?"
}

func nData(data any) *sources.NinjaDataset {
	d, _ := data.(*sources.NinjaDataset)
	return d
}

func init() {
	Register("in.invoice_overdue", string(enums.ServiceInvoiceNinja), map[string]any{"dunning_after_days": 14.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := nData(raw)
			var found []Finding
			for _, inv := range metrics.NinjaOpenInvoices(data, env.Today) {
				if inv.OverdueDays <= 0 {
					continue
				}
				dunning := inv.OverdueDays >= cfgInt(cfg, "dunning_after_days")
				level, msg := enums.SeverityWarn, "in.overdue"
				if dunning {
					level, msg = enums.SeverityCritical, "in.overdue_dunning"
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("overdue:%d", inv.ID), Rule: "in.invoice_overdue",
					Severity: level, Message: msg,
					Params: map[string]any{
						"number": inv.Number, "client": inv.Client, "days": inv.OverdueDays,
						"amount": Money(inv.Balance, ninjaCurrency(data)),
					},
					ActionURL: ninjaURL(data, fmt.Sprintf("invoices/%d/edit", inv.ID)), ActionLabel: ninjaOpen,
					Sources: []string{ninjaSource},
				})
			}
			return found
		})

	Register("in.slow_payer", string(enums.ServiceInvoiceNinja), map[string]any{"days": 30.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := nData(raw)
			clients := ninjaClientMap(data)
			var found []Finding
			for cid, avg := range metrics.NinjaPaymentDays(data) {
				if avg <= cfgInt(cfg, "days") {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("slow:%d", cid), Rule: "in.slow_payer",
					Severity: enums.SeverityInfo, Message: "in.slow_payer",
					Params:  map[string]any{"client": clientName(clients, cid), "days": avg, "limit": cfgInt(cfg, "days")},
					Sources: []string{ninjaSource},
				})
			}
			return found
		})

	Register("in.draft_stale", string(enums.ServiceInvoiceNinja), map[string]any{"days": 7.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := nData(raw)
			clients := ninjaClientMap(data)
			var found []Finding
			for _, inv := range data.Invoices {
				created, ok := metrics.ParseDay(inv.Date)
				if inv.Status != "draft" || !ok {
					continue
				}
				age := int(env.Today.Sub(created).Hours() / 24)
				if age < cfgInt(cfg, "days") {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("draft:%d", inv.ID), Rule: "in.draft_stale",
					Severity: enums.SeverityInfo, Message: "in.draft_stale",
					Params: map[string]any{
						"client": clientName(clients, inv.ClientID), "days": age,
						"amount": Money(inv.Amount, ninjaCurrency(data)),
					},
					ActionURL: ninjaURL(data, fmt.Sprintf("invoices/%d/edit", inv.ID)), ActionLabel: ninjaOpen,
					Sources: []string{ninjaSource},
				})
			}
			return found
		})

	Register("in.quote_open", string(enums.ServiceInvoiceNinja), map[string]any{"days": 14.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := nData(raw)
			clients := ninjaClientMap(data)
			var found []Finding
			for _, q := range data.Quotes {
				sent, ok := metrics.ParseDay(q.Date)
				if q.Status != "sent" || !ok {
					continue
				}
				age := int(env.Today.Sub(sent).Hours() / 24)
				if age < cfgInt(cfg, "days") {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("quote:%d", q.ID), Rule: "in.quote_open",
					Severity: enums.SeverityInfo, Message: "in.quote_open",
					Params:    map[string]any{"number": q.Number, "client": clientName(clients, q.ClientID), "days": age},
					ActionURL: ninjaURL(data, fmt.Sprintf("quotes/%d/edit", q.ID)), ActionLabel: ninjaOpen,
					Sources: []string{ninjaSource},
				})
			}
			return found
		})

	Register("in.recurring_ending", string(enums.ServiceInvoiceNinja), map[string]any{"days": 30.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := nData(raw)
			clients := ninjaClientMap(data)
			var found []Finding
			for _, r := range data.Recurring {
				send, ok := metrics.ParseDay(r.NextSendDate)
				if !r.Active || !ok {
					continue
				}
				if r.RemainingCycles == 0 || r.RemainingCycles == -1 || r.RemainingCycles > 1 {
					continue
				}
				if int(send.Sub(env.Today).Hours()/24) > cfgInt(cfg, "days") {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("recurring:%d", r.ID), Rule: "in.recurring_ending",
					Severity: enums.SeverityInfo, Message: "in.recurring_ending",
					Params:      map[string]any{"client": clientName(clients, r.ClientID), "day": Day(send)},
					ActionURL:   ninjaURL(data, fmt.Sprintf("recurring_invoices/%d/edit", r.ID)),
					ActionLabel: ninjaOpen, Due: send.Format("2006-01-02"), Sources: []string{ninjaSource},
				})
			}
			return found
		})

	Register("in.revenue_vs_goal", string(enums.ServiceInvoiceNinja), nil,
		func(raw any, cfg map[string]any, env Env) []Finding {
			goals, _ := env.Settings["goals"].(map[string]any)
			goal, _ := goals["revenue_year"].(float64)
			if goal == 0 || int(env.Today.Month()) < goalFromMonth {
				return nil
			}
			data := nData(raw)
			summary := metrics.NinjaSummaryOf(data, env.Today, "", "")
			expected := goal * float64(env.Today.YearDay()) / ninjaDaysPerYr
			if summary.RevenueYTD >= expected {
				return nil
			}
			return []Finding{{
				Fingerprint: fmt.Sprintf("goal:%d", env.Today.Year()), Rule: "in.revenue_vs_goal",
				Severity: enums.SeverityInfo, Message: "in.revenue_goal",
				Params: map[string]any{
					"ytd": Money(summary.RevenueYTD, ninjaCurrency(data)), "expected": Money(expected, ninjaCurrency(data)),
					"goal": Money(goal, ninjaCurrency(data)),
				},
				Sources: []string{ninjaSource},
			}}
		})

	Register("in.missing_vat", string(enums.ServiceInvoiceNinja), map[string]any{"days": 90.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := nData(raw)
			clients := ninjaClientMap(data)
			home := data.HomeCountryID
			if home == "" {
				home = "276"
			}
			var found []Finding
			for _, inv := range metrics.NinjaCounted(data) {
				d, ok := metrics.ParseDay(inv.Date)
				if !ok || int(env.Today.Sub(d).Hours()/24) > cfgInt(cfg, "days") || inv.Taxes != 0 {
					continue
				}
				client := clients[inv.ClientID]
				country := client.CountryID
				if country == "" {
					country = home
				}
				domestic := country == home
				if !domestic && client.VATNumber != "" {
					continue
				}
				msg := "in.missing_vat_eu"
				if domestic {
					msg = "in.missing_vat_domestic"
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("vat:%d", inv.ID), Rule: "in.missing_vat",
					Severity: enums.SeverityWarn, Message: msg,
					Params:    map[string]any{"number": inv.Number, "client": clientNameOr(client.Name)},
					ActionURL: ninjaURL(data, fmt.Sprintf("invoices/%d/edit", inv.ID)), ActionLabel: ninjaOpen,
					Sources: []string{ninjaSource},
				})
			}
			return found
		})

	Register("in.expense_no_input_vat", string(enums.ServiceInvoiceNinja), map[string]any{"min_amount": 50.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := nData(raw)
			since := env.Today.AddDate(0, 0, -ninjaRecentDays)
			var found []Finding
			for _, e := range data.Expenses {
				d, ok := metrics.ParseDay(e.Date)
				if e.Tax != 0 || e.Amount < cfgFloat(cfg, "min_amount") || !ok || d.Before(since) {
					continue
				}
				notes := e.Notes
				if notes == "" {
					notes = "–"
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("expense:%d", e.ID), Rule: "in.expense_no_input_vat",
					Severity: enums.SeverityInfo, Message: "in.expense_no_vat",
					Params: map[string]any{
						"amount": Money(e.Amount, ninjaCurrency(data)), "notes": notes, "day": DayStr(e.Date),
					},
					ActionURL: ninjaURL(data, fmt.Sprintf("expenses/%d/edit", e.ID)), ActionLabel: ninjaOpen,
					Sources: []string{ninjaSource},
				})
			}
			return found
		})

	Register("in.client_concentration", string(enums.ServiceInvoiceNinja),
		map[string]any{"info_share": 0.5, "warn_share": 5.0 / 6.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := nData(raw)
			shares := metrics.NinjaShares(data, env.Today)
			if len(shares) == 0 || shares[0].Share < cfgFloat(cfg, "info_share") {
				return nil
			}
			top := shares[0]
			warn := top.Share >= cfgFloat(cfg, "warn_share")
			level, msg := enums.SeverityInfo, "in.concentration"
			if warn {
				level, msg = enums.SeverityWarn, "in.concentration_pension"
			}
			return []Finding{{
				Fingerprint: fmt.Sprintf("concentration:%d", top.ClientID), Rule: "in.client_concentration",
				Severity: level, Message: msg,
				Params:  map[string]any{"client": top.Client, "percent": Num(top.Share*100, 0)},
				Sources: []string{ninjaSource},
			}}
		})
}

func clientNameOr(name string) string {
	if name == "" {
		return "?"
	}
	return name
}
