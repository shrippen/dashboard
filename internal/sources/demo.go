package sources

import (
	"math/rand"
	"time"
)

// Generated demo datasets for demo:// connections, relative to today and
// deterministic. They fit together so every rule fires once:
//
//	Muster GmbH   visited on demoSkipDay without a Kimai entry, unbilled hours, overdue invoice
//	Beispiel AG   budget at 85 %, quote without reaction
//	Nordlicht     EU client, invoice at 0 % without VAT id

const (
	demoSeed         = 7
	demoRate         = 95.0
	demoVAT          = 0.19
	demoSkipDay      = 3
	demoUnbilledDays = 40
	demoHistoryDays  = 600
	demoClientWindow = 25
	demoRunningHours = 11
	demoInvoiceMonth = 20
	demoHomeCountry  = "276"
	demoEUCountry    = "40"
)

var (
	demoClientDays = []int{1, 3, 8, 10, 15}
	demoHome       = [2]float64{52.5200, 13.4050}
	demoSite       = [2]float64{52.3906, 13.0645}
	demoCustomers  = []KimaiCustomer{{1, "Muster GmbH"}, {2, "Beispiel AG"}, {3, "Nordlicht e.V."}}
)

func demoDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func iso(t time.Time) string { return t.Format(time.DateOnly) }

func stamp(d time.Time, hour, minute int) string {
	return time.Date(d.Year(), d.Month(), d.Day(), hour, minute, 0, 0, time.UTC).Format(time.RFC3339)
}

// weekdaysBack lists the weekdays before today, newest first.
func weekdaysBack(today time.Time, days int) []time.Time {
	var found []time.Time
	for back := 1; back <= days; back++ {
		d := today.AddDate(0, 0, -back)
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			found = append(found, d)
		}
	}
	return found
}

func clientDays(today time.Time) []time.Time {
	days := weekdaysBack(today, demoClientWindow)
	limit := demoClientDays[len(demoClientDays)-1] + 1
	if len(days) > limit {
		days = days[:limit]
	}
	return days
}

// DemoKimai is the demo Kimai dataset.
func DemoKimai(now time.Time) *KimaiDataset {
	today := demoDay(now)
	rnd := rand.New(rand.NewSource(demoSeed))
	clients := clientDays(today)
	visits := map[time.Time]bool{}
	for _, i := range demoClientDays {
		if i < len(clients) {
			visits[clients[i]] = true
		}
	}
	skip := clients[demoSkipDay]

	history := weekdaysBack(today, demoHistoryDays)
	sheets := make([]KimaiSheet, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		d := history[i]
		customer := []int64{1, 2, 2, 3}[rnd.Intn(4)]
		if visits[d] {
			customer = 1
		}
		if d.Equal(skip) {
			customer = 2
		}
		hours := 5 + rnd.Intn(4)
		age := int(today.Sub(d).Hours() / 24)
		activity := "Entwicklung"
		if visits[d] {
			activity = "vor Ort"
		}
		sheets = append(sheets, KimaiSheet{
			ID: int64(len(sheets) + 1), Begin: stamp(d, 9, 0), End: stamp(d, 9+hours, 0),
			Minutes: hours * 60, Rate: float64(hours) * demoRate, Billable: true,
			Exported:  !(customer == 1 && age <= demoUnbilledDays) && age > 5,
			ProjectID: customer, CustomerID: customer, Activity: activity, UserID: 1,
		})
	}

	running := now.UTC().Add(-demoRunningHours * time.Hour)
	return &KimaiDataset{
		URL:        "https://kimai.demo",
		Timesheets: sheets,
		Active: []KimaiSheet{{ID: 9999, Begin: running.Format(time.RFC3339), Billable: true,
			ProjectID: 2, CustomerID: 2, Activity: "Entwicklung", UserID: 1}},
		Projects: []KimaiProject{
			{ID: 1, Name: "Wartung", CustomerID: 1},
			{ID: 2, Name: "Relaunch", CustomerID: 2, Budget: 40000, End: iso(today.AddDate(0, 0, 60)), UsedMoney: 34000},
			{ID: 3, Name: "Mitgliederportal", CustomerID: 3, TimeBudgetMin: 40 * 60, BudgetType: "month"},
		},
		Customers: demoCustomers,
		Absences: []KimaiAbsence{{Start: iso(today.AddDate(0, 0, 20)), End: iso(today.AddDate(0, 0, 24)),
			Type: "holiday", Status: "approved"}},
		Holidays:      []KimaiHoliday{{Date: time.Date(today.Year(), time.December, 25, 0, 0, 0, 0, time.UTC).Format(time.DateOnly), Name: "1. Weihnachtstag"}},
		HolidayBundle: true,
	}
}

