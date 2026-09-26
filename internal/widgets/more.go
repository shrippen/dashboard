package widgets

// Start widgets without an own service (Dashy's widget list): calendar,
// custom JSON API, static list, holidays, xkcd, NASA picture, jokes,
// crypto, stocks, flights and public transport.

import (
	"strconv"
	"strings"
	"time"

	"andon/internal/sources"
)

const (
	defaultListLimit = 8
	defaultCalDays   = 14
	maxCalDays       = 90
	defaultCountry   = "DE"
	defaultCurrency  = "eur"
	timeOfDay        = "15:04"
	pathSep          = "."
	fieldSep         = "="
)

// ── configs ──

// CalendarConfig: URL is a private iCal address, stored sealed.
type CalendarConfig struct {
	URL   string
	Days  int
	Limit int
}

func decodeCalendar(raw map[string]any) any {
	return CalendarConfig{URL: webURL(raw["ical_url"]), Days: clampInt(asInt(raw["days"], defaultCalDays), 1, maxCalDays),
		Limit: clampInt(asInt(raw["limit"], defaultListLimit), 1, 50)}
}

// APIField is one value picked from a JSON body: "Temp = main.temp".
type APIField struct {
	Label, Path string
}

type CustomAPIConfig struct {
	URL     string
	Headers map[string]string
	Fields  []APIField
}

func decodeCustomAPI(raw map[string]any) any {
	cfg := CustomAPIConfig{URL: webURL(raw["url"]), Headers: stringMap(raw["headers"])}
	for _, line := range strings.Split(asString(raw["fields"]), "\n") {
		label, path, ok := strings.Cut(line, fieldSep)
		if !ok {
			label, path = line, line
		}
		if path = strings.TrimSpace(path); path == "" {
			continue
		}
		cfg.Fields = append(cfg.Fields, APIField{Label: strings.TrimSpace(label), Path: path})
	}
	return cfg
}

// ListEntry is one line of a static list; URL may be "".
type ListEntry struct {
	Text, URL string
}

type ListConfig struct{ Entries []ListEntry }

func decodeList(raw map[string]any) any {
	var cfg ListConfig
	for _, line := range strings.Split(asString(raw["entries"]), "\n") {
		text, link, _ := strings.Cut(line, linkSep)
		text, link = strings.TrimSpace(text), strings.TrimSpace(link)
		if text == "" && link == "" {
			continue
		}
		if !strings.HasPrefix(link, "https://") && !strings.HasPrefix(link, "http://") {
			link = ""
		}
		cfg.Entries = append(cfg.Entries, ListEntry{Text: firstNonEmpty(text, link), URL: link})
	}
	return cfg
}

type HolidaysConfig struct {
	Country, State string
	Limit          int
}

func decodeHolidays(raw map[string]any) any {
	return HolidaysConfig{Country: firstNonEmpty(strings.ToUpper(asString(raw["country"])), defaultCountry),
		State: strings.ToUpper(asString(raw["state"])), Limit: clampInt(asInt(raw["limit"], 5), 1, 30)}
}

type ApodConfig struct{ APIKey string }

func decodeApod(raw map[string]any) any { return ApodConfig{APIKey: asString(raw["api_key"])} }

type JokeConfig struct{ Category, Lang string }

func decodeJoke(raw map[string]any) any {
	return JokeConfig{Category: firstNonEmpty(asString(raw["category"]), "Any"), Lang: firstNonEmpty(asString(raw["lang"]), "de")}
}

type CryptoConfig struct {
	Coins    []string
	Currency string
}

func decodeCrypto(raw map[string]any) any {
	return CryptoConfig{Coins: asStringList(raw["coins"]), Currency: firstNonEmpty(strings.ToLower(asString(raw["currency"])), defaultCurrency)}
}

type StocksConfig struct{ Symbols []string }

func decodeStocks(raw map[string]any) any { return StocksConfig{Symbols: asStringList(raw["tickers"])} }

type FlightsConfig struct {
	Airport, Direction, APIKey string
	Limit                      int
}

