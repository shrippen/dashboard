package rules

// Sure (personal finance) and its links to Invoice Ninja:
//
//	sure                 missed recurring payments, low balances, failed
//	                     bank sync, unusually large expenses, uncategorised
//	sure <-> invoiceninja open invoice already paid (income matches amount)
//	                     business expense missing in Invoice Ninja

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

const centTolerance = 0.01

func sureData(raw any) *sources.SureDataset {
	d, _ := raw.(*sources.SureDataset)
	return d
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	return sorted[len(sorted)/2]
}

func init() {
	svc := string(enums.ServiceSure)
	link := func(data *sources.SureDataset, path string) string {
		return strings.TrimRight(data.URL, "/") + "/" + path
	}

	Register("sure.recurring_missed", svc, map[string]any{"days": 5.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data := sureData(raw)
		var found []Finding
		for _, r := range data.Recurring {
			next, ok := metrics.ParseDay(r.Next)
			if r.Status != "active" || !ok || env.Today.Sub(next).Hours()/hoursPerDay <= cfgFloat(cfg, "days") {
				continue
			}
			found = append(found, svcFinding(svc, "sure.recurring_missed", "missed:"+r.Name+":"+r.Next, "sure.missed",
				enums.SeverityWarn, link(data, "recurring_transactions"), map[string]any{"name": r.Name, "amount": Money(r.Amount, data.Currency), "day": Day(next)}))
		}
		return found
	})

	Register("sure.low_balance", svc, map[string]any{"min_amount": 200.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data := sureData(raw)
		var found []Finding
		for _, a := range data.Accounts {
			if a.Type != "depository" || a.Balance >= cfgFloat(cfg, "min_amount") {
				continue
			}
			level := enums.SeverityWarn
			if a.Balance < 0 {
				level = enums.SeverityCritical
			}
			found = append(found, svcFinding(svc, "sure.low_balance", "low:"+a.ID, "sure.low", level,
				link(data, "accounts/"+a.ID), map[string]any{"account": a.Name, "amount": Money(a.Balance, a.Currency)}))
		}
		return found
	})

	Register("sure.sync_failed", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data := sureData(raw)
		if data.SyncError == "" {
			return nil
		}
		return []Finding{svcFinding(svc, "sure.sync_failed", "sync", "sure.sync", enums.SeverityWarn, data.URL,
			map[string]any{"name": data.SyncError})}
	})

	Register("sure.uncategorized", svc, map[string]any{"min_count": 5.0, "lookback_days": 30.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data := sureData(raw)
		since := env.Today.AddDate(0, 0, -cfgInt(cfg, "lookback_days"))
		count := 0
		for _, t := range data.Transactions {
			d, ok := metrics.ParseDay(t.Date)
			if ok && !d.Before(since) && t.Category == "" {
				count++
			}
		}
		if count < cfgInt(cfg, "min_count") {
			return nil
		}
		return []Finding{svcFinding(svc, "sure.uncategorized", "uncategorized", "sure.uncategorized", enums.SeverityInfo,
			link(data, "transactions"), map[string]any{"count": count})}
	})

	// A recent expense well above what this payee usually costs, or large
	// without history: worth a second look (double booking, price rise).
	Register("sure.unusual_expense", svc, map[string]any{"min_amount": 500.0, "factor": 3.0, "days": 14.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := sureData(raw)
			history := map[string][]float64{}
			for _, t := range data.Transactions {
				if t.Amount < 0 {
					history[strings.ToLower(t.Name)] = append(history[strings.ToLower(t.Name)], -t.Amount)
				}
			}
			since := env.Today.AddDate(0, 0, -cfgInt(cfg, "days"))
			var found []Finding
			for _, t := range data.Transactions {
				d, ok := metrics.ParseDay(t.Date)
				spent := -t.Amount
				if !ok || d.Before(since) || spent < cfgFloat(cfg, "min_amount") {
					continue
				}
				past := history[strings.ToLower(t.Name)]
				if len(past) > 2 && spent < median(past)*cfgFloat(cfg, "factor") {
					continue
				}
				found = append(found, svcFinding(svc, "sure.unusual_expense", "unusual:"+t.ID, "sure.unusual", enums.SeverityInfo,
					link(data, "transactions"), map[string]any{"name": t.Name, "amount": Money(spent, data.Currency), "day": Day(d)}))
			}
			return found
		})

	registerSureCross()
}

