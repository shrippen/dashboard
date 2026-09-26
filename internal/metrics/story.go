package metrics

// The week in numbers, connected: one line per topic, from the stored
// datasets and history of a scope.
//
//	38 h worked, 60 % for Acme · 2 invoices paid (4.100 €) · tank +3 points
//	(81 %) · power 52 kWh, 14 € · 5 hints new, 7 resolved

import (
	"sort"
	"time"

	"andon/internal/sources"
)

// StoryLine is one sentence: catalog key under week.* and typed params.
type StoryLine struct {
	Key    string
	Params map[string]any
}

// WeekStory builds the lines of the seven days before now.
func WeekStory(datasets map[string]any, h *History, now time.Time) []StoryLine {
	start := Today(now).AddDate(0, 0, -weekLen)
	var out []StoryLine
	for _, raw := range datasets {
		switch d := raw.(type) {
		case *sources.KimaiDataset:
			if line, ok := weekHours(d, start, now); ok {
				out = append(out, line)
			}
		case *sources.NinjaDataset:
			out = append(out, weekMoney(d, start)...)
		case *sources.TibberDataset:
			kwh, cost := 0.0, 0.0
			for _, day := range d.Days {
				if t, ok := ParseDay(day.Day); ok && !t.Before(start) {
					kwh, cost = kwh+day.KWh, cost+day.Cost
				}
			}
			if kwh > 0 {
				out = append(out, StoryLine{Key: "power", Params: map[string]any{"kwh": numParam(kwh, 0), "cost": moneyParam(cost, d.Currency)}})
			}
		}
	}
	if line, ok := weekStorage(h, start); ok {
		out = append(out, line)
	}
	sort.SliceStable(out, func(i, j int) bool { return storyOrder[out[i].Key] < storyOrder[out[j].Key] })
	return out
}

// storyOrder puts work before money before machines.
var storyOrder = map[string]int{"hours": 0, "invoiced": 1, "paid": 2, "storage": 3, "power": 4}

func numParam(v float64, digits int) map[string]any {
	return map[string]any{"$num": v, "digits": digits}
}

func moneyParam(v float64, currency string) map[string]any {
	return map[string]any{"$money": v, "currency": currency}
}

func weekHours(kimai *sources.KimaiDataset, start, now time.Time) (StoryLine, bool) {
	names := KimaiCustomerNames(kimai)
	per := map[string]int{}
	total := 0
	for _, s := range kimai.Timesheets {
		t, ok := ParseTime(s.Begin)
		if !ok || t.Before(start) || t.After(now) {
			continue
		}
		per[names[s.CustomerID]] += s.Minutes
		total += s.Minutes
	}
	if total == 0 {
		return StoryLine{}, false
	}
	top, topMin := "", 0
	for name, m := range per {
		if m > topMin || (m == topMin && name < top) {
			top, topMin = name, m
		}
	}
	return StoryLine{Key: "hours", Params: map[string]any{"hours": numParam(float64(total)/minutesPerHour, 0),
		"customer": top, "percent": numParam(float64(topMin)*100/float64(total), 0)}}, true
}

func weekMoney(ninja *sources.NinjaDataset, start time.Time) []StoryLine {
	var out []StoryLine
	paid, paidN := 0.0, 0
	for _, p := range ninja.Payments {
		if d, ok := ParseDay(p.Date); ok && !d.Before(start) {
			paid += p.Amount
			paidN++
		}
	}
	invoiced, invN := 0.0, 0
	for _, i := range NinjaCounted(ninja) {
		if d, ok := ParseDay(i.Date); ok && !d.Before(start) {
			invoiced += i.Amount
			invN++
		}
	}
	if invN > 0 {
		out = append(out, StoryLine{Key: "invoiced", Params: map[string]any{"count": invN, "amount": moneyParam(invoiced, ninja.Currency)}})
	}
	if paidN > 0 {
		out = append(out, StoryLine{Key: "paid", Params: map[string]any{"count": paidN, "amount": moneyParam(paid, ninja.Currency)}})
	}
	return out
}

// weekStorage names the storage that grew most, in percentage points.
func weekStorage(h *History, start time.Time) (StoryLine, bool) {
	if h == nil {
		return StoryLine{}, false
	}
	best, bestGrowth, bestNow := "", 0.0, 0.0
	for _, k := range h.Keys("") {
		if len(k) < len(usedSuffix) || k[len(k)-len(usedSuffix):] != usedSuffix {
			continue
		}
		series := h.SeriesOf(k)
		if len(series) == 0 {
			continue
		}
		then, ok := ValueOn(series, start)
		now := series[len(series)-1].Value
		if ok && now-then > bestGrowth {
			best, bestGrowth, bestNow = storageLabel(k), now-then, now
		}
	}
	if best == "" {
		return StoryLine{}, false
	}
	return StoryLine{Key: "storage", Params: map[string]any{"name": best, "points": numParam(bestGrowth*percentScale, 1),
		"percent": numParam(bestNow*percentScale, 0)}}, true
}
