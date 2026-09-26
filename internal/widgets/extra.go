package widgets

// Widgets added for Dashy imports: image, exchange rates, and the
// monitor list of an Uptime Kuma connection.

import (
	"andon/internal/enums"
	"andon/internal/sources"
	"sort"
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

// kumaCells maps monitor_status to a strip cell state.
var kumaCells = map[int]string{sources.KumaDown: "bad", sources.KumaUp: "ok", sources.KumaPending: "mid", sources.KumaMaintenance: "none"}

// Limits of the monitors tile: problems listed, certificate warning.
const (
	monitorProblems = 4
	certSoonDays    = 30
)

// monitorsView sums up the instance in a few lines: how many are up, one
// strip cell per monitor (down first) and only the monitors that are not
// up by name.
//
//	12 / 14 online · Ø 180 ms
//	▮▮▮▮▮▮▮▮▮▮▮▮▯▯
//	NAS down · Shop pending
func monitorsView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.KumaDataset)
	if !ok {
		return map[string]any{}
	}
	mons := append([]sources.KumaMonitor(nil), data.Monitors...)
	rank := func(status int) int {
		if status == sources.KumaUp {
			return 1
		}
		return 0
	}
	sort.SliceStable(mons, func(a, b int) bool { return rank(mons[a].Status) < rank(mons[b].Status) })

	up, msSum, msCount, more := 0, 0.0, 0, 0
	var cells []StripCell
	var problems []MonitorRow
	soon := sources.KumaMonitor{CertDays: -1}
	for _, m := range mons {
		cells = append(cells, StripCell{State: kumaCells[m.Status], Title: m.Name})
		if m.Status == sources.KumaUp {
			up++
			if m.MS > 0 {
				msSum, msCount = msSum+m.MS, msCount+1
			}
		} else if len(problems) < monitorProblems {
			state := kumaStates[m.Status]
			problems = append(problems, MonitorRow{Name: m.Name, State: state[0], Key: "kuma." + state[1]})
		} else {
			more++
		}
		if m.CertDays >= 0 && m.CertDays < certSoonDays && (soon.CertDays < 0 || m.CertDays < soon.CertDays) {
			soon = m
		}
	}

	out := map[string]any{"Up": up, "Total": len(mons), "Cells": cells, "Problems": problems, "More": more}
	if msCount > 0 {
		out["AvgMS"] = msSum / float64(msCount)
	}
	if soon.CertDays >= 0 {
		out["CertName"], out["CertDays"] = soon.Name, soon.CertDays
	}
	return out
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
