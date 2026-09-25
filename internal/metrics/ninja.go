// Package metrics: Invoice Ninja — revenue (net), receivables, VAT, payment behaviour.
package metrics

import (
	"sort"
	"time"

	"dashboard/internal/sources"
)

const (
	monthsBack  = 12
	daysPerYear = 365
)

func isCounted(status string) bool {
	return status == "sent" || status == "partial" || status == "paid"
}
func isOpen(status string) bool { return status == "sent" || status == "partial" }

// NinjaCounted returns invoices counted as revenue (sent/partial/paid, dated).
func NinjaCounted(data *sources.NinjaDataset) []sources.NinjaInvoice {
	var out []sources.NinjaInvoice
	for _, i := range data.Invoices {
		if isCounted(i.Status) && i.Date != "" {
			out = append(out, i)
		}
	}
	return out
}

// NinjaRevenue is net revenue by invoice date in [start, end]: VAT is not revenue.
func NinjaRevenue(data *sources.NinjaDataset, start, end time.Time) float64 {
	var total float64
	for _, i := range NinjaCounted(data) {
		d, ok := ParseDay(i.Date)
		if ok && !d.Before(start) && !d.After(end) {
			total += i.Net
		}
	}
	return round2(total)
}

// NinjaMonth is one month's net revenue plus the same month last year, for
// the chart widget.
type NinjaMonth struct {
	Month string // "2026-09"
	Net   float64
	Prev  float64
}

// NinjaByMonth returns the trailing `months` months (oldest first).
func NinjaByMonth(data *sources.NinjaDataset, today time.Time, months int) []NinjaMonth {
	if months == 0 {
		months = monthsBack
	}
	out := make([]NinjaMonth, 0, months)
	for back := months - 1; back >= 0; back-- {
		start := AddMonths(today, -back)
		end := AddMonths(start, 1).AddDate(0, 0, -1)
		prevStart := time.Date(start.Year()-1, start.Month(), 1, 0, 0, 0, 0, time.UTC)
		prevEnd := AddMonths(prevStart, 1).AddDate(0, 0, -1)
		out = append(out, NinjaMonth{
			Month: start.Format("2006-01"), Net: NinjaRevenue(data, start, end),
			Prev: NinjaRevenue(data, prevStart, prevEnd),
		})
	}
	return out
}

// NinjaOpenInvoice is an open invoice enriched with its client name and
// days overdue.
type NinjaOpenInvoice struct {
	sources.NinjaInvoice
	Client      string
	OverdueDays int
}

// NinjaOpenInvoices returns open (sent/partial, balance > 0) invoices,
// most-overdue first.
func NinjaOpenInvoices(data *sources.NinjaDataset, today time.Time) []NinjaOpenInvoice {
	clients := ninjaClientNames(data)
	var out []NinjaOpenInvoice
	for _, i := range data.Invoices {
		if !isOpen(i.Status) || i.Balance <= 0 {
			continue
		}
		overdue := 0
		if due, ok := ParseDay(i.DueDate); ok && due.Before(today) {
			overdue = int(today.Sub(due).Hours() / 24)
		}
		name := clients[i.ClientID]
		if name == "" {
			name = "?"
		}
		out = append(out, NinjaOpenInvoice{NinjaInvoice: i, Client: name, OverdueDays: overdue})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].OverdueDays > out[b].OverdueDays })
	return out
}

func ninjaClientNames(data *sources.NinjaDataset) map[int64]string {
	out := make(map[int64]string, len(data.Clients))
	for _, c := range data.Clients {
		out[c.ID] = c.Name
	}
	return out
}

// NinjaVATPeriod returns [start, end] of the VAT period containing today.
func NinjaVATPeriod(today time.Time, interval string) (time.Time, time.Time) {
	var start time.Time
	months := 1
	if interval == "quarterly" {
		start, months = QuarterStart(today), 3
	} else {
		start = MonthStart(today)
	}
	return start, AddMonths(start, months).AddDate(0, 0, -1)
}

// NinjaOutputVAT is VAT owed for [start, end].
//
//	soll (accrual):   VAT of invoices dated in the period
//	ist (cash basis): VAT share of payments received in the period (per client ratio)
func NinjaOutputVAT(data *sources.NinjaDataset, start, end time.Time, method string) float64 {
	if method == "soll" {
		var total float64
		for _, i := range NinjaCounted(data) {
			if d, ok := ParseDay(i.Date); ok && !d.Before(start) && !d.After(end) {
				total += i.Taxes
			}
		}
		return round2(total)
	}

	type shareT struct{ taxes, amount float64 }
	share := map[int64]shareT{}
	for _, i := range NinjaCounted(data) {
		s := share[i.ClientID]
		s.taxes += i.Taxes
		s.amount += i.Amount
		share[i.ClientID] = s
	}

	var total float64
	for _, p := range data.Payments {
		d, ok := ParseDay(p.Date)
		if !ok || d.Before(start) || d.After(end) {
			continue
		}
		s := share[p.ClientID]
		if s.amount != 0 {
			total += p.Amount * (s.taxes / s.amount)
		}
	}
	return round2(total)
}

// NinjaInputVAT sums expense VAT in [start, end].
func NinjaInputVAT(data *sources.NinjaDataset, start, end time.Time) float64 {
	var total float64
	for _, e := range data.Expenses {
		if d, ok := ParseDay(e.Date); ok && !d.Before(start) && !d.After(end) {
			total += e.Tax
		}
	}
	return round2(total)
}

// NinjaVATLiability is one period's VAT liability (output − input).
type NinjaVATLiability struct {
	Start, End    string
	Output, Input float64
	Liability     float64
}

