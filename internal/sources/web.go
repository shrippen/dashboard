// Package sources: generic web sources shared by start widgets — status
// checks, feeds, weather, system stats, public IP. Ports app/sources/web.py.
package sources

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"dashboard/internal/drivers/httpclient"
	"dashboard/internal/drivers/services"
	"dashboard/internal/enums"
)

const (
	httpStatusTTL = 5 * time.Minute
	feedTTL       = 30 * time.Minute
	weatherTTL    = 30 * time.Minute
	glancesTTL    = time.Minute
	publicIPTTL   = time.Hour

	httpOKMin    = 200
	httpOKMax    = 399
	feedLimitMax = 50
	openMeteoURL = "https://api.open-meteo.com/v1/forecast"
	publicIPURL  = "https://api.ipify.org"
	forecastDays = 4
)

// ── http_status ──

// HTTPStatusResult is one link's reachability check.
type HTTPStatusResult struct {
	Up    bool
	Code  int
	Ms    int
	Error string // "" if the request itself succeeded (Up may still be false)
}

// Outcome is (up, response ms) for background bookkeeping; ms only
// counts when a response came back.
func (r *HTTPStatusResult) Outcome() (bool, int) {
	if r.Error != "" || !r.Up {
		return false, 0
	}
	return true, r.Ms
}

type HTTPStatusSource struct{}

func (HTTPStatusSource) Key() string                { return "http_status" }
func (HTTPStatusSource) TTL() time.Duration         { return httpStatusTTL }
func (HTTPStatusSource) Service() enums.ServiceType { return "" }

// Fetch checks a URL's reachability. A failed check is data, not an error
// — the widget shows "down", it doesn't fail to load.
func (HTTPStatusSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	target := asStr(sctx.Params["url"])
	accept, _ := sctx.Params["accept"].([]int)
	insecure, _ := sctx.Params["insecure"].(bool)
	headers, _ := sctx.Params["headers"].(map[string]string)

	started := time.Now()
	resp, err := httpclient.Request(ctx, "GET", target, httpclient.Options{SkipVerify: insecure, Headers: headers})
	if err != nil {
		msg := "egress"
		var denied httpclient.EgressDenied
		if !errors.As(err, &denied) {
			msg = err.Error()
		}
		return &HTTPStatusResult{Error: msg}, nil
	}
	defer resp.Body.Close()

	ms := int(time.Since(started).Milliseconds())
	code := resp.StatusCode
	up := code >= httpOKMin && code <= httpOKMax
	if len(accept) > 0 {
		up = false
		for _, want := range accept {
			if want == code {
				up = true
				break
			}
		}
	}
	return &HTTPStatusResult{Up: up, Code: code, Ms: ms}, nil
}

// ── rss ──

// FeedItem is one entry of a parsed feed.
type FeedItem struct {
	Title     string
	Link      string
	Published string // ISO 8601, "" if unknown
	Summary   string
}

// FeedResult is a parsed RSS/Atom feed.
type FeedResult struct {
	Title string
	Items []FeedItem
}

type FeedSource struct{}

func (FeedSource) Key() string                { return "rss" }
func (FeedSource) TTL() time.Duration         { return feedTTL }
func (FeedSource) Service() enums.ServiceType { return "" }

func (FeedSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	target := asStr(sctx.Params["url"])
	resp, err := httpclient.Request(ctx, "GET", target, httpclient.Options{})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	defer resp.Body.Close()

	parsed, err := parseFeed(resp.Body)
	if err != nil {
		return nil, newSourceError("invalid feed")
	}

	limit := int(asFloat(sctx.Params["limit"]))
	if limit <= 0 {
		limit = 8
	}
	if limit > feedLimitMax {
		limit = feedLimitMax
	}
	if len(parsed.Items) > limit {
		parsed.Items = parsed.Items[:limit]
	}
	return parsed, nil
}

// ── open_meteo ──

// WeatherDay is one forecast day.
type WeatherDay struct {
	Day      string
	Code     int
	Max, Min float64
}

// WeatherResult is the current conditions plus a short forecast.
type WeatherResult struct {
	Temp, Wind float64
	Code       int
	IsDay      bool
	Days       []WeatherDay
}

type WeatherSource struct{}