func registerSureCross() {
	sources2 := []string{string(enums.ServiceSure), string(enums.ServiceInvoiceNinja)}

	// An income in the bank that equals an open invoice: the payment is
	// probably just not recorded in Invoice Ninja yet.
	Register("cross.invoice_paid", Cross, map[string]any{"days": 90.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		sure, ok1 := env.Datasets[string(enums.ServiceSure)].(*sources.SureDataset)
		ninja, ok2 := env.Datasets[string(enums.ServiceInvoiceNinja)].(*sources.NinjaDataset)
		if !ok1 || !ok2 {
			return nil
		}
		used := map[string]bool{}
		var found []Finding
		for _, inv := range metrics.NinjaOpenInvoices(ninja, env.Today) {
			issued, ok := metrics.ParseDay(inv.Date)
			if !ok {
				continue
			}
			for _, t := range sure.Transactions {
				paid, ok := metrics.ParseDay(t.Date)
				if used[t.ID] || t.Amount <= 0 || !ok || paid.Before(issued) || paid.Sub(issued).Hours()/hoursPerDay > cfgFloat(cfg, "days") {
					continue
				}
				if absF(t.Amount-inv.Balance) > centTolerance && absF(t.Amount-inv.Amount) > centTolerance {
					continue
				}
				used[t.ID] = true
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("paid:%d", inv.ID), Rule: "cross.invoice_paid", Severity: enums.SeverityWarn,
					Message: "cross.invoice_paid", Params: map[string]any{"number": inv.Number, "client": inv.Client,
						"amount": Money(t.Amount, ninja.Currency), "day": Day(paid), "account": t.Account},
					ActionURL: strings.TrimRight(ninja.URL, "/") + fmt.Sprintf("/invoices/%d", inv.ID), ActionLabel: "open_in_invoiceninja",
					Sources: sources2,
				})
				break
			}
		}
		return found
	})

	// Business account spending without a matching expense in Invoice
	// Ninja. Only accounts named in "accounts" count: private spending
	// stays out.
	Register("cross.expense_unrecorded", Cross, map[string]any{"accounts": []any{}, "min_amount": 20.0, "lookback_days": 60.0, "date_window": 10.0},
		func(_ any, cfg map[string]any, env Env) []Finding {
			sure, ok1 := env.Datasets[string(enums.ServiceSure)].(*sources.SureDataset)
			ninja, ok2 := env.Datasets[string(enums.ServiceInvoiceNinja)].(*sources.NinjaDataset)
			accounts := stringsSlice(cfg["accounts"])
			if !ok1 || !ok2 || len(accounts) == 0 {
				return nil
			}
			since := env.Today.AddDate(0, 0, -cfgInt(cfg, "lookback_days"))
			var found []Finding
			for _, t := range sure.Transactions {
				d, ok := metrics.ParseDay(t.Date)
				spent := -t.Amount
				if !ok || d.Before(since) || spent < cfgFloat(cfg, "min_amount") || !containsAny(strings.ToLower(t.Account), accounts) {
					continue
				}
				if expenseRecorded(ninja, spent, d, cfgInt(cfg, "date_window")) {
					continue
				}
				found = append(found, Finding{
					Fingerprint: "expense:" + t.ID, Rule: "cross.expense_unrecorded", Severity: enums.SeverityInfo,
					Message: "cross.expense_unrecorded", Params: map[string]any{"name": t.Name, "amount": Money(spent, sure.Currency),
						"day": Day(d), "account": t.Account},
					ActionURL: strings.TrimRight(ninja.URL, "/") + "/expenses/create", ActionLabel: "open_in_invoiceninja",
					Sources: sources2,
				})
			}
			return found
		})
}

// expenseRecorded: a Ninja expense of this amount (gross or net) within
// window days of day.
func expenseRecorded(ninja *sources.NinjaDataset, amount float64, day time.Time, window int) bool {
	for _, e := range ninja.Expenses {
		if absF(e.Amount-amount) > centTolerance && absF(e.Amount-e.Tax-amount) > centTolerance {
			continue
		}
		if d, ok := metrics.ParseDay(e.Date); ok && absDays(d, day) <= window {
			return true
		}
	}
	return false
}
