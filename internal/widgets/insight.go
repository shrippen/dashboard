// Package widgets: insight widgets — metrics, tables, charts, budgets,
// deadlines, hints. All data comes from the "data" query of the widget's
// connection; view functions turn it into template values with the pure
// metrics package (no I/O here).
package widgets

import (
	"fmt"
	"sort"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

const (
	minutesPerHourInsight = 60
	defaultIncomeTaxRate  = 0.3
)

// Metric selects which KPI a "kpi" widget shows.
type Metric string

const (
	MetricHoursToday      Metric = "hours_today"
	MetricHoursWeek       Metric = "hours_week"
	MetricHoursMonth      Metric = "hours_month"
	MetricUtilization     Metric = "utilization"
	MetricUnbilled        Metric = "unbilled"
	MetricRevenueYTD      Metric = "revenue_ytd"
	MetricRevenueMonth    Metric = "revenue_month"
	MetricOpenAmount      Metric = "open_amount"
	MetricOverdueAmount   Metric = "overdue_amount"
	MetricVATLiability    Metric = "vat_liability"
	MetricTaxReserve      Metric = "tax_reserve"
	MetricAssetValue      Metric = "asset_value"
	MetricAssetsReady     Metric = "assets_ready"
	MetricRevenueForecast Metric = "revenue_forecast"
	MetricCash30          Metric = "cash_30"
	MetricEffectiveRate   Metric = "effective_rate"
	MetricLiquidity30     Metric = "liquidity_30"
	MetricNetWorth        Metric = "net_worth"
	MetricCash            Metric = "cash"
)

// TableKind selects a "table" widget's row source.
type TableKind string

const (
	TableOpenInvoices TableKind = "open_invoices"
	TableUnbilled     TableKind = "unbilled"
	TableBudgets      TableKind = "budgets"
	TableClientShares TableKind = "client_shares"
	TableAssetDates   TableKind = "asset_dates"
	TableTrips        TableKind = "trips"
	TableRates        TableKind = "effective_rates"
	TableAppUsage     TableKind = "app_usage"
)

// ChartKind selects a "chart" widget's series.
type ChartKind string

const (
	ChartRevenue ChartKind = "revenue"
	ChartHours   ChartKind = "hours"
	ChartSeason  ChartKind = "seasonal"
)

// TrendMetric selects a "trend" widget's daily snapshot series.
type TrendMetric string

const (
	TrendRevenueYTD  TrendMetric = "revenue_ytd"
	TrendOpenAmount  TrendMetric = "open_amount"
	TrendMonthMinute TrendMetric = "month_min"
)

// KpiConfig is the "kpi" widget's config.
type KpiConfig struct{ Metric Metric }

func decodeKpi(raw map[string]any) any {
	metric := Metric(asString(raw["metric"]))
	if metric == "" {
		metric = MetricRevenueYTD
	}
	return KpiConfig{Metric: metric}
}

// TableConfig is the "table" widget's config.
type TableConfig struct {
	Table TableKind
	Limit int
}

func decodeTable(raw map[string]any) any {
	table := TableKind(asString(raw["table"]))
	if table == "" {
		table = TableOpenInvoices
	}
	return TableConfig{Table: table, Limit: clampInt(asInt(raw["limit"], 8), 1, 50)}
}

// ChartConfig is the "chart" widget's config.
type ChartConfig struct {
	Chart  ChartKind
	Months int
}

func decodeChart(raw map[string]any) any {
	chart := ChartKind(asString(raw["chart"]))
	if chart == "" {
		chart = ChartRevenue
	}
	return ChartConfig{Chart: chart, Months: clampInt(asInt(raw["months"], 12), 3, 24)}
}

// ProgressConfig is the "progress" widget's config.
type ProgressConfig struct{ Goal bool }

func decodeProgress(raw map[string]any) any {
	goal := true
	if v, ok := raw["goal"]; ok {
		goal = asBool(v)
	}
	return ProgressConfig{Goal: goal}
}

// HintsConfig is the "hints" widget's config.
type HintsConfig struct {
	Sources     []string
	MinSeverity int
	Limit       int
}

func decodeHints(raw map[string]any) any {
	minSeverity := clampInt(asInt(raw["min_severity"], int(enums.SeverityInfo)), int(enums.SeverityInfo), int(enums.SeverityCritical))
	return HintsConfig{Sources: asStringList(raw["sources"]), MinSeverity: minSeverity, Limit: clampInt(asInt(raw["limit"], 8), 1, 50)}
}

// TrendConfig is the "trend" widget's config.
type TrendConfig struct {
	Metric TrendMetric
	Days   int
}

func decodeTrend(raw map[string]any) any {
	metric := TrendMetric(asString(raw["metric"]))
	if metric == "" {
		metric = TrendOpenAmount
	}
	return TrendConfig{Metric: metric, Days: clampInt(asInt(raw["days"], 90), 7, 730)}
}

// DeadlinesConfig is the "deadlines" widget's config.
type DeadlinesConfig struct{ Days int }

func decodeDeadlines(raw map[string]any) any {
	return DeadlinesConfig{Days: clampInt(asInt(raw["days"], 45), 7, 400)}
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func settingsMap(settings map[string]any, key string) map[string]any {
	m, _ := settings[key].(map[string]any)
	return m
}

func settingsFloat(m map[string]any, key string, def float64) float64 {
	if v, ok := m[key]; ok {
		if f := asFloat(v); f != 0 {
			return f
		}
	}
	return def
}

func parseToday(iso string) time.Time {
	if t, ok := metrics.ParseDay(iso); ok {
		return t
	}
	return time.Now().UTC()
}

// ── KPI ──

// KpiResult is one "kpi" widget's computed value plus an optional
// translated sub-caption (see the widgets/kpi.html template).
type KpiResult struct {
	Kind     string // "money", "percent", "hours", "count"
	Value    float64
	Currency string
	HasDelta bool
	Delta    float64
	SubKey   string // "" if none
	SubHours float64
	SubCount int
	SubIn    float64 // liquidity: expected income
	SubOut   float64 // liquidity: fixed costs
	SubStart string
	SubEnd   string
	SubGoal  float64
	SubRate  int
}

func kpiKimai(metric Metric, data *sources.KimaiDataset, ctx ViewCtx) *KpiResult {
	hoursPerDay := settingsFloat(settingsMap(ctx.Settings, "goals"), "hours_per_day", 0)
	stats := metrics.KimaiSummaryOf(data, parseToday(ctx.Today), hoursPerDay)
	switch metric {
	case MetricHoursToday:
		return &KpiResult{Kind: "hours", Value: float64(stats.TodayMin) / minutesPerHourInsight}
	case MetricHoursWeek:
		return &KpiResult{Kind: "hours", Value: float64(stats.WeekMin) / minutesPerHourInsight}
	case MetricHoursMonth:
		return &KpiResult{Kind: "hours", Value: float64(stats.MonthMin) / minutesPerHourInsight}
	case MetricUtilization:
		if stats.Utilization == nil {
			return nil
		}
		return &KpiResult{Kind: "percent", Value: *stats.Utilization, SubKey: "kpi.of_target",
			SubHours: float64(stats.TargetMonthMin) / minutesPerHourInsight}
	case MetricUnbilled:
		var amount float64
		var minutes int
		for _, g := range stats.Unbilled {
			amount += g.Amount
			minutes += g.Minutes
		}
		return &KpiResult{Kind: "money", Value: amount, SubKey: "kpi.hours", SubHours: float64(minutes) / minutesPerHourInsight}
	}
	return nil
}

func kpiNinja(metric Metric, data *sources.NinjaDataset, peers map[string]any, ctx ViewCtx) *KpiResult {
	tax := settingsMap(ctx.Settings, "tax")
	interval := metrics.TaxVATInterval(ctx.Settings)
	method := metrics.TaxVATMethod(ctx.Settings)
	today := parseToday(ctx.Today)
	stats := metrics.NinjaSummaryOf(data, today, interval, method)

	switch metric {
	case MetricRevenueYTD:
		if stats.RevenuePrevYTD == 0 {
			return &KpiResult{Kind: "money", Value: stats.RevenueYTD, Currency: stats.Currency}
		}
		delta := (stats.RevenueYTD - stats.RevenuePrevYTD) / stats.RevenuePrevYTD
		return &KpiResult{Kind: "money", Value: stats.RevenueYTD, Currency: stats.Currency, HasDelta: true, Delta: delta}
	case MetricRevenueMonth:
		return &KpiResult{Kind: "money", Value: stats.RevenueMonth, Currency: stats.Currency}
	case MetricOpenAmount:
		return &KpiResult{Kind: "money", Value: stats.OpenAmount, Currency: stats.Currency,
			SubKey: "kpi.invoices", SubCount: len(stats.Open)}
	case MetricOverdueAmount:
		var amount float64
		for _, i := range stats.Overdue {
			amount += i.Balance
		}
		return &KpiResult{Kind: "money", Value: amount, Currency: stats.Currency,
			SubKey: "kpi.invoices", SubCount: len(stats.Overdue)}
	case MetricVATLiability:
		return &KpiResult{Kind: "money", Value: stats.VAT.Liability, Currency: stats.Currency,
			SubKey: "kpi.vat_period", SubStart: stats.VAT.Start, SubEnd: stats.VAT.End}
	case MetricRevenueForecast:
		forecast := metrics.NinjaForecastYear(data, today)
		goal := settingsFloat(settingsMap(ctx.Settings, "goals"), "revenue_year", 0)
		result := &KpiResult{Kind: "money", Value: forecast, Currency: stats.Currency}
		if goal > 0 {
			result.SubKey, result.SubGoal = "kpi.of_goal", goal
		}
		return result
	case MetricCash30:
		return &KpiResult{Kind: "money", Value: metrics.NinjaCashExpected(data, today, 30), Currency: stats.Currency,
			SubKey: "kpi.cash_30"}
	case MetricLiquidity30:
		// 30 days ≈ one month of fixed costs; Sure's recurring payments
		// replace the manual figure when a Sure connection exists.
		income := metrics.NinjaCashExpected(data, today, 30)
		fixed := settingsFloat(settingsMap(ctx.Settings, "costs"), "fixed_monthly", 0)
		if sure, ok := peers[peerSure].(*sources.SureDataset); ok {
			fixed = metrics.SureDue(sure, today, 30)
		}
		return &KpiResult{Kind: "money", Value: income - fixed, Currency: stats.Currency,
			SubKey: "kpi.liquidity", SubIn: income, SubOut: fixed}
	case MetricEffectiveRate:
		kimai, ok := peers[peerKimai].(*sources.KimaiDataset)
		if !ok {
			return nil
		}
		rows, overall := metrics.EffectiveRates(kimai, data, today)
		return &KpiResult{Kind: "money", Value: overall, Currency: stats.Currency, SubKey: "kpi.per_hour", SubCount: len(rows)}
	case MetricTaxReserve:
		rate := settingsFloat(tax, "income_tax_rate", defaultIncomeTaxRate)
		var expenses float64
		for _, e := range data.Expenses {
			if d, ok := metrics.ParseDay(e.Date); ok && d.Year() == today.Year() {
				expenses += e.Amount - e.Tax
			}
		}
		surplus := stats.RevenueYTD - expenses
		if surplus < 0 {
			surplus = 0
		}
		return &KpiResult{Kind: "money", Value: stats.VAT.Liability + surplus*rate, Currency: stats.Currency,
			SubKey: "kpi.reserve", SubRate: int(rate*100 + 0.5)}
	}
	return nil
}

func kpiSure(metric Metric, data *sources.SureDataset) *KpiResult {
	switch metric {
	case MetricNetWorth:
		return &KpiResult{Kind: "money", Value: data.NetWorth, Currency: data.Currency}
	case MetricCash:
		return &KpiResult{Kind: "money", Value: metrics.SureCash(data), Currency: data.Currency}
	}
	return nil
}

func kpiSnipe(metric Metric, data *sources.SnipeDataset, ctx ViewCtx) *KpiResult {
	stats := metrics.SnipeSummaryOf(data, parseToday(ctx.Today))
	switch metric {
	case MetricAssetValue:
		return &KpiResult{Kind: "money", Value: stats.Value, SubKey: "kpi.assets", SubCount: stats.Assets}
	case MetricAssetsReady:
		return &KpiResult{Kind: "count", Value: float64(stats.Ready)}
	}
	return nil
}

func kpiView(cfgAny any, results map[string]any, ctx ViewCtx) map[string]any {
	cfg := cfgAny.(KpiConfig)
	data, ok := results["data"]
	if !ok || data == nil {
		return map[string]any{}
	}
	var kpi *KpiResult
	switch enums.ServiceType(ctx.Service) {
	case enums.ServiceKimai:
		kpi = kpiKimai(cfg.Metric, data.(*sources.KimaiDataset), ctx)
	case enums.ServiceInvoiceNinja:
		kpi = kpiNinja(cfg.Metric, data.(*sources.NinjaDataset), results, ctx)
	case enums.ServiceSure:
		kpi = kpiSure(cfg.Metric, data.(*sources.SureDataset))
	case enums.ServiceSnipeIT:
		kpi = kpiSnipe(cfg.Metric, data.(*sources.SnipeDataset), ctx)
	}
	return map[string]any{"KPI": kpi, "Unsupported": kpi == nil}
}

// ── Tables ──

// Col is one table column: its header key and how to format each row's
// value at the same index.
type Col struct{ Label, Format string }

// Row is one table row; Values line up with the widget's Cols.
type Row struct{ Values []any }

func colsFor(kind TableKind) []Col {
	switch kind {
	case TableOpenInvoices:
		return []Col{{"number", "text"}, {"client", "text"}, {"due", "day"}, {"late", "late"}, {"amount", "money"}}
	case TableClientShares:
		return []Col{{"client", "text"}, {"amount", "money"}, {"share", "pct"}}
	case TableUnbilled:
		return []Col{{"customer", "text"}, {"hours", "hours"}, {"amount", "money"}, {"oldest", "day"}}
	case TableBudgets:
		return []Col{{"project", "text"}, {"used", "bar"}}
	case TableAssetDates:
		return []Col{{"name", "text"}, {"kind", "upcoming"}, {"due", "day"}}
	case TableTrips:
		return []Col{{"area", "text"}, {"day", "day"}, {"km", "km"}, {"away", "hours"}}
	case TableRates:
		return []Col{{"customer", "text"}, {"hours", "hours"}, {"amount", "money"}, {"rate", "money"}}
	case TableAppUsage:
		return []Col{{"app", "text"}, {"logins", "text"}, {"users", "text"}}
	}
	return nil
}

func tableRows(kind TableKind, results map[string]any, ctx ViewCtx) ([]Row, bool) {
	data, ok := results["data"]
	if !ok || data == nil {
		return nil, false
	}
	today := parseToday(ctx.Today)
	service := enums.ServiceType(ctx.Service)

	switch {
	case kind == TableOpenInvoices && service == enums.ServiceInvoiceNinja:
		var rows []Row
		for _, i := range metrics.NinjaOpenInvoices(data.(*sources.NinjaDataset), today) {
			rows = append(rows, Row{[]any{i.Number, i.Client, i.DueDate, i.OverdueDays, i.Balance}})
		}
		return rows, true

	case kind == TableClientShares && service == enums.ServiceInvoiceNinja:
		var rows []Row
		for _, s := range metrics.NinjaShares(data.(*sources.NinjaDataset), today) {
			rows = append(rows, Row{[]any{s.Client, s.Net, s.Share}})
		}
		return rows, true

	case kind == TableUnbilled && service == enums.ServiceKimai:
		var rows []Row
		for _, g := range metrics.KimaiUnbilled(data.(*sources.KimaiDataset), today) {
			rows = append(rows, Row{[]any{g.Customer, float64(g.Minutes) / minutesPerHourInsight, g.Amount, g.Oldest}})
		}
		return rows, true

	case kind == TableBudgets && service == enums.ServiceKimai:
		var rows []Row
		for _, b := range kimaiBudgets(data.(*sources.KimaiDataset), today) {
			rows = append(rows, Row{[]any{b.Name, b.Pct}})
		}
		return rows, true

	case kind == TableAssetDates && service == enums.ServiceSnipeIT:
		var rows []Row
		for _, i := range metrics.SnipeUpcomingDates(data.(*sources.SnipeDataset), today, 0) {
			rows = append(rows, Row{[]any{i.Name, i.Kind, i.Date}})
		}
		return rows, true

	case kind == TableRates && service == enums.ServiceInvoiceNinja:
		kimai, ok := results[peerKimai].(*sources.KimaiDataset)
		if !ok {
			return nil, false
		}
		rates, _ := metrics.EffectiveRates(kimai, data.(*sources.NinjaDataset), today)
		var rows []Row
		for _, r := range rates {
			rows = append(rows, Row{[]any{r.Customer, r.Hours, r.Net, r.Rate}})
		}
		return rows, true

	case kind == TableAppUsage && service == enums.ServiceAuthentik:
		var rows []Row
		for _, a := range data.(*sources.AuthentikDataset).Apps {
			rows = append(rows, Row{[]any{a.Name, a.Events, a.Users}})
		}
		return rows, true

	case kind == TableTrips && service == enums.ServiceDawarich:
		mapping := metrics.ParseAreaMapping(ctx.Options)
		start := metrics.AddMonths(today, -1)
		var rows []Row
		for _, t := range metrics.Trips(data.(*sources.DawarichDataset), mapping, start, today) {
			rows = append(rows, Row{[]any{t.Area, t.Day, t.KM, float64(t.AwayMin) / minutesPerHourInsight}})
		}
		return rows, true
	}
	return nil, false
}

// budgetRow is one Kimai project's budget usage.
type budgetRow struct {
	Name string
	Pct  float64
}

// kimaiBudgets mirrors the Python insight.py "_budgets" helper: money
// budgets use their running total, monthly time budgets are recomputed
// from this month's timesheets.
func kimaiBudgets(data *sources.KimaiDataset, today time.Time) []budgetRow {
	var rows []budgetRow
	for _, p := range data.Projects {
		switch {
		case p.Budget > 0:
			rows = append(rows, budgetRow{Name: p.Name, Pct: p.UsedMoney / p.Budget})
		case p.TimeBudgetMin > 0:
			used := p.UsedMinutes
			if p.BudgetType == "month" {
				start := metrics.MonthStart(today)
				used = 0
				for _, s := range data.Timesheets {
					if s.ProjectID != p.ID {
						continue
					}
					if d, ok := metrics.ParseDay(s.Begin); ok && !d.Before(start) {
						used += s.Minutes
					}
				}
			}
			rows = append(rows, budgetRow{Name: p.Name, Pct: float64(used) / float64(p.TimeBudgetMin)})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Pct > rows[j].Pct })
	return rows
}

func tableView(cfgAny any, results map[string]any, ctx ViewCtx) map[string]any {
	cfg := cfgAny.(TableConfig)
	rows, ok := tableRows(cfg.Table, results, ctx)
	if !ok {
		if _, hasData := results["data"]; hasData {
			return map[string]any{"Unsupported": true}
		}
		return map[string]any{}
	}
	total := len(rows)
	if total > cfg.Limit {
		rows = rows[:cfg.Limit]
	}
	return map[string]any{"Cols": colsFor(cfg.Table), "Rows": rows, "Total": total, "More": total - len(rows)}
}

// ── Chart ──

const (
	chartW, chartH, chartPad = 1000.0, 200.0, 10.0
)

// Bar is one chart bar. Geometry (Prev*/Value*) is pre-computed here, not
// in the template, since SVG coordinates must never pick up a locale's
// thousands separator the way money()/num() would apply one.
type Bar struct {
	Label, AxisLabel               string
	Value, Prev                    float64
	PrevX, PrevY, PrevW, PrevH     string
	ValueX, ValueY, ValueW, ValueH string
	LabelX                         string
}

// barSeries is one bar's raw numbers, before geometry is computed.
type barSeries struct {
	Label       string
	Value, Prev float64
}

func barsFrom(raw []barSeries) []Bar {
	top := 1.0
	for _, r := range raw {
		top = maxF(top, r.Value, r.Prev)
	}
	step := (chartW - chartPad) / float64(len(raw))

	bars := make([]Bar, len(raw))
	for i, r := range raw {
		x := chartPad + float64(i)*step
		hp, hv := r.Prev/top*(chartH-chartPad), r.Value/top*(chartH-chartPad)
		bar := Bar{
			Label: r.Label, Value: r.Value, Prev: r.Prev,
			PrevX: fnum(x + step*0.1), PrevY: fnum(chartH - hp), PrevW: fnum(step * 0.35), PrevH: fnum(hp),
			ValueX: fnum(x + step*0.47), ValueY: fnum(chartH - hv), ValueW: fnum(step * 0.4), ValueH: fnum(hv),
			LabelX: fnum(x + step/2),
		}
		if i%2 == 0 && len(r.Label) >= 7 {
			bar.AxisLabel = r.Label[5:] + "/" + r.Label[2:4]
		}
		bars[i] = bar
	}
	return bars
}

func maxF(vals ...float64) float64 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

func fnum(f float64) string { return fmt.Sprintf("%.1f", f) }

func chartView(cfgAny any, results map[string]any, ctx ViewCtx) map[string]any {
	cfg := cfgAny.(ChartConfig)
	data, ok := results["data"]
	if !ok || data == nil {
		return map[string]any{}
	}
	today := parseToday(ctx.Today)
	service := enums.ServiceType(ctx.Service)

	switch {
	case cfg.Chart == ChartRevenue && service == enums.ServiceInvoiceNinja:
		var raw []barSeries
		for _, m := range metrics.NinjaByMonth(data.(*sources.NinjaDataset), today, cfg.Months) {
			raw = append(raw, barSeries{m.Month, m.Net, m.Prev})
		}
		return map[string]any{"Bars": barsFrom(raw), "Unit": "money"}

	case cfg.Chart == ChartSeason && service == enums.ServiceInvoiceNinja:
		var raw []barSeries
		for _, m := range metrics.NinjaSeasonal(data.(*sources.NinjaDataset), today, cfg.Months) {
			raw = append(raw, barSeries{m.Month, m.Net, m.Prev})
		}
		return map[string]any{"Bars": barsFrom(raw), "Unit": "money", "PrevKey": "chart.season_avg"}

	case cfg.Chart == ChartHours && service == enums.ServiceKimai:
		kdata := data.(*sources.KimaiDataset)
		var raw []barSeries
		for back := cfg.Months - 1; back >= 0; back-- {
			start := metrics.AddMonths(today, -back)
			end := metrics.AddMonths(start, 1).AddDate(0, 0, -1)
			prevStart := time.Date(start.Year()-1, start.Month(), 1, 0, 0, 0, 0, time.UTC)
			prevEnd := metrics.AddMonths(prevStart, 1).AddDate(0, 0, -1)
			raw = append(raw, barSeries{
				start.Format("2006-01"),
				float64(metrics.KimaiMinutesBetween(kdata, start, end, metrics.HoursAll)) / minutesPerHourInsight,
				float64(metrics.KimaiMinutesBetween(kdata, prevStart, prevEnd, metrics.HoursAll)) / minutesPerHourInsight,
			})
		}
		return map[string]any{"Bars": barsFrom(raw), "Unit": "hours"}
	}
	return map[string]any{"Unsupported": true}
}

// ── Progress (budgets + revenue goal) ──

// ProgressItem is one meter: either a Kimai budget or the revenue-goal bar.
type ProgressItem struct {
	LabelKey string // "" if Label is used instead
	Label    string
	Pct      float64
	HasGoal  bool
	Value    float64
	Goal     float64
}

func progressView(cfgAny any, results map[string]any, ctx ViewCtx) map[string]any {
	cfg := cfgAny.(ProgressConfig)
	data, ok := results["data"]
	if !ok || data == nil {
		return map[string]any{}
	}
	today := parseToday(ctx.Today)
	var items []ProgressItem

	if enums.ServiceType(ctx.Service) == enums.ServiceKimai {
		for _, b := range kimaiBudgets(data.(*sources.KimaiDataset), today) {
			items = append(items, ProgressItem{Label: b.Name, Pct: b.Pct})
		}
	}
	goal := settingsFloat(settingsMap(ctx.Settings, "goals"), "revenue_year", 0)
	if enums.ServiceType(ctx.Service) == enums.ServiceInvoiceNinja && cfg.Goal && goal > 0 {
		ytd := metrics.NinjaSummaryOf(data.(*sources.NinjaDataset), today, "", "").RevenueYTD
		items = append(items, ProgressItem{LabelKey: "progress.revenue_goal", Pct: ytd / goal, HasGoal: true, Value: ytd, Goal: goal})
	}
	return map[string]any{"Items": items}
}

// ── Deadlines ──

func deadlinesView(cfgAny any, _ map[string]any, ctx ViewCtx) map[string]any {
	cfg := cfgAny.(DeadlinesConfig)
	tax, configured := metrics.ParseTaxSettings(ctx.Settings)
	today := parseToday(ctx.Today)

	var items []map[string]any
	if configured {
		for _, d := range metrics.UpcomingDeadlines(tax, today, cfg.Days) {
			items = append(items, map[string]any{
				"Kind": d.Kind, "Due": d.Due.Format("2006-01-02"), "Left": int(d.Due.Sub(today).Hours() / 24),
				"Period": d.Period, "Year": d.Year, "Amount": d.Amount,
			})
		}
	}
	return map[string]any{"Items": items, "Configured": configured}
}

// ── Trend ──

const (
	trendWidth  = 1000
	trendHeight = 160
)

func trendView(cfgAny any, results map[string]any, _ ViewCtx) map[string]any {
	cfg := cfgAny.(TrendConfig)
	points, _ := results["points"].([][2]any)
	if len(points) < 2 {
		return map[string]any{"Count": len(points)}
	}

	values := make([]float64, len(points))
	for i, p := range points {
		values[i] = asFloat(p[1])
		if cfg.Metric == TrendMonthMinute {
			values[i] /= minutesPerHourInsight
		}
	}
	low, high := values[0], values[0]
	for _, v := range values {
		if v < low {
			low = v
		}
		if v > high {
			high = v
		}
	}
	span := high - low
	if span == 0 {
		span = 1
	}
	step := float64(trendWidth) / float64(len(values)-1)

	coords := make([]string, len(values))
	for i, v := range values {
		x := float64(i) * step
		y := trendHeight - (v-low)/span*(trendHeight-10) - 5
		coords[i] = formatPoint(x, y)
	}
	unit := "money"
	if cfg.Metric == TrendMonthMinute {
		unit = "hours"
	}
	return map[string]any{
		"Path": "M" + joinPoints(coords), "First": points[0][0], "Last": points[len(points)-1][0],
		"Low": low, "High": high, "Now": values[len(values)-1], "Unit": unit, "W": trendWidth, "H": trendHeight,
	}
}

func formatPoint(x, y float64) string { return fmt.Sprintf("%.1f,%.1f", x, y) }

func joinPoints(coords []string) string {
	out := coords[0]
	for _, c := range coords[1:] {
		out += " L" + c
	}
	return out
}

func dataQuery(any) []Query {
	return []Query{{Name: "data", Source: "data", Conn: ConnWidget}}
}

// peerKimai names the space's Kimai dataset for rate views.
const peerKimai = "kimai"

var kimaiPeer = Query{Name: peerKimai, Source: "data", Conn: ConnPeer, Service: enums.ServiceKimai}

// peerSure names the space's Sure dataset (recurring costs).
const peerSure = "sure"

var surePeer = Query{Name: peerSure, Source: "data", Conn: ConnPeer, Service: enums.ServiceSure}

func kpiQueries(cfg any) []Query {
	switch cfg.(KpiConfig).Metric {
	case MetricEffectiveRate:
		return append(dataQuery(nil), kimaiPeer)
	case MetricLiquidity30:
		return append(dataQuery(nil), surePeer)
	}
	return dataQuery(nil)
}

func tableQueries(cfg any) []Query {
	if cfg.(TableConfig).Table == TableRates {
		return append(dataQuery(nil), kimaiPeer)
	}
	return dataQuery(nil)
}

func init() {
	Register(WidgetType{Key: "kpi", Decode: decodeKpi, Template: "widgets/kpi", Category: CategoryInsight,
		RefreshS: 600, Queries: kpiQueries, View: kpiView})
	Register(WidgetType{Key: "table", Decode: decodeTable, Template: "widgets/table", Category: CategoryInsight,
		RefreshS: 600, Queries: tableQueries, View: tableView})
	Register(WidgetType{Key: "chart", Decode: decodeChart, Template: "widgets/chart", Category: CategoryInsight,
		RefreshS: 3600, Queries: dataQuery, View: chartView})
	Register(WidgetType{Key: "progress", Decode: decodeProgress, Template: "widgets/progress", Category: CategoryInsight,
		RefreshS: 600, Queries: dataQuery, View: progressView})
	Register(WidgetType{Key: "deadlines", Decode: decodeDeadlines, Template: "widgets/deadlines", Category: CategoryInsight,
		RefreshS: 3600, View: deadlinesView})
	Register(WidgetType{Key: "trend", Decode: decodeTrend, Template: "widgets/trend", Category: CategoryInsight,
		RefreshS: 3600, View: trendView, Extra: ExtraPoints})
	Register(WidgetType{Key: "hints", Decode: decodeHints, Template: "widgets/hints", Category: CategoryInsight,
		RefreshS: 300, Extra: ExtraHints})
}
