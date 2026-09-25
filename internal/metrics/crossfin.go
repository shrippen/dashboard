package metrics

// Freelance analyses across services:
//
//	FullCostRates     revenue ÷ all hours a customer costs (billable, internal, travel)
//	UnbilledAging     unbilled work by age: 0–30, 31–60, over 60 days
//	PaymentMatches    bank incomes → open invoices (number in the text, else amount)
//	MissingReceipts   business spending without expense, document or invoice mail
//	SafeToSpend       cash minus VAT, income tax reserve and fixed costs
//	Subscriptions     recurring payments with contract notice and last use
//	BudgetForecasts   when a project budget runs out, against its end date
//	ProjectMargins    work value minus expenses and time at cost rate
//	OrderGap          booked hours now against the same weeks last year
//	WorkloadOf        long weeks, late evenings, weekends, days since a free day

import (
	"sort"
	"strings"
	"time"

	"dashboard/internal/sources"
)

const (
	travelKMH      = 50.0 // average speed for travel time from trip distance
	agingMid       = 30
	agingOld       = 60
	daysPerMonth   = 30.0
	minRecurDays   = 25
	maxRecurDays   = 370
	minNameLen     = 4
	burnWindowDays = 28
	lateHour       = 22
	amountTol      = 0.01
)

// ── Full-cost rate ──

// FullRate is one customer's rate over the window.
type FullRate struct {
	Customer                   string
	BillableH, OtherH, TravelH float64
	Net                        float64
	Nominal, Full              float64 // per billable hour, per hour of any kind
}

// FullCostRates compares what a customer pays per billable hour with what
// they pay per hour they actually cost. dawarich may be nil.
func FullCostRates(kimai *sources.KimaiDataset, ninja *sources.NinjaDataset, dawarich *sources.DawarichDataset,
	mapping map[string]AreaMapping, today time.Time, days int) []FullRate {
	start := today.AddDate(0, 0, -days)
	names := KimaiCustomerNames(kimai)

	billable, other, travel := map[string]int{}, map[string]int{}, map[string]float64{}
	for _, s := range kimai.Timesheets {
		begin, ok := ParseTime(s.Begin)
		if !ok || begin.Before(start) || begin.After(today.AddDate(0, 0, 1)) {
			continue
		}
		k := nameKey(names[s.CustomerID])
		if s.Billable {
			billable[k] += s.Minutes
		} else {
			other[k] += s.Minutes
		}
	}
	if dawarich != nil {
		for _, t := range Trips(dawarich, mapping, start, today) {
			travel[nameKey(names[t.CustomerID])] += t.KM / travelKMH
		}
	}

	net, display := map[string]float64{}, map[string]string{}
	clients := ninjaClientNames(ninja)
	for _, inv := range NinjaCounted(ninja) {
		if d, ok := ParseDay(inv.Date); ok && !d.Before(start) {
			k := nameKey(clients[inv.ClientID])
			net[k] += inv.Net
			display[k] = clients[inv.ClientID]
		}
	}

	var out []FullRate
	for k, amount := range net {
		if k == "" || billable[k] == 0 {
			continue
		}
		r := FullRate{Customer: display[k], BillableH: float64(billable[k]) / minutesPerHour,
			OtherH: float64(other[k]) / minutesPerHour, TravelH: round1(travel[k]), Net: round2(amount)}
		r.Nominal = round2(amount / r.BillableH)
		r.Full = round2(amount / (r.BillableH + r.OtherH + r.TravelH))
		out = append(out, r)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Full < out[b].Full })
	return out
}

// ── Unbilled work by age ──

// AgingRow is one customer's unbilled work value by age.
type AgingRow struct {
	Customer        string
	Fresh, Mid, Old float64
	OldMinutes      int
	OldestDay       string
}

