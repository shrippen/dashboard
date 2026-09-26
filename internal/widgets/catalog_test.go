package widgets

import (
	"testing"
	"time"

	"andon/internal/metrics"
	"andon/internal/sources"
)

// Monday 2026-09-21 … Sunday 2026-09-27; today is Saturday.
const catalogToday = "2026-09-26"

func sheet(day string, minutes int, customer int64) sources.KimaiSheet {
	return sources.KimaiSheet{Begin: day + "T09:00:00", Minutes: minutes, CustomerID: customer}
}

func TestKimaiWeekAgainstDailyTarget(t *testing.T) {
	data := &sources.KimaiDataset{Timesheets: []sources.KimaiSheet{
		sheet("2026-09-21", 390, 1), sheet("2026-09-22", 540, 1), sheet("2026-09-20", 600, 1), // Sunday before: not this week
	}}
	v := kimaiWeekView(KimaiWeekConfig{WeekHours: 40}, map[string]any{"data": data}, ViewCtx{Today: catalogToday})
	days := v["Days"].([]DayCol)
	if days[0].Tier != "yellow" || days[1].Tier != "" || days[1].H != 100 || v["Total"] != "15:30" || v["TargetPct"] != 89 {
		t.Fatalf("view: %+v", v)
	}
}

func TestKimaiSplitGroupsSmallCustomers(t *testing.T) {
	var sheets []sources.KimaiSheet
	for c := int64(1); c <= 6; c++ {
		sheets = append(sheets, sheet("2026-09-22", int(70-c*10), c))
	}
	data := &sources.KimaiDataset{Timesheets: sheets, Customers: []sources.KimaiCustomer{{ID: 1, Name: "Acme"}}}
	v := kimaiSplitView(nil, map[string]any{"data": data}, ViewCtx{Today: catalogToday})
	legend := v["Legend"].([]SplitLegend)
	if len(legend) != 5 || legend[0].Name != "Acme" || legend[4].Name != "" || legend[4].Hours != "0:30" {
		t.Fatalf("legend: %+v", legend)
	}
	if segs := v["Days"].([]DayCol)[1].Segs; len(segs) != 5 {
		t.Fatalf("segs: %+v", segs)
	}
}

func TestExpirySoonestFirst(t *testing.T) {
	today, _ := time.Parse(time.DateOnly, catalogToday)
	certs := &sources.CertDataset{Certs: []sources.Cert{{Host: "a", NotAfter: today.AddDate(0, 0, 40)}, {Host: "b", NotAfter: today.AddDate(0, 0, 10)}}}
	doms := &sources.DomainsDataset{Domains: []sources.DomainInfo{{Name: "c.org", Expires: today.AddDate(0, 0, 20)}}}
	v := expiryView(nil, map[string]any{"data": certs, peerDomains: doms}, ViewCtx{Today: catalogToday})
	bars := v["Bars"].([]HBar)
	if bars[0].Label != "b" || bars[0].Tier != "red" || bars[1].Tier != "yellow" || bars[2].Tier != "" || bars[2].Value != "40" {
		t.Fatalf("bars: %+v", bars)
	}
}

func TestSpeedHistoryCountsSlowDays(t *testing.T) {
	day := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	h := &metrics.History{Series: map[string][]metrics.Point{metrics.SampleKey("speedtest", "down"): {
		{Day: day, Value: 250}, {Day: day.AddDate(0, 0, 1), Value: 120}, {Day: day.AddDate(0, 0, 2), Value: 240},
	}}}
	v := speedHistoryView(nil, map[string]any{HistorySlot: h, "data": &sources.SpeedtestDataset{ExpectDown: 250}}, ViewCtx{})
	if v["Slow"] != 1 || v["ExpectPct"] != 100 {
		t.Fatalf("view: %+v", v)
	}
}

func TestVaultwardenCountsActiveOnly(t *testing.T) {
	data := &sources.VaultwardenDataset{Users: []sources.VaultUser{
		{Email: "a", Enabled: true, TwoFactor: true}, {Email: "b", Enabled: true}, {Email: "c", Enabled: false},
	}}
	v := vaultwardenView(nil, map[string]any{"data": data}, ViewCtx{})
	if v["With"] != 1 || v["Active"] != 2 || v["WithPct"] != 50 {
		t.Fatalf("view: %+v", v)
	}
}
