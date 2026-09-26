package metrics

import (
	"time"

	"andon/internal/sources"
)

// seasonYears is how many prior years form the seasonal average.
const seasonYears = 3

// NinjaSeasonal returns the trailing months like NinjaByMonth, but Prev is
// the average of the same calendar month over up to three prior years
// with invoices, e.g. Sep 2026 vs Ø(Sep 2023, Sep 2024, Sep 2025).
func NinjaSeasonal(data *sources.NinjaDataset, today time.Time, months int) []NinjaMonth {
	first := today
	for _, i := range NinjaCounted(data) {
		if d, ok := ParseDay(i.Date); ok && d.Before(first) {
			first = d
		}
	}

	out := NinjaByMonth(data, today, months)
	for i := range out {
		start, _ := time.Parse("2006-01", out[i].Month)
		var sum float64
		years := 0
		for back := 1; back <= seasonYears; back++ {
			prevStart := start.AddDate(-back, 0, 0)
			if prevStart.Year() < first.Year() {
				break
			}
			sum += NinjaRevenue(data, prevStart, AddMonths(prevStart, 1).AddDate(0, 0, -1))
			years++
		}
		out[i].Prev = 0
		if years > 0 {
			out[i].Prev = round2(sum / float64(years))
		}
	}
	return out
}
