package metrics_test

import (
	"testing"
	"time"

	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

func day(s string) time.Time {
	t, ok := metrics.ParseDay(s)
	if !ok {
		panic("bad date: " + s)
	}
	return t
}

// ── dates ──

func TestAddMonths(t *testing.T) {
	got := metrics.AddMonths(day("2026-03-15"), -2)
	if got.Format("2006-01-02") != "2026-01-01" {
		t.Fatalf("expected 2026-01-01, got %s", got.Format("2006-01-02"))
	}
}

func TestWeekStartIsMonday(t *testing.T) {
	// 2026-03-05 is a Thursday.
	got := metrics.WeekStart(day("2026-03-05"))
	if got.Weekday() != time.Monday || got.Format("2006-01-02") != "2026-03-02" {
		t.Fatalf("expected Monday 2026-03-02, got %s (%s)", got.Format("2006-01-02"), got.Weekday())
	}
}

func TestQuarterStart(t *testing.T) {
	if got := metrics.QuarterStart(day("2026-08-15")); got.Format("2006-01-02") != "2026-07-01" {
		t.Fatalf("expected Q3 start 2026-07-01, got %s", got.Format("2006-01-02"))
	}
}

func TestWorkdaysExcludesWeekendsAndFree(t *testing.T) {
	free := map[time.Time]bool{day("2026-03-04"): true} // Wednesday
	got := metrics.Workdays(day("2026-03-02"), day("2026-03-06"), free)
	if len(got) != 4 { // Mon, Tue, Thu, Fri (Wed excluded)
		t.Fatalf("expected 4 workdays, got %d: %v", len(got), got)
	}
}

// ── kimai ──

func kimaiData() *sources.KimaiDataset {
	return &sources.KimaiDataset{
		Timesheets: []sources.KimaiSheet{
			{ID: 1, Begin: "2026-03-05T09:00:00", Minutes: 120, Rate: 150, Billable: true, CustomerID: 1},
			{ID: 2, Begin: "2026-03-06T09:00:00", Minutes: 60, Rate: 0, Billable: false, CustomerID: 1},
		},
		Customers: []sources.KimaiCustomer{{ID: 1, Name: "Acme"}},
	}
}

func TestKimaiMinutesBetweenBillableOnly(t *testing.T) {
	data := kimaiData()
	all := metrics.KimaiMinutesBetween(data, day("2026-03-01"), day("2026-03-31"), metrics.HoursAll)
	billable := metrics.KimaiMinutesBetween(data, day("2026-03-01"), day("2026-03-31"), metrics.HoursBillable)
	if all != 180 {
		t.Fatalf("expected 180 total minutes, got %d", all)
	}
	if billable != 120 {
		t.Fatalf("expected 120 billable minutes, got %d", billable)
	}
}

func TestKimaiUnbilledGroupsByCustomer(t *testing.T) {
	data := &sources.KimaiDataset{
		Timesheets: []sources.KimaiSheet{
			{ID: 1, Begin: "2026-01-01T09:00:00", End: "2026-01-01T11:00:00", Minutes: 120, Rate: 150,
				Billable: true, Exported: false, CustomerID: 1},
		},
		Customers: []sources.KimaiCustomer{{ID: 1, Name: "Acme"}},
	}
	groups := metrics.KimaiUnbilled(data, day("2026-03-01"))
	if len(groups) != 1 || groups[0].Customer != "Acme" || groups[0].Minutes != 120 {
		t.Fatalf("unexpected groups: %+v", groups)
	}
	if groups[0].AgeDays != 59 { // Jan 1 to Mar 1, 2026 (non-leap)
		t.Fatalf("expected 59 days age, got %d", groups[0].AgeDays)
	}
}

// ── ninja ──

func ninjaData() *sources.NinjaDataset {
	return &sources.NinjaDataset{
		Currency: "EUR",
		Invoices: []sources.NinjaInvoice{
			{ID: 1, ClientID: 1, Status: "paid", Date: "2026-01-15", Net: 1000, Taxes: 190, Amount: 1190},
			{ID: 2, ClientID: 1, Status: "sent", Date: "2026-02-01", DueDate: "2026-02-15", Balance: 500, Net: 420, Amount: 500},
		},
		Payments: []sources.NinjaPayment{{ID: 1, ClientID: 1, Date: "2026-01-20", Amount: 1190}},
		Clients:  []sources.NinjaClient{{ID: 1, Name: "Acme"}},
	}
}

func TestNinjaRevenueIsNetOnly(t *testing.T) {
	got := metrics.NinjaRevenue(ninjaData(), day("2026-01-01"), day("2026-01-31"))
	if got != 1000 {
		t.Fatalf("expected 1000 net revenue, got %v", got)
	}
}

func TestNinjaOpenInvoicesComputesOverdue(t *testing.T) {
	open := metrics.NinjaOpenInvoices(ninjaData(), day("2026-03-01"))
	if len(open) != 1 || open[0].Client != "Acme" {
		t.Fatalf("unexpected open invoices: %+v", open)
	}
	if open[0].OverdueDays != 14 { // due 2026-02-15, today 2026-03-01
		t.Fatalf("expected 14 overdue days, got %d", open[0].OverdueDays)
	}
}

func TestNinjaOutputVATSollUsesInvoiceDate(t *testing.T) {
	got := metrics.NinjaOutputVAT(ninjaData(), day("2026-01-01"), day("2026-01-31"), "soll")
	if got != 190 {
		t.Fatalf("expected 190 (invoice taxes in January), got %v", got)
	}
}

func TestNinjaOutputVATIstUsesPaymentShare(t *testing.T) {
	// Payment of 1190 in January; client's ratio taxes/amount across all
	// their counted invoices is (190+0)/(1190+500) = 190/1690.
	got := metrics.NinjaOutputVAT(ninjaData(), day("2026-01-01"), day("2026-01-31"), "ist")
	want := 1190.0 * (190.0 / 1690.0)
	if diff := got - round2Test(want); diff > 0.01 || diff < -0.01 {
		t.Fatalf("expected ~%v, got %v", round2Test(want), got)
	}
}

func round2Test(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}

func TestNinjaSharesLargestFirst(t *testing.T) {
	data := &sources.NinjaDataset{
		Invoices: []sources.NinjaInvoice{
			{ClientID: 1, Status: "paid", Date: "2026-01-01", Net: 100},
			{ClientID: 2, Status: "paid", Date: "2026-01-01", Net: 900},
		},
		Clients: []sources.NinjaClient{{ID: 1, Name: "Small"}, {ID: 2, Name: "Big"}},
	}
	shares := metrics.NinjaShares(data, day("2026-06-01"))
	if len(shares) != 2 || shares[0].Client != "Big" {
		t.Fatalf("expected Big client first, got %+v", shares)
	}
	if shares[0].Share != 0.9 {
		t.Fatalf("expected 90%% share, got %v", shares[0].Share)
	}
}

// ── snipe ──

func TestSnipeUpcomingWithinHorizon(t *testing.T) {
	data := &sources.SnipeDataset{
		Assets: []sources.SnipeAsset{
			{ID: 1, Name: "Laptop", WarrantyExpires: "2026-03-20"}, // 19 days out from 2026-03-01
			{ID: 2, Name: "Old", WarrantyExpires: "2020-01-01"},    // in the past, excluded
		},
	}
	got := metrics.SnipeUpcomingDates(data, day("2026-03-01"), 30)
	if len(got) != 1 || got[0].Name != "Laptop" {
		t.Fatalf("expected 1 upcoming item, got %+v", got)
	}
}

// ── travel ──

func TestDistanceKMBerlinHamburg(t *testing.T) {
	// Berlin (52.52, 13.405) to Hamburg (53.5511, 9.9937) is ~255 km.
	got := metrics.DistanceKM(52.52, 13.405, 53.5511, 9.9937)
	if got < 250 || got > 260 {
		t.Fatalf("expected ~255km, got %v", got)
	}
}

func TestTripsAppliesRoadFactorRoundTrip(t *testing.T) {
	data := &sources.DawarichDataset{
		Areas: []sources.DawarichArea{
			{ID: 1, Name: "Home", Lat: 52.5, Lon: 13.4, Radius: 100},
			{ID: 2, Name: "Client", Lat: 52.6, Lon: 13.5, Radius: 100},
		},
		Visits: []sources.DawarichVisit{
			{ID: 1, AreaID: 2, Start: "2026-03-05T09:00:00Z", End: "2026-03-05T11:00:00Z", Minutes: 120},
		},
	}
	mapping := map[string]metrics.AreaMapping{
		"Home": {Home: true}, "Client": {CustomerID: 1},
	}
	trips := metrics.Trips(data, mapping, day("2026-03-01"), day("2026-03-31"))
	if len(trips) != 1 {
		t.Fatalf("expected 1 trip, got %d", len(trips))
	}
	oneWay := metrics.DistanceKM(52.5, 13.4, 52.6, 13.5)
	want := round1Test(2 * oneWay * 1.3)
	if trips[0].KM != want {
		t.Fatalf("expected round-trip km %v, got %v", want, trips[0].KM)
	}
	if trips[0].AwayMin != 120 {
		t.Fatalf("expected 120 away minutes, got %d", trips[0].AwayMin)
	}
}

func round1Test(f float64) float64 {
	return float64(int64(f*10+0.5)) / 10
}

// ── deadlines ──

func TestUpcomingDeadlinesMonthlyVAT(t *testing.T) {
	tax := metrics.TaxSettings{VATReturnInterval: "monthly"}
	items := metrics.UpcomingDeadlines(tax, day("2026-03-01"), 14)
	found := false
	for _, i := range items {
		if i.Kind == "vat_return" && i.Due.Format("2006-01-02") == "2026-03-10" {
			found = true
			if i.Period != "02/2026" {
				t.Fatalf("expected period 02/2026, got %q", i.Period)
			}
		}
	}
	if !found {
		t.Fatalf("expected a VAT return due 2026-03-10, got %+v", items)
	}
}

func TestUpcomingDeadlinesQuarterlyLabel(t *testing.T) {
	tax := metrics.TaxSettings{VATReturnInterval: "quarterly"}
	items := metrics.UpcomingDeadlines(tax, day("2026-04-01"), 14)
	found := false
	for _, i := range items {
		if i.Kind == "vat_return" {
			found = true
			if i.Period != "Q1/2026" {
				t.Fatalf("expected Q1/2026, got %q", i.Period)
			}
		}
	}
	if !found {
		t.Fatalf("expected a quarterly VAT return, got %+v", items)
	}
}

func TestUpcomingDeadlinesAnnual(t *testing.T) {
	tax := metrics.TaxSettings{}
	items := metrics.UpcomingDeadlines(tax, day("2026-07-20"), 14)
	found := false
	for _, i := range items {
		if i.Kind == "annual" && i.Year == 2025 {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected annual deadline for 2025, got %+v", items)
	}
}
