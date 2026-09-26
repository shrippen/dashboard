package widgets

import (
	"testing"

	"dashboard/internal/sources"
)

func TestBarsOfBucketsAndScales(t *testing.T) {
	got := barsOf([]float64{10, 20, 30, 40}, 2, 40)
	if len(got) != 2 || got[0].H != 38 || got[1].H != 88 || got[1].Tier != "red" {
		t.Fatalf("bars: %+v", got)
	}
}

func TestConnHealthShowsWorstFirst(t *testing.T) {
	strips := []ConnStrip{
		{Name: "ok", FailPct: 0, Days: []ConnDayState{{OK: 3}}},
		{Name: "paperless", FailPct: 40, Days: []ConnDayState{{OK: 3, Fail: 2}, {}}},
		{Name: "kimai", FailPct: 81, Days: []ConnDayState{{Fail: 5}}},
	}
	v := connHealthView(ConnHealthConfig{Limit: 4}, map[string]any{ConnHealthSlot: strips}, ViewCtx{})
	rows := v["Rows"].([]StripRow)
	if v["Healthy"] != 1 || len(rows) != 2 || rows[0].Name != "kimai" || rows[1].Cells[0].State != "mid" || rows[1].Cells[1].State != "none" {
		t.Fatalf("view: %+v", v)
	}
}

func TestInvoiceAgingBands(t *testing.T) {
	data := &sources.NinjaDataset{Invoices: []sources.NinjaInvoice{
		{Status: "sent", Balance: 1000, DueDate: "2026-10-01"},
		{Status: "sent", Balance: 500, DueDate: "2026-09-10"},
		{Status: "sent", Balance: 500, DueDate: "2026-06-01"},
	}}
	v := invoiceAgingView(nil, map[string]any{"data": data}, ViewCtx{Today: "2026-09-26"})
	bands := v["Bands"].([]AgingBand)
	if v["Total"] != 2000.0 || bands[0].Amount != 1000 || bands[1].Amount != 500 || bands[3].Amount != 500 || bands[0].Pct != 50 {
		t.Fatalf("view: %+v", v)
	}
}