// UnbilledAging groups billable, not exported work by how long it waits.
func UnbilledAging(kimai *sources.KimaiDataset, today time.Time) []AgingRow {
	names := KimaiCustomerNames(kimai)
	rows := map[int64]*AgingRow{}
	for _, s := range kimai.Timesheets {
		if !s.Billable || s.Exported || s.End == "" {
			continue
		}
		d, ok := ParseDay(s.Begin)
		if !ok {
			continue
		}
		r, found := rows[s.CustomerID]
		if !found {
			r = &AgingRow{Customer: names[s.CustomerID]}
			rows[s.CustomerID] = r
		}
		age := int(today.Sub(d).Hours() / hoursPerDay)
		switch {
		case age > agingOld:
			r.Old += s.Rate
			r.OldMinutes += s.Minutes
		case age > agingMid:
			r.Mid += s.Rate
		default:
			r.Fresh += s.Rate
		}
		if day := d.Format(time.DateOnly); r.OldestDay == "" || day < r.OldestDay {
			r.OldestDay = day
		}
	}
	out := make([]AgingRow, 0, len(rows))
	for _, r := range rows {
		r.Fresh, r.Mid, r.Old = round2(r.Fresh), round2(r.Mid), round2(r.Old)
		out = append(out, *r)
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Old != out[b].Old {
			return out[a].Old > out[b].Old
		}
		return out[a].Mid+out[a].Fresh > out[b].Mid+out[b].Fresh
	})
	return out
}

// ── Payments ──

// Match reasons.
const (
	MatchNumber = "number" // invoice number in the booking text
	MatchAmount = "amount" // same amount after the invoice date
)

// PaymentMatch is a bank income that pays an open invoice.
type PaymentMatch struct {
	Txn     sources.SureTxn
	Day     time.Time
	Invoice NinjaOpenInvoice
	Reason  string
}

