package metrics_test

import (
	"testing"
	"time"

	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

func TestPaymentMoraleRecentSlower(t *testing.T) {
	data := &sources.NinjaDataset{Clients: []sources.NinjaClient{{ID: 1, Name: "Acme"}}}
	// Four invoices paid after 10 days, the last three after 30.
	for i, gap := range []int{10, 10, 10, 30, 30, 30} {
		issued := time.Date(2026, time.Month(i+1), 1, 0, 0, 0, 0, time.UTC)
		data.Invoices = append(data.Invoices, sources.NinjaInvoice{ClientID: 1, Status: "paid", Date: issued.Format("2006-01-02")})
		data.Payments = append(data.Payments, sources.NinjaPayment{ClientID: 1, Date: issued.AddDate(0, 0, gap).Format("2006-01-02")})
	}
	rows := metrics.PaymentMorale(data, 10)
	if len(rows) != 1 || rows[0].AvgDays != 20 || rows[0].RecentDays != 30 || !rows[0].Worse {
		t.Fatalf("morale: %+v", rows)
	}
}

func TestCashflowEvents(t *testing.T) {
	today := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	ninja := &sources.NinjaDataset{Invoices: []sources.NinjaInvoice{
		{Number: "7", ClientID: 1, Status: "sent", Date: "2026-09-20", Balance: 1000},
	}}
	sure := &sources.SureDataset{Accounts: []sources.SureAccount{{Type: "depository", Balance: 500}},
		Recurring: []sources.SureRecurring{{Name: "Miete", Status: "active", Expense: true, Amount: 800, Next: "2026-10-01"}}}
	points, events := metrics.Cashflow(metrics.CashInputs{Ninja: ninja, Sure: sure}, today, 30)

	// −800 on 1 Oct, +1000 on 4 Oct (invoice date + 14 days default terms).
	if len(events) != 2 || events[0].Amount != -800 || events[1].Day != time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("events: %+v", events)
	}
	low := points[0].Balance
	for _, p := range points {
		low = min(low, p.Balance)
	}
	if points[0].Balance != 500 || low != -300 || points[len(points)-1].Balance != 700 {
		t.Fatalf("points: start %v low %v end %v", points[0].Balance, low, points[len(points)-1].Balance)
	}
}
