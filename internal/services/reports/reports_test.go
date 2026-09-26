package reports_test

import (
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"dashboard/internal/metrics"
	"dashboard/internal/services/reports"
)

// The CSV opens in German Excel: BOM, semicolons, decimal commas, one
// row per day and outage.
func TestISPCSVForExcel(t *testing.T) {
	day := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	report := reports.ISP{Space: "Zuhause", ExpectDown: 250, SpeedReport: metrics.SpeedReport{
		Days:    []metrics.SpeedDay{{Day: day, Down: 212.5, Up: 40, Below: true}},
		Outages: []metrics.Outage{{Gateway: "wan1", Start: day.Add(3 * time.Hour)}},
	}}

	out, err := reports.ISPCSV([]reports.ISP{report})
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.HasPrefix(text, "\ufeff") {
		t.Fatal("missing BOM")
	}
	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\ufeff")))
	r.Comma = ';'
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows: %v", rows)
	}
	if got := rows[1]; got[1] != "2026-09-01" || got[2] != "212,5" || got[4] != "250,0" || got[5] != "ja" {
		t.Fatalf("day row: %v", got)
	}
	if got := rows[2]; got[2] != "WAN-Ausfall wan1" || got[3] != "bis " {
		t.Fatalf("outage row: %v", got)
	}
}