// compact makes numbers comparable in booking texts: "RE-2026 041" → "re2026041".
func compact(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// PaymentMatches pairs incomes of the last days with open invoices; each
// income and invoice is used once, number matches first.
func PaymentMatches(sure *sources.SureDataset, ninja *sources.NinjaDataset, today time.Time, days int) []PaymentMatch {
	open := NinjaOpenInvoices(ninja, today)
	since := today.AddDate(0, 0, -days)
	usedTxn, usedInv := map[string]bool{}, map[int64]bool{}
	var out []PaymentMatch

	pass := func(reason string, fits func(t sources.SureTxn, paid time.Time, inv NinjaOpenInvoice) bool) {
		for _, inv := range open {
			if usedInv[inv.ID] {
				continue
			}
			for _, t := range sure.Transactions {
				paid, ok := ParseDay(t.Date)
				if usedTxn[t.ID] || t.Amount <= 0 || !ok || paid.Before(since) || !fits(t, paid, inv) {
					continue
				}
				usedTxn[t.ID], usedInv[inv.ID] = true, true
				out = append(out, PaymentMatch{Txn: t, Day: paid, Invoice: inv, Reason: reason})
				break
			}
		}
	}
	pass(MatchNumber, func(t sources.SureTxn, _ time.Time, inv NinjaOpenInvoice) bool {
		number := compact(inv.Number)
		return len(number) >= minNameLen && strings.Contains(compact(t.Name), number)
	})
	pass(MatchAmount, func(t sources.SureTxn, paid time.Time, inv NinjaOpenInvoice) bool {
		issued, ok := ParseDay(inv.Date)
		return ok && !paid.Before(issued) && (abs(t.Amount-inv.Balance) <= amountTol || abs(t.Amount-inv.Amount) <= amountTol)
	})
	sort.Slice(out, func(a, b int) bool { return out[a].Day.After(out[b].Day) })
	return out
}

// UnmatchedIncome lists incomes naming a known client that paid no open
// invoice: a payment for an invoice that was never written, or a typo.
func UnmatchedIncome(sure *sources.SureDataset, ninja *sources.NinjaDataset, matches []PaymentMatch, today time.Time, days int) []sources.SureTxn {
	matched := map[string]bool{}
	for _, m := range matches {
		matched[m.Txn.ID] = true
	}
	var clients []string
	for _, c := range ninja.Clients {
		if len(c.Name) >= minNameLen {
			clients = append(clients, strings.ToLower(c.Name))
		}
	}
	since := today.AddDate(0, 0, -days)
	var out []sources.SureTxn
	for _, t := range sure.Transactions {
		d, ok := ParseDay(t.Date)
		if matched[t.ID] || t.Amount <= 0 || !ok || d.Before(since) {
			continue
		}
		text := strings.ToLower(t.Name + " " + t.Merchant)
		for _, c := range clients {
			if strings.Contains(text, c) {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// ── Receipts ──

// ReceiptInputs are the places a receipt may be; nil ones are skipped.
type ReceiptInputs struct {
	Sure      *sources.SureDataset
	Ninja     *sources.NinjaDataset
	Paperless *sources.PaperlessDataset
	Mail      *sources.MailDataset
	Accounts  []string // business accounts, lower case parts of the name
	Since     time.Time
	MinAmount float64
	Window    int // days between booking and receipt
}

// MissingReceipts returns business spending with no receipt anywhere.
func MissingReceipts(in ReceiptInputs) []sources.SureTxn {
	if in.Sure == nil || len(in.Accounts) == 0 {
		return nil
	}
	near := func(day string, d time.Time) bool {
		r, ok := ParseDay(day)
		return ok && abs(r.Sub(d).Hours()/hoursPerDay) <= float64(in.Window)
	}
	var out []sources.SureTxn
	for _, t := range in.Sure.Transactions {
		d, ok := ParseDay(t.Date)
		spent := -t.Amount
		if !ok || d.Before(in.Since) || spent < in.MinAmount || !businessAccount(t.Account, in.Accounts) {
			continue
		}
		if !receiptFound(in, spent, d, near) {
			out = append(out, t)
		}
	}
	return out
}

func businessAccount(account string, accounts []string) bool {
	lower := strings.ToLower(account)
	for _, a := range accounts {
		if a != "" && strings.Contains(lower, strings.ToLower(a)) {
			return true
		}
	}
	return false
}

func receiptFound(in ReceiptInputs, amount float64, d time.Time, near func(string, time.Time) bool) bool {
	if in.Ninja != nil {
		for _, e := range in.Ninja.Expenses {
			if (abs(e.Amount-amount) <= amountTol || abs(e.Amount-e.Tax-amount) <= amountTol) && near(e.Date, d) {
				return true
			}
		}
	}
	if in.Paperless != nil {
		for _, doc := range in.Paperless.Invoices {
			if abs(doc.Amount-amount) <= amountTol && near(doc.Created, d) {
				return true
			}
		}
	}
	if in.Mail != nil {
		for _, m := range in.Mail.Invoices {
			if abs(m.Amount-amount) <= amountTol && near(m.Date.Format(time.DateOnly), d) {
				return true
			}
		}
	}
	return false
}

// ── Money that is really free ──

// IncomeTaxReserve: this year's surplus (net revenue minus net expenses)
// times the income tax rate; never negative.
func IncomeTaxReserve(ninja *sources.NinjaDataset, today time.Time, rate float64) float64 {
	start := time.Date(today.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	revenue := NinjaRevenue(ninja, start, today)
	var expenses float64
	for _, e := range ninja.Expenses {
		if d, ok := ParseDay(e.Date); ok && d.Year() == today.Year() {
			expenses += e.Amount - e.Tax
		}
	}
	return max(revenue-expenses, 0) * rate
}

// Spendable splits the cash on the accounts.
type Spendable struct {
	Cash, VAT, IncomeTax, Fixed, Free float64
}

// SafeToSpend: cash minus VAT owed, income tax reserve and the fixed
// costs of the next 30 days.
func SafeToSpend(sure *sources.SureDataset, ninja *sources.NinjaDataset, today time.Time, interval, method string, taxRate float64) Spendable {
	s := Spendable{Cash: SureCash(sure), VAT: NinjaVATLiabilityOf(ninja, today, interval, method).Liability,
		IncomeTax: IncomeTaxReserve(ninja, today, taxRate), Fixed: SureDue(sure, today, int(daysPerMonth))}
	s.Free = s.Cash - s.VAT - s.IncomeTax - s.Fixed
	return s
}

// ── Subscriptions ──

// Usage is when something was last used (tile click, login).
type Usage struct {
	Name string
	Last time.Time
}

// Subscription is one recurring expense with what is known about it.
type Subscription struct {
	Name     string
	Monthly  float64
	Next     string
	Contract string    // matching Paperless contract, "" if none
	Deadline time.Time // last day to cancel, zero if unknown
	LastUse  time.Time // zero: no use found
	UseKnown bool      // a usage signal matched by name
}

// nameTokens are the words of a name long enough to match on.
func nameTokens(name string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127)
	}) {
		if len(w) >= minNameLen {
			out = append(out, w)
		}
	}
	return out
}

// sameThing: the first word of a (usually the brand) is a word of b
// ("Adobe Creative Cloud" ~ "adobe.com", not ~ "cloud.lan").
func sameThing(a, b string) bool {
	ta := nameTokens(a)
	if len(ta) == 0 {
		return false
	}
	for _, y := range nameTokens(b) {
		if y == ta[0] {
			return true
		}
	}
	return false
}

// Subscriptions lists active recurring expenses with monthly cost, the
// matching contract and the last use found.
func Subscriptions(sure *sources.SureDataset, contracts []sources.PaperlessContract, usage []Usage) []Subscription {
	var out []Subscription
	for _, r := range sure.Recurring {
		if !r.Expense || r.Status == "inactive" {
			continue
		}
		s := Subscription{Name: r.Name, Monthly: round2(monthly(r)), Next: r.Next}
		for _, c := range contracts {
			if sameThing(r.Name, c.Title+" "+c.Correspondent) {
				s.Contract, s.Deadline = c.Title, c.Deadline
			}
		}
		for _, u := range usage {
			if sameThing(r.Name, u.Name) {
				s.UseKnown = true
				if u.Last.After(s.LastUse) {
					s.LastUse = u.Last
				}
			}
		}
		out = append(out, s)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Monthly > out[b].Monthly })
	return out
}

// monthly converts a recurring amount by its interval (last → next).
func monthly(r sources.SureRecurring) float64 {
	amount := r.Avg
	if amount == 0 {
		amount = r.Amount
	}
	last, ok1 := ParseDay(r.Last)
	next, ok2 := ParseDay(r.Next)
	if !ok1 || !ok2 {
		return amount
	}
	days := next.Sub(last).Hours() / hoursPerDay
	if days < minRecurDays || days > maxRecurDays {
		return amount
	}
	return amount * daysPerMonth / days
}

// ── Budgets ──

// BudgetForecast is when a project budget runs out at the recent pace.
type BudgetForecast struct {
	ProjectID int64
	Project   string
	Used      float64   // share of the budget used
	RunOut    time.Time // zero: not at this pace within a year
	End       time.Time // project end, zero if none
	GapDays   int       // days between run-out and end; > 0 means too early
}

// BudgetForecasts projects the last four weeks' pace of money or time.
func BudgetForecasts(kimai *sources.KimaiDataset, today time.Time) []BudgetForecast {
	since := today.AddDate(0, 0, -burnWindowDays)
	var out []BudgetForecast
	for _, p := range kimai.Projects {
		if p.BudgetType != "" || (p.Budget <= 0 && p.TimeBudgetMin <= 0) {
			continue
		}
		var recent float64
		for _, s := range kimai.Timesheets {
			d, ok := ParseDay(s.Begin)
			if s.ProjectID != p.ID || !ok || d.Before(since) {
				continue
			}
			if p.Budget > 0 {
				recent += s.Rate
			} else {
				recent += float64(s.Minutes)
			}
		}
		total, used := p.Budget, p.UsedMoney
		if p.Budget <= 0 {
			total, used = float64(p.TimeBudgetMin), float64(p.UsedMinutes)
		}
		f := BudgetForecast{ProjectID: p.ID, Project: p.Name, Used: used / total}
		f.End, _ = ParseDay(p.End)
		if perDay := recent / burnWindowDays; perDay > 0 && used < total {
			days := (total - used) / perDay
			if days <= 365 {
				f.RunOut = today.AddDate(0, 0, int(days))
			}
		} else if used >= total {
			f.RunOut = today
		}
		if !f.RunOut.IsZero() && !f.End.IsZero() {
			f.GapDays = int(f.End.Sub(f.RunOut).Hours() / hoursPerDay)
		}
		out = append(out, f)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].GapDays > out[b].GapDays })
	return out
}