func decodeFlights(raw map[string]any) any {
	direction := asString(raw["direction"])
	if direction != "Arrival" {
		direction = "Departure"
	}
	return FlightsConfig{Airport: strings.ToUpper(asString(raw["airport"])), Direction: direction,
		APIKey: asString(raw["api_key"]), Limit: clampInt(asInt(raw["limit"], defaultListLimit), 1, 30)}
}

type TransitConfig struct {
	Stop  string
	Limit int
}

func decodeTransit(raw map[string]any) any {
	return TransitConfig{Stop: asString(raw["stop"]), Limit: clampInt(asInt(raw["limit"], defaultListLimit), 1, 30)}
}

// ── views ──

// clockZone is where times of day are shown.
func clockZone() *time.Location {
	if loc, err := time.LoadLocation(defaultTimezone); err == nil {
		return loc
	}
	return time.UTC
}

// CalRow is one event as shown.
type CalRow struct {
	Day, Time, Title, Location string
}

func calendarView(cfgAny any, results map[string]any, _ ViewCtx) map[string]any {
	cfg := cfgAny.(CalendarConfig)
	data, ok := results["events"].(*sources.CalendarResult)
	if !ok {
		return map[string]any{}
	}
	zone := clockZone()
	var rows []CalRow
	for _, e := range data.Events {
		if len(rows) >= cfg.Limit {
			break
		}
		at := e.Start.In(zone)
		row := CalRow{Day: at.Format(isoDate), Title: e.Title, Location: e.Location}
		if !e.AllDay {
			row.Time = at.Format(timeOfDay)
		}
		rows = append(rows, row)
	}
	return map[string]any{"Rows": rows}
}

const isoDate = "2006-01-02"

// APIValue is one picked field; Missing when the path matched nothing.
type APIValue struct {
	Label, Value string
	Missing      bool
}

func customAPIView(cfgAny any, results map[string]any, _ ViewCtx) map[string]any {
	cfg := cfgAny.(CustomAPIConfig)
	data, ok := results["body"].(*sources.JSONResult)
	if !ok {
		return map[string]any{}
	}
	var rows []APIValue
	for _, f := range cfg.Fields {
		v, found := jsonPath(data.Body, f.Path)
		rows = append(rows, APIValue{Label: f.Label, Value: textOf(v), Missing: !found})
	}
	return map[string]any{"Rows": rows}
}

