package metrics

import (
	"sort"
	"strings"
	"time"

	"andon/internal/sources"
)

// Effective hourly rate: what a customer actually paid per hour worked.
//
//	net invoiced (Invoice Ninja, last 365 days) ÷ hours booked (Kimai, same window)
//
// Customers are matched by name ("Muster GmbH" in both tools).

const rateWindowDays = 365

// RateRow is one customer's effective rate.
type RateRow struct {
	Customer string
	Hours    float64
	Net      float64
	Rate     float64 // 0 when no hours were booked
}

func nameKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// EffectiveRates returns per-customer rates (highest revenue first) and the
// overall rate across matched customers.
func EffectiveRates(kimai *sources.KimaiDataset, ninja *sources.NinjaDataset, today time.Time) ([]RateRow, float64) {
	start := today.AddDate(0, 0, -rateWindowDays)

	minutes := map[string]int{}
	names := KimaiCustomerNames(kimai)
	for _, s := range kimai.Timesheets {
		begin, ok := ParseTime(s.Begin)
		if !ok || begin.Before(start) || begin.After(today.AddDate(0, 0, 1)) {
			continue
		}
		minutes[nameKey(names[s.CustomerID])] += s.Minutes
	}

	net := map[string]float64{}
	display := map[string]string{}
	clients := ninjaClientNames(ninja)
	for _, inv := range NinjaCounted(ninja) {
		if d, ok := ParseDay(inv.Date); ok && !d.Before(start) {
			key := nameKey(clients[inv.ClientID])
			net[key] += inv.Net
			display[key] = clients[inv.ClientID]
		}
	}

	var rows []RateRow
	var totalNet float64
	var totalMin int
	for key, amount := range net {
		m, ok := minutes[key]
		if !ok || m == 0 || key == "" {
			continue
		}
		hours := float64(m) / minutesPerHour
		rows = append(rows, RateRow{Customer: display[key], Hours: round2(hours), Net: round2(amount), Rate: round2(amount / hours)})
		totalNet += amount
		totalMin += m
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Net > rows[b].Net })

	overall := 0.0
	if totalMin > 0 {
		overall = round2(totalNet / (float64(totalMin) / minutesPerHour))
	}
	return rows, overall
}