// NinjaVATLiabilityOf computes the current period's liability.
func NinjaVATLiabilityOf(data *sources.NinjaDataset, today time.Time, interval, method string) NinjaVATLiability {
	start, end := NinjaVATPeriod(today, interval)
	out, inp := NinjaOutputVAT(data, start, end, method), NinjaInputVAT(data, start, end)
	return NinjaVATLiability{
		Start: start.Format("2006-01-02"), End: end.Format("2006-01-02"),
		Output: out, Input: inp, Liability: round2(out - inp),
	}
}

// NinjaClientShare is one client's revenue share over the trailing year.
type NinjaClientShare struct {
	ClientID int64
	Client   string
	Net      float64
	Share    float64
}

// NinjaShares returns revenue share per client over the trailing 12
// months, largest share first.
func NinjaShares(data *sources.NinjaDataset, today time.Time) []NinjaClientShare {
	start := today.AddDate(0, 0, -365)
	totals := map[int64]float64{}
	for _, i := range NinjaCounted(data) {
		if d, ok := ParseDay(i.Date); ok && !d.Before(start) {
			totals[i.ClientID] += i.Net
		}
	}
	var whole float64
	for _, v := range totals {
		whole += v
	}
	clients := ninjaClientNames(data)
	out := make([]NinjaClientShare, 0, len(totals))
	for cid, v := range totals {
		name := clients[cid]
		if name == "" {
			name = "?"
		}
		share := 0.0
		if whole != 0 {
			share = v / whole
		}
		out = append(out, NinjaClientShare{ClientID: cid, Client: name, Net: round2(v), Share: share})
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Share > out[b].Share })
	return out
}

// NinjaPaymentDays is the average days from invoice to the next payment of
// the same client (approximation), keyed by client id.
func NinjaPaymentDays(data *sources.NinjaDataset) map[int64]int {
	byClient := map[int64][]time.Time{}
	for _, p := range data.Payments {
		if d, ok := ParseDay(p.Date); ok {
			byClient[p.ClientID] = append(byClient[p.ClientID], d)
		}
	}

	result := map[int64]int{}
	for clientID, paid := range byClient {
		sort.Slice(paid, func(a, b int) bool { return paid[a].Before(paid[b]) })
		var gaps []int
		for _, i := range NinjaCounted(data) {
			if i.ClientID != clientID || i.Status != "paid" {
				continue
			}
			d, ok := ParseDay(i.Date)
			if !ok {
				continue
			}
			for _, pd := range paid {
				if !pd.Before(d) {
					gaps = append(gaps, int(pd.Sub(d).Hours()/24))
					break
				}
			}
		}
		if len(gaps) > 0 {
			sum := 0
			for _, g := range gaps {
				sum += g
			}
			result[clientID] = int(round(float64(sum) / float64(len(gaps))))
		}
	}
	return result
}

// NinjaSummary is the Invoice Ninja dashboard's headline numbers.
type NinjaSummary struct {
	RevenueYTD     float64
	RevenuePrevYTD float64
	RevenueMonth   float64
	OpenAmount     float64
	Overdue        []NinjaOpenInvoice
	Open           []NinjaOpenInvoice
	VAT            NinjaVATLiability
	Currency       string
}

// NinjaSummaryOf computes NinjaSummary for today.
func NinjaSummaryOf(data *sources.NinjaDataset, today time.Time, interval, method string) NinjaSummary {
	if interval == "" {
		interval = "monthly"
	}
	if method == "" {
		method = "ist"
	}
	yearStart := time.Date(today.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	lastYear := time.Date(today.Year()-1, today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	if today.Month() == time.February && today.Day() == 29 {
		lastYear = time.Date(today.Year()-1, time.February, 28, 0, 0, 0, 0, time.UTC)
	}

	open := NinjaOpenInvoices(data, today)
	var overdue []NinjaOpenInvoice
	var openAmount float64
	for _, i := range open {
		openAmount += i.Balance
		if i.OverdueDays > 0 {
			overdue = append(overdue, i)
		}
	}

	currency := data.Currency
	if currency == "" {
		currency = "EUR"
	}
	return NinjaSummary{
		RevenueYTD:     NinjaRevenue(data, yearStart, today),
		RevenuePrevYTD: NinjaRevenue(data, time.Date(today.Year()-1, 1, 1, 0, 0, 0, 0, time.UTC), lastYear),
		RevenueMonth:   NinjaRevenue(data, MonthStart(today), today), OpenAmount: round2(openAmount),
		Overdue: overdue, Open: open, VAT: NinjaVATLiabilityOf(data, today, interval, method), Currency: currency,
	}
}

// NinjaForecastYear is a linear projection of net revenue to Dec 31, from
// the pace so far.
func NinjaForecastYear(data *sources.NinjaDataset, today time.Time) float64 {
	yearStart := time.Date(today.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	ytd := NinjaRevenue(data, yearStart, today)
	elapsed := today.Sub(yearStart).Hours()/24 + 1
	yearEnd := time.Date(today.Year(), 12, 31, 0, 0, 0, 0, time.UTC)
	days := yearEnd.Sub(yearStart).Hours()/24 + 1
	return round2(ytd / elapsed * days)
}

// NinjaCashExpected is receivables plus recurring invoices due within
// `days` (gross).
func NinjaCashExpected(data *sources.NinjaDataset, today time.Time, days int) float64 {
	horizon := today.AddDate(0, 0, days)
	var openAmount float64
	for _, i := range NinjaOpenInvoices(data, today) {
		openAmount += i.Balance
	}
	var recurring float64
	for _, r := range data.Recurring {
		if !r.Active {
			continue
		}
		d, ok := ParseDay(r.NextSendDate)
		if ok && !d.Before(today) && !d.After(horizon) {
			recurring += r.Amount
		}
	}
	return round2(openAmount + recurring)
}
