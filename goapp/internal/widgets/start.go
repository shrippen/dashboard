// Package widgets: start page widgets (Dashy replacements).
package widgets

import (
	"strings"

	"dashboard/internal/enums"
)

const defaultTimezone = "Europe/Berlin"

// StatusMode selects whether a link tile checks its target's HTTP status.
type StatusMode string

const (
	StatusOff  StatusMode = "off"
	StatusHTTP StatusMode = "http"
)

func webURL(raw any) string {
	v := strings.TrimSpace(asString(raw))
	return v // scheme validation happens in the web form layer (task: editor), not here
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func asInt(v any, def int) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return def
	}
}

func asFloat(v any) float64 {
	f, _ := v.(float64)
	return f
}

func asStringList(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func asIntList(v any) []int {
	list, _ := v.([]any)
	out := make([]int, 0, len(list))
	for _, item := range list {
		out = append(out, asInt(item, 0))
	}
	return out
}

// LinkConfig is the "link" widget's config: a Dashy-style tile.
type LinkConfig struct {
	URL         string
	Description string
	Icon        string
	Target      enums.LinkTarget
	Status      StatusMode
	StatusURL   string
	Accept      []int
	Insecure    bool
	Hotkey      string
	InfoConn    string // connection key for the info line, "" if none
}

func decodeLink(raw map[string]any) any {
	target := enums.LinkTarget(asString(raw["target"]))
	if target == "" {
		target = enums.LinkNewTab
	}
	status := StatusMode(asString(raw["status"]))
	if status == "" {
		status = StatusHTTP
	}
	cfg := LinkConfig{
		URL: webURL(raw["url"]), Description: asString(raw["description"]), Icon: asString(raw["icon"]),
		Target: target, Status: status, StatusURL: webURL(raw["status_url"]),
		Accept: asIntList(raw["accept"]), Insecure: asBool(raw["insecure"]), Hotkey: asString(raw["hotkey"]),
	}
	if info, ok := raw["info"].(map[string]any); ok {
		cfg.InfoConn = asString(info["connection"])
	}
	return cfg
}

func linkQueries(cfgAny any) []Query {
	cfg := cfgAny.(LinkConfig)
	var found []Query
	if cfg.Status == StatusHTTP {
		found = append(found, Query{Name: "status", Source: "http_status", Params: map[string]any{
			"url": firstNonEmpty(cfg.StatusURL, cfg.URL), "accept": cfg.Accept, "insecure": cfg.Insecure,
		}})
	}
	if cfg.InfoConn != "" {
		found = append(found, Query{Name: "info", Source: "data", Conn: ConnInfo})
	}
	return found
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func linkView(cfgAny any, results map[string]any, ctx ViewCtx) map[string]any {
	info := results["info"]
	if info == nil || ctx.Service == "" {
		return map[string]any{}
	}
	// The actual metrics.*Info() call happens in the widgets service (it
	// knows the concrete dataset type per ctx.Service); this view only
	// passes the pre-computed parts through.
	return map[string]any{"info": info}
}

// RssConfig is the "rss" widget's config.
type RssConfig struct {
	URL     string
	Limit   int
	Summary bool
}

func decodeRss(raw map[string]any) any {
	limit := asInt(raw["limit"], 8)
	if limit < 1 {
		limit = 1
	}
	if limit > 50 {
		limit = 50
	}
	return RssConfig{URL: webURL(raw["url"]), Limit: limit, Summary: asBool(raw["summary"])}
}

// ClockConfig is the "clock" widget's config.
type ClockConfig struct {
	Timezones []string
	Seconds   bool
	Date      bool
}

func decodeClock(raw map[string]any) any {
	tz := asStringList(raw["timezones"])
	if len(tz) == 0 {
		tz = []string{defaultTimezone}
	}
	date := true
	if v, ok := raw["date"]; ok {
		date = asBool(v)
	}
	return ClockConfig{Timezones: tz, Seconds: asBool(raw["seconds"]), Date: date}
}

// WeatherConfig is the "weather" widget's config.
type WeatherConfig struct {
	Label string
	Lat   float64
	Lon   float64
}

func decodeWeather(raw map[string]any) any {
	return WeatherConfig{Label: asString(raw["label"]), Lat: asFloat(raw["lat"]), Lon: asFloat(raw["lon"])}
}

// IframeConfig is the "iframe" widget's config.
type IframeConfig struct {
	URL    string
	Height int
}

func decodeIframe(raw map[string]any) any {
	height := asInt(raw["height"], 320)
	if height < 80 {
		height = 80
	}
	if height > 2000 {
		height = 2000
	}
	return IframeConfig{URL: webURL(raw["url"]), Height: height}
}

// NoteConfig is the "note" widget's config.
type NoteConfig struct {
	Text string
}

func decodeNote(raw map[string]any) any {
	return NoteConfig{Text: asString(raw["text"])}
}

// EmptyConfig is used by widgets with no configuration (sysinfo, public_ip).
type EmptyConfig struct{}

func decodeEmpty(map[string]any) any { return EmptyConfig{} }

func init() {
	Register(WidgetType{Key: "link", Decode: decodeLink, Template: "widgets/link.html",
		Category: CategoryStart, Inline: true, Queries: linkQueries, View: linkView})

	Register(WidgetType{Key: "rss", Decode: decodeRss, Template: "widgets/rss.html",
		Category: CategoryStart, RefreshS: 30 * 60, Queries: func(cfgAny any) []Query {
			cfg := cfgAny.(RssConfig)
			return []Query{{Name: "feed", Source: "rss", Params: map[string]any{"url": cfg.URL, "limit": cfg.Limit}}}
		}})

	Register(WidgetType{Key: "clock", Decode: decodeClock, Template: "widgets/clock.html",
		Category: CategoryStart, Inline: true})

	Register(WidgetType{Key: "weather", Decode: decodeWeather, Template: "widgets/weather.html",
		Category: CategoryStart, RefreshS: 30 * 60, Queries: func(cfgAny any) []Query {
			cfg := cfgAny.(WeatherConfig)
			return []Query{{Name: "weather", Source: "open_meteo", Params: map[string]any{"lat": cfg.Lat, "lon": cfg.Lon}}}
		}})

	Register(WidgetType{Key: "iframe", Decode: decodeIframe, Template: "widgets/iframe.html",
		Category: CategoryStart, Inline: true})

	Register(WidgetType{Key: "sysinfo", Decode: decodeEmpty, Template: "widgets/sysinfo.html",
		Category: CategoryStart, Service: enums.ServiceGlances, RefreshS: 60,
		Queries: func(any) []Query { return []Query{{Name: "stats", Source: "glances", Conn: ConnWidget}} }})

	Register(WidgetType{Key: "public_ip", Decode: decodeEmpty, Template: "widgets/public_ip.html",
		Category: CategoryStart, RefreshS: 60 * 60,
		Queries: func(any) []Query { return []Query{{Name: "ip", Source: "public_ip"}} }})

	Register(WidgetType{Key: "note", Decode: decodeNote, Template: "widgets/note.html",
		Category: CategoryStart, Inline: true})
}