// DemoNinja is the demo Invoice Ninja dataset.
func DemoNinja(now time.Time) *NinjaDataset {
	today := demoDay(now)
	rnd := rand.New(rand.NewSource(demoSeed))
	var invoices []NinjaInvoice
	var payments []NinjaPayment
	number := int64(1)

	for back := demoInvoiceMonth; back > 0; back-- {
		start := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -back, 0)
		for _, c := range demoCustomers {
			net := round2(2500 + rnd.Float64()*4000)
			tax := round2(net * demoVAT)
			if c.ID == 3 && back == 1 {
				tax = 0
			}
			issued := start.AddDate(0, 0, 2)
			invoices = append(invoices, NinjaInvoice{
				ID: number, Number: "R-" + issued.Format("2006") + "-" + pad3(number), ClientID: c.ID, Status: "paid",
				Date: iso(issued), DueDate: iso(issued.AddDate(0, 0, 14)), Amount: net + tax, Taxes: tax, Net: net,
			})
			payments = append(payments, NinjaPayment{ID: number, Date: iso(issued.AddDate(0, 0, 10)), Amount: net + tax, ClientID: c.ID})
			number++
		}
	}

	overdue := &invoices[len(invoices)-3]
	overdue.Status, overdue.Balance, overdue.DueDate = "sent", overdue.Amount, iso(today.AddDate(0, 0, -21))
	kept := payments[:0]
	for _, p := range payments {
		if p.ID != overdue.ID {
			kept = append(kept, p)
		}
	}
	invoices = append(invoices, NinjaInvoice{ID: number, ClientID: 2, Status: "draft", Date: iso(today.AddDate(0, 0, -10)),
		Amount: 1190, Balance: 1190, Taxes: 190, Net: 1000})

	return &NinjaDataset{
		URL: "https://invoice.demo", Currency: "EUR", Invoices: invoices, Payments: kept,
		Clients: []NinjaClient{
			{ID: 1, Name: "Muster GmbH", VATNumber: "DE123456789", CountryID: demoHomeCountry},
			{ID: 2, Name: "Beispiel AG", VATNumber: "DE987654321", CountryID: demoHomeCountry},
			{ID: 3, Name: "Nordlicht e.V.", CountryID: demoEUCountry},
		},
		Expenses: []NinjaExpense{
			{ID: 1, Date: iso(today.AddDate(0, 0, -30)), Amount: 1428, Tax: 228, Notes: "Notebook ThinkPad", VendorID: 1},
			{ID: 2, Date: iso(today.AddDate(0, 0, -12)), Amount: 59.5, Notes: "Hosting", VendorID: 2},
			{ID: 3, Date: iso(today.AddDate(0, 0, -5)), Amount: 238, Tax: 38, Notes: "Software", VendorID: 3},
		},
		Quotes: []NinjaQuote{{ID: 1, Number: "A-" + today.Format("2006") + "-004", ClientID: 2, Status: "sent",
			Date: iso(today.AddDate(0, 0, -20)), Amount: 8330}},
		Recurring: []NinjaRecurring{{ID: 1, Number: "W-01", ClientID: 1, Active: true,
			NextSendDate: iso(today.AddDate(0, 0, 12)), RemainingCycles: 1, Amount: 595}},
		HomeCountryID: demoHomeCountry,
	}
}

