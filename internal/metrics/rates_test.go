package metrics

import (
	"testing"
	"time"

	"andon/internal/sources"
)

func TestEffectiveRates(t *testing.T) {
	today := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	kimai := &sources.KimaiDataset{
		Customers: []sources.KimaiCustomer{{ID: 1, Name: "Muster GmbH"}, {ID: 2, Name: "Nur Kimai"}},
		Timesheets: []sources.KimaiSheet{
			{Begin: "2026-09-01T09:00:00Z", Minutes: 600, CustomerID: 1},
			{Begin: "2026-08-01T09:00:00Z", Minutes: 600, CustomerID: 1},
			{Begin: "2024-01-01T09:00:00Z", Minutes: 6000, CustomerID: 1},
			{Begin: "2026-09-02T09:00:00Z", Minutes: 60, CustomerID: 2},
		},
	}
	ninja := &sources.NinjaDataset{
		Clients: []sources.NinjaClient{{ID: 7, Name: "muster gmbh "}},
		Invoices: []sources.NinjaInvoice{
			{ClientID: 7, Status: "paid", Date: "2026-09-10", Net: 1500},
			{ClientID: 7, Status: "draft", Date: "2026-09-11", Net: 9999},
		},
	}
	rows, overall := EffectiveRates(kimai, ninja, today)
	if len(rows) != 1 || rows[0].Hours != 20 || rows[0].Net != 1500 || rows[0].Rate != 75 {
		t.Fatalf("unexpected rows: %+v", rows)
	}
	if overall != 75 {
		t.Fatalf("overall rate: %v", overall)
	}
}