// jsonPath walks "a.b.0.c" through maps and lists.
func jsonPath(body any, path string) (any, bool) {
	cur := body
	for _, part := range strings.Split(path, pathSep) {
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			cur = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			cur = node[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// HolidayRow is one upcoming holiday with the days left.
type HolidayRow struct {
	Day, Name string
	In        int
}

func holidaysView(cfgAny any, results map[string]any, ctx ViewCtx) map[string]any {
	cfg := cfgAny.(HolidaysConfig)
	data, ok := results["days"].(*sources.HolidaysResult)
	if !ok {
		return map[string]any{}
	}
	today := parseToday(ctx.Today)
	var rows []HolidayRow
	for _, h := range data.Days {
		if len(rows) >= cfg.Limit {
			break
		}
		at, err := time.Parse(isoDate, h.Day)
		if err != nil {
			continue
		}
		rows = append(rows, HolidayRow{Day: h.Day, Name: h.Name, In: int(at.Sub(today).Hours() / hoursPerDayInsight)})
	}
	return map[string]any{"Rows": rows}
}

const hoursPerDayInsight = 24

// MoveRow is one departure/arrival as shown.
type MoveRow struct {
	Time, Line, Place, Status, Platform string
	Delay                               int
	Canceled                            bool
}

func boardView(limit int, data *sources.BoardResult) map[string]any {
	zone := clockZone()
	var rows []MoveRow
	for _, m := range data.Movements {
		if len(rows) >= limit {
			break
		}
		rows = append(rows, MoveRow{Time: m.When.In(zone).Format(timeOfDay), Line: m.Line, Place: m.Place,
			Status: m.Status, Platform: m.Platform, Delay: m.Delay, Canceled: m.Canceled})
	}
	return map[string]any{"Stop": data.Stop, "Rows": rows}
}

func flightsView(cfgAny any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["board"].(*sources.BoardResult)
	if !ok {
		return map[string]any{}
	}
	return boardView(cfgAny.(FlightsConfig).Limit, data)
}

func transitView(cfgAny any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["board"].(*sources.BoardResult)
	if !ok {
		return map[string]any{}
	}
	return boardView(cfgAny.(TransitConfig).Limit, data)
}

// ── registration ──

// one builds a single-query Queries func.
func one(name, source string, params func(cfg any) map[string]any) QueriesFunc {
	return func(cfg any) []Query {
		return []Query{{Name: name, Source: source, Params: params(cfg)}}
	}
}

func init() {
	const (
		minute = 60
		hour   = 60 * minute
	)

	Register(WidgetType{Key: "calendar", Decode: decodeCalendar, Template: "widgets/calendar", Category: CategoryStart, RefreshS: 15 * minute,
		Queries: one("events", "ical", func(c any) map[string]any {
			cfg := c.(CalendarConfig)
			return map[string]any{"url": cfg.URL, "days": float64(cfg.Days)}
		}), View: calendarView})

	Register(WidgetType{Key: "custom_api", Decode: decodeCustomAPI, Template: "widgets/custom_api", Category: CategoryStart, RefreshS: 5 * minute,
		Queries: one("body", "json_api", func(c any) map[string]any {
			cfg := c.(CustomAPIConfig)
			return map[string]any{"url": cfg.URL, "headers": cfg.Headers}
		}), View: customAPIView})

	Register(WidgetType{Key: "list", Decode: decodeList, Template: "widgets/list", Category: CategoryStart})

	Register(WidgetType{Key: "holidays", Decode: decodeHolidays, Template: "widgets/holidays", Category: CategoryStart, RefreshS: 12 * hour,
		Queries: one("days", "holidays", func(c any) map[string]any {
			cfg := c.(HolidaysConfig)
			return map[string]any{"country": cfg.Country, "state": cfg.State}
		}), View: holidaysView})

	Register(WidgetType{Key: "xkcd", Decode: decodeEmpty, Template: "widgets/picture", Category: CategoryStart, RefreshS: 6 * hour,
		Queries: one("picture", "xkcd", func(any) map[string]any { return map[string]any{} })})

	Register(WidgetType{Key: "apod", Decode: decodeApod, Template: "widgets/picture", Category: CategoryStart, RefreshS: 6 * hour,
		Queries: one("picture", "apod", func(c any) map[string]any { return map[string]any{"api_key": c.(ApodConfig).APIKey} })})

	Register(WidgetType{Key: "joke", Decode: decodeJoke, Template: "widgets/joke", Category: CategoryStart, RefreshS: hour,
		Queries: one("joke", "jokes", func(c any) map[string]any {
			cfg := c.(JokeConfig)
			return map[string]any{"category": cfg.Category, "lang": cfg.Lang}
		})})

	Register(WidgetType{Key: "crypto", Decode: decodeCrypto, Template: "widgets/crypto", Category: CategoryStart, RefreshS: 10 * minute,
		Queries: one("prices", "crypto", func(c any) map[string]any {
			cfg := c.(CryptoConfig)
			return map[string]any{"coins": cfg.Coins, "currency": cfg.Currency}
		})})

	Register(WidgetType{Key: "stocks", Decode: decodeStocks, Template: "widgets/stocks", Category: CategoryStart, RefreshS: 15 * minute,
		Queries: one("quotes", "stocks", func(c any) map[string]any { return map[string]any{"symbols": c.(StocksConfig).Symbols} })})

	Register(WidgetType{Key: "flights", Decode: decodeFlights, Template: "widgets/board", Category: CategoryStart, RefreshS: 10 * minute,
		Queries: one("board", "flights", func(c any) map[string]any {
			cfg := c.(FlightsConfig)
			return map[string]any{"airport": cfg.Airport, "direction": cfg.Direction, "api_key": cfg.APIKey}
		}), View: flightsView})

	Register(WidgetType{Key: "transit", Decode: decodeTransit, Template: "widgets/board", Category: CategoryStart, RefreshS: minute,
		Queries: one("board", "transit", func(c any) map[string]any {
			cfg := c.(TransitConfig)
			return map[string]any{"stop": cfg.Stop, "results": float64(cfg.Limit)}
		}), View: transitView})
}
