// Package metrics: Snipe-IT — inventory value and upcoming ends.
package metrics

import (
	"sort"
	"time"

	"andon/internal/sources"
)

const horizonDays = 90

// SnipeUpcoming is one warranty/license/EOL date within the horizon.
type SnipeUpcoming struct {
	Kind string // "warranty", "eol", "license"
	Name string
	Date string
}

// SnipeUpcomingDates returns warranty, licence and EOL dates within `days`
// (default horizonDays), soonest first.
func SnipeUpcomingDates(data *sources.SnipeDataset, today time.Time, days int) []SnipeUpcoming {
	if days == 0 {
		days = horizonDays
	}
	var items []SnipeUpcoming
	for _, a := range data.Assets {
		for _, pair := range []struct{ kind, raw string }{
			{"warranty", a.WarrantyExpires}, {"eol", a.EOLDate},
		} {
			if within(pair.raw, today, days) {
				items = append(items, SnipeUpcoming{Kind: pair.kind, Name: a.Name, Date: pair.raw})
			}
		}
	}
	for _, l := range data.Licenses {
		if within(l.Expires, today, days) {
			items = append(items, SnipeUpcoming{Kind: "license", Name: l.Name, Date: l.Expires})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Date < items[j].Date })
	return items
}

func within(raw string, today time.Time, days int) bool {
	d, ok := ParseDay(raw)
	if !ok {
		return false
	}
	delta := int(d.Sub(today).Hours() / 24)
	return delta >= 0 && delta <= days
}

// SnipeSummary is the Snipe-IT dashboard's headline numbers.
type SnipeSummary struct {
	Assets       int
	Value        float64
	Ready        int
	Upcoming     []SnipeUpcoming
	AuditOverdue int
}

// SnipeSummaryOf computes SnipeSummary for today.
func SnipeSummaryOf(data *sources.SnipeDataset, today time.Time) SnipeSummary {
	var value float64
	ready := 0
	for _, a := range data.Assets {
		value += a.PurchaseCost
		if a.Deployable && !a.Assigned {
			ready++
		}
	}
	return SnipeSummary{
		Assets: len(data.Assets), Value: round2(value), Ready: ready,
		Upcoming: SnipeUpcomingDates(data, today, horizonDays), AuditOverdue: len(data.AuditOverdue),
	}
}