// ── Project margin ──

// ProjectMargin is one project's result over the window.
type ProjectMargin struct {
	Project, Customer string
	Hours             float64
	Revenue, Expenses float64
	TimeCost, Result  float64
	Margin            float64 // Result ÷ Revenue
}

// ProjectMargins: value of billable work minus expenses whose note names
// the project minus all hours at hourlyCost (0 = time not priced).
func ProjectMargins(kimai *sources.KimaiDataset, ninja *sources.NinjaDataset, hourlyCost float64, today time.Time, days int) []ProjectMargin {
	start := today.AddDate(0, 0, -days)
	names := KimaiCustomerNames(kimai)
	byID := map[int64]*ProjectMargin{}
	for _, p := range kimai.Projects {
		byID[p.ID] = &ProjectMargin{Project: p.Name, Customer: names[p.CustomerID]}
	}
	for _, s := range kimai.Timesheets {
		d, ok := ParseDay(s.Begin)
		m := byID[s.ProjectID]
		if !ok || d.Before(start) || m == nil {
			continue
		}
		m.Hours += float64(s.Minutes) / minutesPerHour
		if s.Billable {
			m.Revenue += s.Rate
		}
	}
	var out []ProjectMargin
	for _, m := range byID {
		if m.Revenue <= 0 {
			continue
		}
		if ninja != nil {
			for _, e := range ninja.Expenses {
				d, ok := ParseDay(e.Date)
				if ok && !d.Before(start) && len(m.Project) >= minNameLen && strings.Contains(strings.ToLower(e.Notes), strings.ToLower(m.Project)) {
					m.Expenses += e.Amount - e.Tax
				}
			}
		}
		m.TimeCost = m.Hours * hourlyCost
		m.Result = m.Revenue - m.Expenses - m.TimeCost
		m.Margin = m.Result / m.Revenue
		m.Hours, m.Revenue, m.Expenses, m.TimeCost, m.Result = round1(m.Hours), round2(m.Revenue), round2(m.Expenses), round2(m.TimeCost), round2(m.Result)
		out = append(out, *m)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Margin < out[b].Margin })
	return out
}

