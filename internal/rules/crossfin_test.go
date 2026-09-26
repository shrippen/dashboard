package rules_test

import (
	"fmt"
	"testing"
	"time"

	"andon/internal/enums"
	"andon/internal/rules"
	"andon/internal/sources"
)

// sheet books minutes for a customer and project on a day.
func sheet(day string, minutes int, customer, project int64, billable bool, rate float64) sources.KimaiSheet {
	return sources.KimaiSheet{Begin: day + "T09:00:00Z", End: day + "T17:00:00Z", Minutes: minutes, Rate: rate,
		Billable: billable, CustomerID: customer, ProjectID: project}
}

func crossEnv(today string, datasets map[string]any) rules.Env {
	return rules.Env{Today: day(today), Datasets: datasets, Settings: map[string]any{}}
}

func TestPaymentRulesMatchNumberAndClient(t *testing.T) {
	ninja := &sources.NinjaDataset{Currency: "EUR",
		Clients:  []sources.NinjaClient{{ID: 1, Name: "Acme GmbH"}},
		Invoices: []sources.NinjaInvoice{{ID: 7, Number: "RE-2026-041", ClientID: 1, Status: "sent", Date: "2026-09-01", Amount: 1190, Balance: 1190}}}
	sure := &sources.SureDataset{Currency: "EUR", Transactions: []sources.SureTxn{
		{ID: "a", Date: "2026-09-20", Name: "Acme GmbH RE 2026 041", Amount: 1000}, // partial payment, found by number
		{ID: "b", Date: "2026-09-21", Name: "Acme GmbH Vorschuss", Amount: 500},
	}}
	env := crossEnv("2026-09-25", map[string]any{"sure": sure, "invoiceninja": ninja})
	if got := run(t, "cross.invoice_paid", nil, env); len(got) != 1 || got[0].Params["number"] != "RE-2026-041" {
		t.Fatalf("paid: %+v", got)
	}
	if got := run(t, "cross.payment_unmatched", nil, env); len(got) != 1 || got[0].Params["name"] != "Acme GmbH Vorschuss" {
		t.Fatalf("unmatched: %+v", got)
	}
}

func TestExpenseWithPaperlessReceiptIsRecorded(t *testing.T) {
	sure := &sources.SureDataset{Currency: "EUR", Transactions: []sources.SureTxn{
		{ID: "h", Date: "2026-09-10", Name: "Hetzner", Amount: -46.41, Account: "Geschäftskonto"},
		{ID: "j", Date: "2026-09-12", Name: "JetBrains", Amount: -89, Account: "Geschäftskonto"},
	}}
	paperless := &sources.PaperlessDataset{Invoices: []sources.PaperlessDoc{{ID: 1, Title: "Hetzner", Created: "2026-09-09", Amount: 46.41}}}
	env := crossEnv("2026-09-25", map[string]any{"sure": sure, "invoiceninja": &sources.NinjaDataset{}, "paperless": paperless})
	env.Settings = map[string]any{"rules": map[string]any{"cross.expense_unrecorded": map[string]any{"accounts": []any{"geschäft"}}}}
	if got := run(t, "cross.expense_unrecorded", nil, env); len(got) != 1 || got[0].Params["name"] != "JetBrains" {
		t.Fatalf("missing receipts: %+v", got)
	}
}

func TestRateBelowCountsInternalHours(t *testing.T) {
	kimai := &sources.KimaiDataset{Customers: []sources.KimaiCustomer{{ID: 1, Name: "Acme"}}}
	for i := range 10 {
		d := fmt.Sprintf("2026-09-%02d", i+1)
		kimai.Timesheets = append(kimai.Timesheets, sheet(d, 120, 1, 1, true, 200), sheet(d, 120, 1, 1, false, 0))
	}
	ninja := &sources.NinjaDataset{Currency: "EUR", Clients: []sources.NinjaClient{{ID: 1, Name: "Acme"}},
		Invoices: []sources.NinjaInvoice{{ID: 1, ClientID: 1, Status: "paid", Date: "2026-09-15", Amount: 2000, Net: 2000}}}
	got := run(t, "in.rate_below", nil, crossEnv("2026-09-25", map[string]any{"kimai": kimai, "invoiceninja": ninja}))
	if len(got) != 1 || got[0].Params["client"] != "Acme" {
		t.Fatalf("rate: %+v", got)
	}
}