func pad3(n int64) string {
	s := []byte{'0', '0', '0'}
	for i := 2; i >= 0 && n > 0; i-- {
		s[i] = byte('0' + n%10)
		n /= 10
	}
	return string(s)
}

// DemoSnipe is the demo Snipe-IT dataset.
func DemoSnipe(now time.Time) *SnipeDataset {
	today := demoDay(now)
	ago := func(n int) string { return iso(today.AddDate(0, 0, -n)) }
	ahead := func(n int) string { return iso(today.AddDate(0, 0, n)) }
	return &SnipeDataset{
		URL: "https://assets.demo",
		Assets: []SnipeAsset{
			{ID: 1, Name: "ThinkPad T14", Tag: "NB-001", Model: "T14 Gen 4", Category: "Notebook", Status: "Ausgegeben",
				Deployable: true, Assigned: true, PurchaseDate: ago(30), PurchaseCost: 1200, WarrantyExpires: ahead(10),
				EOLDate: ahead(900), NextAudit: ahead(100), LastChange: ago(30)},
			{ID: 2, Name: "NAS", Tag: "SRV-002", Model: "DS920+", Category: "Server", Status: "In Betrieb",
				Deployable: true, Assigned: true, PurchaseDate: ago(1900), PurchaseCost: 650, WarrantyExpires: ago(800),
				EOLDate: ago(20), NextAudit: ago(15), LastChange: ago(400)},
			{ID: 3, Name: "Pixel 7", Tag: "PH-003", Model: "Pixel 7", Category: "Smartphone", Status: "Bereit",
				Deployable: true, PurchaseDate: ago(500), PurchaseCost: 599, WarrantyExpires: ahead(230),
				NextAudit: ahead(60), LastChange: ago(140)},
			{ID: 4, Name: `Monitor 27"`, Tag: "MO-004", Model: "U2723QE", Category: "Monitor", Status: "Ausgegeben",
				Deployable: true, Assigned: true, PurchaseDate: ago(60), PurchaseCost: 890, WarrantyExpires: ahead(1000),
				NextAudit: ahead(200), LastChange: ago(60)},
		},
		Licenses: []SnipeLicense{
			{ID: 1, Name: "JetBrains All Products", Expires: ahead(20), Seats: 1},
			{ID: 2, Name: "Microsoft 365", Expires: ahead(200), Seats: 5, Free: 2},
		},
		Consumables:  []SnipeConsumable{{ID: 1, Name: "Toner schwarz", Remaining: 1, Min: 2}},
		AuditOverdue: []int64{2},
	}
}

// DemoDawarich is the demo Dawarich dataset.
func DemoDawarich(now time.Time) *DawarichDataset {
	today := demoDay(now)
	days := clientDays(today)
	lat, lon := demoSite[0], demoSite[1]
	var visits []DawarichVisit
	for _, i := range demoClientDays {
		if i >= len(days) {
			continue
		}
		visits = append(visits, DawarichVisit{ID: int64(i), Start: stamp(days[i], 8, 30), End: stamp(days[i], 17, 45),
			Minutes: 555, AreaID: 2, Name: "Muster GmbH Büro", Lat: &lat, Lon: &lon})
	}
	return &DawarichDataset{
		URL: "https://dawarich.demo",
		Areas: []DawarichArea{
			{ID: 1, Name: "Home", Lat: demoHome[0], Lon: demoHome[1], Radius: 100},
			{ID: 2, Name: "Muster GmbH Büro", Lat: lat, Lon: lon, Radius: 150},
		},
		Visits:    visits,
		Stats:     map[string]any{"totalDistanceKm": 18450.0},
		LastPoint: now.UTC().Add(-2 * time.Hour).Format(time.RFC3339),
	}
}
