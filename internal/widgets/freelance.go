package widgets

// Freelance widgets on the Kimai connection:
//
//	kimai_timer   Kimai Lite, see kimailite.go
//	heatmap       hours per day over the last year, GitHub style
//	cashflow      expected balance for the next days (Ninja + Sure + taxes)

import (
	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

const minutesPerHour = 60

// ── heatmap ──

const (
	heatWeeks = 53
	heatCell  = 12 // px incl. gap
)

// heatLevels are the minute thresholds of levels 1–4 (2 h, 4 h, 6 h, 8 h).
var heatLevels = []int{1, 120, 240, 360, 480}

// HeatCell is one day of the heatmap.
type HeatCell struct {
	X, Y, Level int
	Day         string
	Hours       string
}

func heatLevel(minutes int) int {
	level := 0
	for i, limit := range heatLevels {
		if minutes >= limit {
			level = i + 1
		}
	}
	return min(level, len(heatLevels)-1)
}

func heatmapView(_ any, results map[string]any, ctx ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.KimaiDataset)
	if !ok {
		return map[string]any{}
	}
	perDay := metrics.HoursByDay(data)
	today := parseToday(ctx.Today)

	// Columns are weeks (Monday first), the last one holds today.
	lastMonday := today.AddDate(0, 0, -((int(today.Weekday()) + 6) % 7))
	first := lastMonday.AddDate(0, 0, -7*(heatWeeks-1))
	var cells []HeatCell
	total := 0
	for d := first; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format(isoDate)
		minutes := perDay[key]
		total += minutes
		week := int(d.Sub(first).Hours()/24) / 7
		cells = append(cells, HeatCell{X: week * heatCell, Y: ((int(d.Weekday()) + 6) % 7) * heatCell, Level: heatLevel(minutes),
			Day: key, Hours: clockMinutes(minutes)})
	}
	return map[string]any{"Cells": cells, "W": heatWeeks * heatCell, "H": 7 * heatCell, "Total": total / minutesPerHour}
}

// ── cashflow ──

const (
	defaultCashDays = 90
	cashEventsShown = 6
)

type CashflowConfig struct{ Days int }

func decodeCashflow(raw map[string]any) any {
	return CashflowConfig{Days: clampInt(asInt(raw["days"], defaultCashDays), 14, 365)}
}

func cashflowView(cfgAny any, results map[string]any, ctx ViewCtx) map[string]any {
	ninja, ok := results["data"].(*sources.NinjaDataset)
	if !ok {
		return map[string]any{}
	}
	in := metrics.CashInputs{Ninja: ninja, FixedMonthly: settingsFloat(settingsMap(ctx.Settings, "costs"), "fixed_monthly", 0),
		VATInterval: metrics.TaxVATInterval(ctx.Settings), VATMethod: metrics.TaxVATMethod(ctx.Settings)}
	in.Tax, in.HasTax = metrics.ParseTaxSettings(ctx.Settings)
	if sure, ok := results[peerSure].(*sources.SureDataset); ok {
		in.Sure = sure
	}
	points, events := metrics.Cashflow(in, parseToday(ctx.Today), cfgAny.(CashflowConfig).Days)
	if len(points) < 2 {
		return map[string]any{}
	}

	low, high := points[0].Balance, points[0].Balance
	lowDay := points[0].Day
	for _, p := range points {
		if p.Balance < low {
			low, lowDay = p.Balance, p.Day
		}
		high = max(high, p.Balance)
	}
	span := max(high-low, 1)
	step := float64(trendWidth) / float64(len(points)-1)
	coords := make([]string, len(points))
	for i, p := range points {
		coords[i] = formatPoint(float64(i)*step, trendHeight-(p.Balance-low)/span*(trendHeight-2*chartMargin)-chartMargin)
	}
	shown := events
	if len(shown) > cashEventsShown {
		shown = shown[:cashEventsShown]
	}
	return map[string]any{"Path": "M" + joinPoints(coords), "W": trendWidth, "H": trendHeight, "Low": low, "LowDay": lowDay,
		"End": points[len(points)-1].Balance, "Relative": in.Sure == nil, "Events": shown, "Currency": ninja.Currency}
}

func init() {
	Register(WidgetType{Key: "heatmap", Decode: decodeEmpty, Template: "widgets/heatmap", Category: CategoryInsight,
		Service: enums.ServiceKimai, RefreshS: 3600, Queries: dataQuery, View: heatmapView})
	Register(WidgetType{Key: "cashflow", Decode: decodeCashflow, Template: "widgets/cashflow", Category: CategoryInsight,
		Service: enums.ServiceInvoiceNinja, RefreshS: 3600, View: cashflowView,
		Queries: func(any) []Query { return append(dataQuery(nil), surePeer) }})
}
