package widgets

// Widgets added for Dashy imports: image, exchange rates, and the
// monitor list of an Uptime Kuma connection.

import (
	"dashboard/internal/enums"
	"dashboard/internal/sources"
)

const (
	defaultImageHeight = 240
	defaultRatesBase   = "EUR"
)

// ImageConfig is the "image" widget's config; Link is "" for none.
type ImageConfig struct {
	URL    string
	Height int
	Link   string
}

func decodeImage(raw map[string]any) any {
	return ImageConfig{URL: webURL(raw["url"]), Height: clampInt(asInt(raw["height"], defaultImageHeight), 40, 1200),
		Link: webURL(raw["link"])}
}

// RatesConfig is the "rates" widget's config.
type RatesConfig struct {
	Base    string
	Symbols []string
}

func decodeRates(raw map[string]any) any {
	base := asString(raw["base"])
	if base == "" {
		base = defaultRatesBase
	}
	return RatesConfig{Base: base, Symbols: asStringList(raw["symbols"])}
}

// MonitorRow is one Kuma monitor with its pill state and label key.
type MonitorRow struct {
	Name, State, Key string
}

// kumaStates maps monitor_status to pill state and "kuma.<key>" label.
var kumaStates = map[int][2]string{
	sources.KumaDown:        {"fail", "down"},
	sources.KumaUp:          {"ok", "up"},
	sources.KumaPending:     {"warn", "pending"},
	sources.KumaMaintenance: {"warn", "maintenance"},
}

// monitorsView lists down monitors first, then the rest by name.
func monitorsView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.KumaDataset)
	if !ok {
		return map[string]any{}
	}
	var down, rest []MonitorRow
	for _, m := range data.Monitors {
		state := kumaStates[m.Status]
		row := MonitorRow{Name: m.Name, State: state[0], Key: "kuma." + state[1]}
		if m.Status == sources.KumaDown {
			down = append(down, row)
			continue
		}
		rest = append(rest, row)
	}
	return map[string]any{"Monitors": append(down, rest...)}
}

func init() {
	Register(WidgetType{Key: "image", Decode: decodeImage, Template: "widgets/image",
		Category: CategoryStart, RefreshS: 60 * 60, Queries: func(cfgAny any) []Query {
			return []Query{{Name: "image", Source: "image", Params: map[string]any{"url": cfgAny.(ImageConfig).URL}}}
		}})

	Register(WidgetType{Key: "rates", Decode: decodeRates, Template: "widgets/rates",
		Category: CategoryStart, RefreshS: 6 * 60 * 60, Queries: func(cfgAny any) []Query {
			cfg := cfgAny.(RatesConfig)
			return []Query{{Name: "rates", Source: "exchange_rates", Params: map[string]any{"base": cfg.Base, "symbols": cfg.Symbols}}}
		}})

	Register(WidgetType{Key: "monitors", Decode: decodeEmpty, Template: "widgets/monitors",
		Category: CategoryStart, Service: enums.ServiceUptimeKuma, RefreshS: 60, Live: true, Queries: dataQuery, View: monitorsView})
}