func TestSubscriptionUnusedWithDeadline(t *testing.T) {
	sure := &sources.SureDataset{Currency: "EUR", Recurring: []sources.SureRecurring{
		{Name: "Adobe Creative Cloud", Status: "active", Amount: 59.49, Expense: true, Last: "2026-08-15", Next: "2026-09-15"},
		{Name: "Nextcloud Hosting", Status: "active", Amount: 10, Expense: true},
	}}
	paperless := &sources.PaperlessDataset{Contracts: []sources.PaperlessContract{{Title: "Adobe Abo", Deadline: day("2026-11-30")}}}
	links := []rules.Link{{Title: "Adobe", URL: "https://adobe.com"}, {Title: "Nextcloud", URL: "https://cloud.lan", LastClick: day("2026-09-24")}}
	env := crossEnv("2026-09-25", map[string]any{"sure": sure, "paperless": paperless, rules.LinksDataset: links})
	got := run(t, "sure.subscription_unused", nil, env)
	if len(got) != 1 || got[0].Message != "sure.subscription_unused_deadline" {
		t.Fatalf("subscriptions: %+v", got)
	}
}

func TestCalendarAndFreeDays(t *testing.T) {
	kimai := &sources.KimaiDataset{Customers: []sources.KimaiCustomer{{ID: 1, Name: "Acme GmbH"}},
		Holidays:   []sources.KimaiHoliday{{Date: "2026-09-21", Name: "Feiertag"}},
		Timesheets: []sources.KimaiSheet{sheet("2026-09-21", 60, 1, 1, true, 100)}}
	cal := &sources.CalendarResult{Events: []sources.Event{
		{Start: time.Date(2026, 9, 22, 14, 0, 0, 0, time.UTC), Title: "Workshop Acme GmbH"},
		{Start: time.Date(2026, 9, 21, 14, 0, 0, 0, time.UTC), Title: "Call Acme GmbH"},
	}}
	env := crossEnv("2026-09-25", map[string]any{"kimai": kimai, "calendar": cal})
	if got := run(t, "calendar.unbooked", nil, env); len(got) != 1 || got[0].Params["title"] != "Workshop Acme GmbH" {
		t.Fatalf("unbooked: %+v", got)
	}
	if got := run(t, "kimai.booked_free_day", kimai, env); len(got) != 1 {
		t.Fatalf("free day: %+v", got)
	}
}

func TestOrderGapWorkloadMarginSpendable(t *testing.T) {
	kimai := &sources.KimaiDataset{Customers: []sources.KimaiCustomer{{ID: 1, Name: "Acme"}},
		Projects: []sources.KimaiProject{{ID: 1, Name: "Relaunch", CustomerID: 1}}}
	for i := range 28 {
		lastYear := day("2025-09-25").AddDate(0, 0, -i).Format(time.DateOnly)
		kimai.Timesheets = append(kimai.Timesheets, sheet(lastYear, 480, 1, 1, true, 400))
		now := day("2026-09-25").AddDate(0, 0, -i).Format(time.DateOnly)
		kimai.Timesheets = append(kimai.Timesheets, sheet(now, 660, 1, 1, true, 100))
	}
	env := crossEnv("2026-09-25", map[string]any{"kimai": kimai})
	env.Settings = map[string]any{"costs": map[string]any{"hourly_cost": 30.0}}

	// 11 h every day for four weeks: long weeks, no free day, low margin.
	if got := run(t, "kimai.workload", kimai, env); len(got) != 1 || got[0].Params["weeks"] != 4 {
		t.Fatalf("workload: %+v", got)
	}
	if got := run(t, "kimai.margin_low", nil, env); len(got) != 1 {
		t.Fatalf("margin: %+v", got)
	}
	// Last year 8 h a day, now 11: no gap.
	if got := run(t, "in.order_gap", nil, env); len(got) != 0 {
		t.Fatalf("order gap: %+v", got)
	}

	sure := &sources.SureDataset{Currency: "EUR", Accounts: []sources.SureAccount{{Name: "Giro", Type: "depository", Classification: "asset", Balance: 1000}}}
	ninja := &sources.NinjaDataset{Currency: "EUR", Invoices: []sources.NinjaInvoice{{ID: 1, Status: "paid", Date: "2026-03-01", Amount: 11900, Taxes: 1900, Net: 10000}}}
	got := run(t, "sure.spendable_negative", nil, crossEnv("2026-09-25", map[string]any{"sure": sure, "invoiceninja": ninja}))
	if len(got) != 1 || got[0].Severity != enums.SeverityWarn {
		t.Fatalf("spendable: %+v", got)
	}
}

func TestBudgetRunsOutBeforeEnd(t *testing.T) {
	kimai := &sources.KimaiDataset{Projects: []sources.KimaiProject{{ID: 1, Name: "Relaunch", Budget: 10000, UsedMoney: 8000, End: "2026-12-31"}}}
	for i := range 28 {
		kimai.Timesheets = append(kimai.Timesheets, sheet(day("2026-09-25").AddDate(0, 0, -i).Format(time.DateOnly), 60, 1, 1, true, 100))
	}
	got := run(t, "kimai.budget_pace", kimai, crossEnv("2026-09-25", nil))
	if len(got) != 1 || got[0].Message != "kimai.runout" {
		t.Fatalf("budget: %+v", got)
	}
}
