package widgets

// "glances_chart": one Glances metric over the last minutes, as a line.

import (
	"andon/internal/enums"
	"andon/internal/sources"
)

const (
	defaultGlancesMetric = "cpu"
	defaultGlancesPoints = 60
	chartMargin          = 5
)

type GlancesChartConfig struct {
	Metric string
	Points int
}

func decodeGlancesChart(raw map[string]any) any {
	metric := asString(raw["metric"])
	if _, ok := sources.GlancesMetrics[metric]; !ok {
		metric = defaultGlancesMetric
	}
	return GlancesChartConfig{Metric: metric, Points: clampInt(asInt(raw["points"], defaultGlancesPoints), 10, 300)}
}

// glancesChartView scales samples into the trend chart's box; percent
// metrics keep a fixed 0–100 axis so a quiet host looks quiet.
// glancesBars caps the bars of the load chart.
const glancesBars = 36

func glancesChartView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["history"].(*sources.GlancesHistory)
	if !ok || len(data.Samples) < 2 {
		return map[string]any{}
	}
	low, high := 0.0, 100.0
	now := data.Samples[len(data.Samples)-1].Value
	if data.Metric == "load" {
		high = 0
		for _, s := range data.Samples {
			high = max(high, s.Value)
		}
		high = max(high, 1)
	}

	step := float64(trendWidth) / float64(len(data.Samples)-1)
	coords := make([]string, len(data.Samples))
	for i, s := range data.Samples {
		y := trendHeight - (min(s.Value, high)-low)/(high-low)*(trendHeight-2*chartMargin) - chartMargin
		coords[i] = formatPoint(float64(i)*step, y)
	}
	values := make([]float64, len(data.Samples))
	for i, s := range data.Samples {
		values[i] = s.Value
	}
	return map[string]any{"Path": "M" + joinPoints(coords), "Bars": barsOf(values, glancesBars, high), "Now": now, "High": high, "Metric": data.Metric,
		"W": trendWidth, "H": trendHeight, "First": data.Samples[0].At, "Last": data.Samples[len(data.Samples)-1].At}
}

func init() {
	Register(WidgetType{Key: "glances_chart", Decode: decodeGlancesChart, Template: "widgets/glances_chart",
		Category: CategoryStart, Service: enums.ServiceGlances, RefreshS: 60, Live: true, View: glancesChartView,
		Queries: func(c any) []Query {
			cfg := c.(GlancesChartConfig)
			return []Query{{Name: "history", Source: "glances_history", Conn: ConnWidget,
				Params: map[string]any{"metric": cfg.Metric, "points": float64(cfg.Points)}}}
		}})
}
