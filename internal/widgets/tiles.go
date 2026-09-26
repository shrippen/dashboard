package widgets

// Data tiles drawn as bars (see dashboard.css .bars, .seg, .stack):
//
//	conn_health    every connection's last days as a strip, worst first
//	invoice_aging  open invoices stacked by how overdue they are

import (
	"sort"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

// LoadBar is one column of a load bar chart: height in percent and a colour band.
type LoadBar struct {
	H     int
	Tier  string // "", "yellow", "red"
	Title string
}

// Tier bands of percent loads (CPU, fill levels).
const (
	loadWarn = 60
	loadHigh = 80
)

func loadTier(pct float64) string {
	switch {
	case pct >= loadHigh:
		return "red"
	case pct >= loadWarn:
		return "yellow"
	default:
		return ""
	}
}

// barsOf buckets values into at most n bars (averaging neighbours) and
// scales them to high:
//
//	[10 20 30 40] n=2 high=40  →  heights 38 88
func barsOf(values []float64, n int, high float64) []LoadBar {
	if len(values) == 0 || n <= 0 || high <= 0 {
		return nil
	}
	per := max(1, (len(values)+n-1)/n)
	var out []LoadBar
	for i := 0; i < len(values); i += per {
		end := min(i+per, len(values))
		sum := 0.0
		for _, v := range values[i:end] {
			sum += v
		}
		avg := sum / float64(end-i)
		out = append(out, LoadBar{H: max(2, int(min(avg, high)/high*pctFull+0.5)), Tier: loadTier(avg / high * pctFull)})
	}
	return out
}

// ── conn_health ──

// ConnHealthSlot names the connection strips among a widget's results.
const ConnHealthSlot = "connhealth"

// Days a connection strip covers.
const ConnHealthDays = 14

// ConnHealthConfig is the "conn_health" widget's config.
type ConnHealthConfig struct {
	Limit int
}

func decodeConnHealth(raw map[string]any) any {
	return ConnHealthConfig{Limit: clampInt(asInt(raw["limit"], 4), 1, 20)}
}

// ConnStrip is one connection's strip as the services hand it over.
type ConnStrip struct {
	Name, Service string
	FailPct       int
	Days          []ConnDayState
}

// ConnDayState is one day of a strip.
type ConnDayState struct {
	Day      string
	OK, Fail int
}

// StripCell is one day as drawn: "ok", "mid" (some failures), "bad"
// (mostly failures) or "none" (no fetch).
type StripCell struct {
	State string
	Title string
}

// StripRow is one connection as drawn.
type StripRow struct {
	Name    string
	FailPct int
	Cells   []StripCell
}

func cellState(d ConnDayState) string {
	switch {
	case d.OK+d.Fail == 0:
		return "none"
	case d.Fail == 0:
		return "ok"
	case d.Fail < d.OK:
		return "mid"
	default:
		return "bad"
	}
}

// connHealthView counts healthy connections and draws the worst ones.
func connHealthView(cfgAny any, results map[string]any, _ ViewCtx) map[string]any {
	cfg := cfgAny.(ConnHealthConfig)
	strips, _ := results[ConnHealthSlot].([]ConnStrip)

	healthy := 0
	var rows []StripRow
	for _, s := range strips {
		if s.FailPct == 0 {
			healthy++
		}
		row := StripRow{Name: s.Name, FailPct: s.FailPct}
		for _, d := range s.Days {
			row.Cells = append(row.Cells, StripCell{State: cellState(d), Title: d.Day})
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(a, b int) bool { return rows[a].FailPct > rows[b].FailPct })

	var shown []StripRow
	for _, r := range rows {
		if r.FailPct > 0 && len(shown) < cfg.Limit {
			shown = append(shown, r)
		}
	}
	return map[string]any{"Healthy": healthy, "Total": len(strips), "Rows": shown, "Days": ConnHealthDays}
}

// ── invoice_aging ──

// AgingBand is one part of the stacked open-invoice bar.
type AgingBand struct {
	Key    string // catalog suffix: "current", "d30", "d60", "older"
	Tier   string
	Amount float64
	Count  int
	Pct    int
}

// agingLimits are the upper overdue days of the bands after "current".
var agingLimits = []struct {
	key, tier string
	upTo      int
}{
	{"current", "green", 0}, {"d30", "yellow", 30}, {"d60", "orange", 60}, {"older", "red", -1},
}

// invoiceAgingView stacks open invoices by days overdue:
//
//	not due 2.940 € · 1–30 d 1.240 € · 31–60 d 0 · older 0
func invoiceAgingView(_ any, results map[string]any, ctx ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.NinjaDataset)
	if !ok {
		return map[string]any{}
	}
	today, _ := time.Parse(time.DateOnly, ctx.Today)
	open := metrics.NinjaOpenInvoices(data, today)

	bands := make([]AgingBand, len(agingLimits))
	total := 0.0
	for i, l := range agingLimits {
		bands[i] = AgingBand{Key: l.key, Tier: l.tier}
	}
	for _, inv := range open {
		i := len(agingLimits) - 1
		for j, l := range agingLimits {
			if l.upTo >= 0 && inv.OverdueDays <= l.upTo {
				i = j
				break
			}
		}
		bands[i].Amount += inv.Balance
		bands[i].Count++
		total += inv.Balance
	}
	for i := range bands {
		if total > 0 {
			bands[i].Pct = int(bands[i].Amount/total*pctFull + 0.5)
		}
	}
	return map[string]any{"Total": total, "Count": len(open), "Bands": bands}
}

// ── speedtest ──

// speedView sets the last measurement against the contract.
func speedView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.SpeedtestDataset)
	if !ok || data == nil {
		return map[string]any{}
	}
	out := map[string]any{"Data": data}
	if data.ExpectDown > 0 {
		out["DownPct"] = data.Down / data.ExpectDown
	}
	if data.ExpectUp > 0 {
		out["UpPct"] = data.Up / data.ExpectUp
	}
	return out
}

func init() {
	Register(WidgetType{Key: "conn_health", Decode: decodeConnHealth, Template: "widgets/conn_health", Category: CategoryInsight,
		RefreshS: 10 * 60, View: connHealthView, Extra: ExtraConnHealth})
	Register(WidgetType{Key: "invoice_aging", Decode: decodeEmpty, Template: "widgets/invoice_aging", Category: CategoryInsight,
		Service: enums.ServiceInvoiceNinja, RefreshS: 30 * 60, Queries: dataQuery, View: invoiceAgingView})
}
