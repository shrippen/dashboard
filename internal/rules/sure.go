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

	// An income in the bank that pays an open invoice (its number in the
	// booking text, else the same amount): probably not recorded yet.
	Register("cross.invoice_paid", Cross, map[string]any{"days": 90.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		sure, ok1 := env.Datasets[string(enums.ServiceSure)].(*sources.SureDataset)
		ninja, ok2 := env.Datasets[string(enums.ServiceInvoiceNinja)].(*sources.NinjaDataset)
		if !ok1 || !ok2 {
			return nil
		}
		var found []Finding
		for _, m := range metrics.PaymentMatches(sure, ninja, env.Today, cfgInt(cfg, "days")) {
			found = append(found, Finding{
				Fingerprint: fmt.Sprintf("paid:%d", m.Invoice.ID), Rule: "cross.invoice_paid", Severity: enums.SeverityWarn,
				Message: "cross.invoice_paid", Params: map[string]any{"number": m.Invoice.Number, "client": m.Invoice.Client,
					"amount": Money(m.Txn.Amount, ninja.Currency), "day": Day(m.Day), "account": m.Txn.Account},
				ActionURL: "/billing#payments", ActionLabel: "book_payment",
				Sources: sources2,
			})
		}
		return found
	})

	// Money from a known client that pays no open invoice.
	Register("cross.payment_unmatched", Cross, map[string]any{"days": 30.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		sure, ok1 := env.Datasets[string(enums.ServiceSure)].(*sources.SureDataset)
		ninja, ok2 := env.Datasets[string(enums.ServiceInvoiceNinja)].(*sources.NinjaDataset)
		if !ok1 || !ok2 {
			return nil
		}
		days := cfgInt(cfg, "days")
		matches := metrics.PaymentMatches(sure, ninja, env.Today, days)
		var found []Finding
		for _, t := range metrics.UnmatchedIncome(sure, ninja, matches, env.Today, days) {
			found = append(found, Finding{
				Fingerprint: "unmatched:" + t.ID, Rule: "cross.payment_unmatched", Severity: enums.SeverityInfo,
				Message: "cross.payment_unmatched", Params: map[string]any{"name": t.Name, "amount": Money(t.Amount, sure.Currency), "day": DayStr(t.Date)},
				Sources: sources2,
			})
		}
		return found
	})

	// Business account spending without any receipt: no expense in
	// Invoice Ninja, no invoice document in Paperless, no invoice mail.
	// Only accounts named in "accounts" count: private spending stays out.
	Register("cross.expense_unrecorded", Cross, map[string]any{"accounts": []any{}, "min_amount": 20.0, "lookback_days": 60.0, "date_window": 10.0},
		func(_ any, cfg map[string]any, env Env) []Finding {
			sure, ok1 := env.Datasets[string(enums.ServiceSure)].(*sources.SureDataset)
			ninja, ok2 := env.Datasets[string(enums.ServiceInvoiceNinja)].(*sources.NinjaDataset)
			if !ok1 || !ok2 {
				return nil
			}
			in := ReceiptInputsOf(env, cfg)
			in.Since = env.Today.AddDate(0, 0, -cfgInt(cfg, "lookback_days"))
			var found []Finding
			for _, t := range metrics.MissingReceipts(in) {
				found = append(found, Finding{
					Fingerprint: "expense:" + t.ID, Rule: "cross.expense_unrecorded", Severity: enums.SeverityInfo,
					Message: "cross.expense_unrecorded", Params: map[string]any{"name": t.Name, "amount": Money(-t.Amount, sure.Currency),
						"day": DayStr(t.Date), "account": t.Account},
					ActionURL: strings.TrimRight(ninja.URL, "/") + "/expenses/create", ActionLabel: "open_in_invoiceninja",
					Sources: sources2,
				})
			}
			return found
		})
}

// ReceiptInputsOf collects the receipt sources of a scope with the
// cross.expense_unrecorded settings; Since is left to the caller.
func ReceiptInputsOf(env Env, cfg map[string]any) metrics.ReceiptInputs {
	in := metrics.ReceiptInputs{Accounts: stringsSlice(cfg["accounts"]), MinAmount: cfgFloat(cfg, "min_amount"), Window: cfgInt(cfg, "date_window")}
	in.Sure, _ = env.Datasets[string(enums.ServiceSure)].(*sources.SureDataset)
	in.Ninja, _ = env.Datasets[string(enums.ServiceInvoiceNinja)].(*sources.NinjaDataset)
	in.Paperless, _ = env.Datasets[string(enums.ServicePaperless)].(*sources.PaperlessDataset)
	in.Mail, _ = env.Datasets[string(enums.ServiceMail)].(*sources.MailDataset)
	return in
}