// ── Order gap ──

// Gap compares booked work with the same weeks last year.
type Gap struct {
	RecentH, LastYearH float64 // last `days` days, and the same days a year ago
	AheadLastYearH     float64 // the following 8 weeks last year
	OpenQuotes         int
	QuoteSum           float64
}

// aheadDays is the look-ahead compared with last year.
const aheadDays = 56

// OrderGap needs last year's timesheets; ok is false without them.
func OrderGap(kimai *sources.KimaiDataset, ninja *sources.NinjaDataset, today time.Time, days int) (Gap, bool) {
	yearAgo := today.AddDate(-1, 0, 0)
	g := Gap{
		RecentH:        float64(KimaiMinutesBetween(kimai, today.AddDate(0, 0, -days), today, HoursAll)) / minutesPerHour,
		LastYearH:      float64(KimaiMinutesBetween(kimai, yearAgo.AddDate(0, 0, -days), yearAgo, HoursAll)) / minutesPerHour,
		AheadLastYearH: float64(KimaiMinutesBetween(kimai, yearAgo, yearAgo.AddDate(0, 0, aheadDays), HoursAll)) / minutesPerHour,
	}
	if ninja != nil {
		for _, q := range ninja.Quotes {
			if q.Status == "sent" || q.Status == "2" {
				g.OpenQuotes++
				g.QuoteSum += q.Amount
			}
		}
	}
	return g, g.LastYearH > 0
}

// ── Workload ──

// Workload summarizes recent working habits.
type Workload struct {
	LongWeeks     int // of the last 4 weeks, over the limit
	LastWeekH     float64
	LateEvenings  int // last 4 weeks, work ending after 22:00
	WeekendDays   int // last 4 weeks
	DaysSinceFree int // days since a day without any booking
}

// WorkloadOf reads the last four weeks of timesheets; free days (holidays,
// absences) count as free even with bookings.
func WorkloadOf(kimai *sources.KimaiDataset, today time.Time, weekLimitH float64) Workload {
	var w Workload
	start := today.AddDate(0, 0, -burnWindowDays)
	perDay := map[string]int{}
	weekend := map[string]bool{}
	for _, s := range kimai.Timesheets {
		begin, ok := ParseTime(s.Begin)
		if !ok || begin.Before(start) {
			continue
		}
		day := begin.Format(time.DateOnly)
		perDay[day] += s.Minutes
		if wd := begin.Weekday(); wd == time.Saturday || wd == time.Sunday {
			weekend[day] = true
		}
		if end, ok := ParseTime(s.End); ok && (end.Hour() >= lateHour || end.Day() != begin.Day()) {
			w.LateEvenings++
		}
	}
	w.WeekendDays = len(weekend)
	for week := 0; week < burnWindowDays/7; week++ {
		minutes := 0
		for d := 0; d < 7; d++ {
			minutes += perDay[today.AddDate(0, 0, -week*7-d).Format(time.DateOnly)]
		}
		if week == 0 {
			w.LastWeekH = round1(float64(minutes) / minutesPerHour)
		}
		if float64(minutes)/minutesPerHour > weekLimitH {
			w.LongWeeks++
		}
	}
	free := KimaiFreeDays(kimai)
	for d := 0; d <= burnWindowDays; d++ {
		day := today.AddDate(0, 0, -d)
		if perDay[day.Format(time.DateOnly)] == 0 || free[Today(day)] {
			return withSince(w, d)
		}
	}
	return withSince(w, burnWindowDays+1)
}

func withSince(w Workload, days int) Workload {
	w.DaysSinceFree = days
	return w
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
