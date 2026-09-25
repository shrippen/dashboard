package metrics

import (
	"testing"
	"time"

	"dashboard/internal/sources"
)

func TestNinjaSeasonal(t *testing.T) {
	today := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	data := &sources.NinjaDataset{Invoices: []sources.NinjaInvoice{
		{Status: "paid", Date: "2024-09-05", Net: 100},
		{Status: "paid", Date: "2025-09-05", Net: 300},
		{Status: "paid", Date: "2026-09-05", Net: 500},
	}}

	// 2023 has no invoices yet, so September averages 2024 and 2025.
	months := NinjaSeasonal(data, today, 1)
	if len(months) != 1 || months[0].Net != 500 || months[0].Prev != 200 {
		t.Fatalf("unexpected: %+v", months)
	}
}
