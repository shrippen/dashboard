// Package metrics: tax deadlines of a freelancer liable to VAT (Germany),
// from space settings.
//
//	settings["tax"] = {"vat": {"return_interval": "monthly", "extension": true},
//	                   "prepayments": {"amount": 1200}, "annual_due": "07-31"}
package metrics

import (
	"fmt"
	"time"
)

const (
	dueDay        = 10
	defaultAnnual = "07-31"
	quarterMonths = 3
)

var prepaymentMonths = []time.Month{time.March, time.June, time.September, time.December}

// TaxSettings mirrors the "tax" key of a space's settings map.
type TaxSettings struct {
	VATReturnInterval string // "monthly" (default) or "quarterly"
	VATExtension      bool   // Dauerfristverlängerung: due date shifts by one month
	PrepaymentAmount  *float64
	AnnualDueMonthDay string // "MM-DD", default "07-31"
}

// TaxDeadline is one upcoming deadline.
type TaxDeadline struct {
	Kind        string // "vat_return", "prepayment", "annual"
	Due         time.Time
	Period      string    // vat_return only: "09/2026" or "Q3/2026"
	PeriodStart time.Time // vat_return only
	PeriodEnd   time.Time // vat_return only
	Amount      *float64  // prepayment only
	Year        int       // annual only: the year the return covers
}

func vatReturns(today time.Time, tax TaxSettings, horizon time.Time) []TaxDeadline {
	months := 1
	if tax.VATReturnInterval == "quarterly" {
		months = quarterMonths
	}
	shift := 0
	if tax.VATExtension {
		shift = 1
	}

	var found []TaxDeadline
	for start := AddMonths(today, -12); !start.After(horizon); start = AddMonths(start, 1) {
		if months != 1 && (int(start.Month())-1)%quarterMonths != 0 {
			continue
		}
		periodEnd := AddMonths(start, months).AddDate(0, 0, -1)
		due := time.Date(AddMonths(periodEnd, 1+shift).Year(), AddMonths(periodEnd, 1+shift).Month(), dueDay,
			0, 0, 0, 0, time.UTC)
		if today.After(due) || due.After(horizon) {
			continue
		}
		label := start.Format("01/2006")
		if months != 1 {
			label = fmt.Sprintf("Q%d/%d", (int(start.Month())-1)/3+1, start.Year())
		}
		found = append(found, TaxDeadline{
			Kind: "vat_return", Due: due, Period: label, PeriodStart: start, PeriodEnd: periodEnd,
		})
	}
	return found
}

func prepayments(today time.Time, tax TaxSettings, horizon time.Time) []TaxDeadline {
	var found []TaxDeadline
	for _, year := range []int{today.Year(), today.Year() + 1} {
		for _, month := range prepaymentMonths {
			due := time.Date(year, month, dueDay, 0, 0, 0, 0, time.UTC)
			if !today.After(due) && !due.After(horizon) {
				found = append(found, TaxDeadline{Kind: "prepayment", Due: due, Amount: tax.PrepaymentAmount})
			}
		}
	}
	return found
}

func annual(today time.Time, tax TaxSettings, horizon time.Time) []TaxDeadline {
	spec := tax.AnnualDueMonthDay
	if spec == "" {
		spec = defaultAnnual
	}
	var month, day int
	fmt.Sscanf(spec, "%d-%d", &month, &day)
	if month == 0 {
		month, day = 7, 31
	}

	var found []TaxDeadline
	for _, year := range []int{today.Year(), today.Year() + 1} {
		due := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		if !today.After(due) && !due.After(horizon) {
			found = append(found, TaxDeadline{Kind: "annual", Due: due, Year: year - 1})
		}
	}
	return found
}

// UpcomingDeadlines returns every tax deadline within `days`, soonest first.
func UpcomingDeadlines(tax TaxSettings, today time.Time, days int) []TaxDeadline {
	horizon := today.AddDate(0, 0, days)
	items := append(vatReturns(today, tax, horizon), prepayments(today, tax, horizon)...)
	items = append(items, annual(today, tax, horizon)...)

	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Due.Before(items[j-1].Due); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
	return items
}