func (WeatherSource) Key() string                { return "open_meteo" }
func (WeatherSource) TTL() time.Duration         { return weatherTTL }
func (WeatherSource) Service() enums.ServiceType { return "" }

func (WeatherSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	params := url.Values{
		"latitude":      {strconv.FormatFloat(asFloat(sctx.Params["lat"]), 'f', -1, 64)},
		"longitude":     {strconv.FormatFloat(asFloat(sctx.Params["lon"]), 'f', -1, 64)},
		"current":       {"temperature_2m,weather_code,wind_speed_10m,is_day"},
		"daily":         {"weather_code,temperature_2m_max,temperature_2m_min"},
		"timezone":      {"auto"},
		"forecast_days": {strconv.Itoa(forecastDays)},
	}
	body, _, err := httpclient.GetJSON(ctx, openMeteoURL, httpclient.Options{Params: params})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}

	raw := asMap(body)
	current := asMap(raw["current"])
	daily := asMap(raw["daily"])
	days := asList(daily["time"])
	codes := asList(daily["weather_code"])
	highs := asList(daily["temperature_2m_max"])
	lows := asList(daily["temperature_2m_min"])

	var forecast []WeatherDay
	for i := range days {
		if i >= len(codes) || i >= len(highs) || i >= len(lows) {
			break
		}
		forecast = append(forecast, WeatherDay{
			Day: asStr(days[i]), Code: int(asFloat(codes[i])), Max: asFloat(highs[i]), Min: asFloat(lows[i]),
		})
	}

	isDay := true
	if v, ok := current["is_day"]; ok {
		isDay = asFloat(v) != 0
	}
	return &WeatherResult{
		Temp: asFloat(current["temperature_2m"]), Wind: asFloat(current["wind_speed_10m"]),
		Code: int(asFloat(current["weather_code"])), IsDay: isDay, Days: forecast,
	}, nil
}

// ── glances ──

// GlancesDisk is one mounted filesystem's usage.
type GlancesDisk struct {
	Mount   string
	Percent float64
}

// GlancesResult is a host's current load, reported by its Glances agent.
type GlancesResult struct {
	CPU, Mem, Swap, Load float64
	Disks                []GlancesDisk
}

type GlancesSource struct{}

func (GlancesSource) Key() string                { return "glances" }
func (GlancesSource) TTL() time.Duration         { return glancesTTL }
func (GlancesSource) Service() enums.ServiceType { return enums.ServiceGlances }

func (GlancesSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	api := services.GlancesApi{URL: sctx.URL, Token: sctx.Secret, Verify: sctx.VerifyTLS, Version: int(asFloat(sctx.Options["api_version"]))}

	quick, err := api.Get(ctx, "quicklook")
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	disks, err := api.Get(ctx, "fs")
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	load, err := api.Get(ctx, "load")
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}

	quickM, loadM := asMap(quick), asMap(load)
	var mounts []GlancesDisk
	for _, d := range asList(disks) {
		m := asMap(d)
		mounts = append(mounts, GlancesDisk{Mount: asStr(m["mnt_point"]), Percent: asFloat(m["percent"])})
	}
	return &GlancesResult{
		CPU: asFloat(quickM["cpu"]), Mem: asFloat(quickM["mem"]), Swap: asFloat(quickM["swap"]),
		Load: asFloat(loadM["min5"]), Disks: mounts,
	}, nil
}

// ── public_ip ──

// PublicIPResult is the container's outbound public IP.
type PublicIPResult struct{ IP string }

type PublicIPSource struct{}

func (PublicIPSource) Key() string                { return "public_ip" }
func (PublicIPSource) TTL() time.Duration         { return publicIPTTL }
func (PublicIPSource) Service() enums.ServiceType { return "" }

func (PublicIPSource) Fetch(ctx context.Context, _ Ctx) (any, error) {
	body, _, err := httpclient.GetJSON(ctx, publicIPURL, httpclient.Options{Params: url.Values{"format": {"json"}}})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	return &PublicIPResult{IP: asStr(asMap(body)["ip"])}, nil
}

func init() {
	Register(HTTPStatusSource{})
	Register(FeedSource{})
	Register(WeatherSource{})
	Register(GlancesSource{})
	Register(PublicIPSource{})
}
