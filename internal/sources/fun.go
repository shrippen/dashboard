package sources

// Start-page sources without an own service (Dashy widgets): public
// holidays, jokes, crypto prices, stock quotes and any JSON API.
//
//	holidays   date.nager.at      /api/v3/PublicHolidays/{year}/{country}
//	jokes      v2.jokeapi.dev     /joke/{category}?lang=&safe-mode
//	crypto     api.coingecko.com  /api/v3/simple/price
//	stocks     stooq.com          /q/l/?s=aapl.us&f=sd2t2ohlcv&e=csv
//	json_api   any URL, optional (sealed) headers

import (
	"context"
	"encoding/csv"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"dashboard/internal/drivers/httpclient"
	"dashboard/internal/enums"
)

const (
	holidaysTTL = 24 * time.Hour
	jokesTTL    = time.Hour
	cryptoTTL   = 10 * time.Minute
	stocksTTL   = 15 * time.Minute
	jsonAPITTL  = 5 * time.Minute

	maxSymbols = 20
	isoDay     = "2006-01-02"

	// Joke flags never shown on a shared dashboard.
	jokeBlacklist = "nsfw,religious,political,racist,sexist,explicit"
)

// Base URLs; tests point them at local servers.
var (
	nagerBase     = "https://date.nager.at"
	jokeBase      = "https://v2.jokeapi.dev"
	coingeckoBase = "https://api.coingecko.com"
	stooqBase     = "https://stooq.com"
)

// ── holidays ──

// Holiday is one public holiday.
type Holiday struct {
	Day, Name string
}

// HolidaysResult lists upcoming holidays, soonest first.
type HolidaysResult struct{ Days []Holiday }

type HolidaysSource struct{}

func (HolidaysSource) Key() string                { return "holidays" }
func (HolidaysSource) TTL() time.Duration         { return holidaysTTL }
func (HolidaysSource) Service() enums.ServiceType { return "" }

// Fetch reads this and next year: in December the next holidays are in
// January. state ("DE-BY") adds regional holidays to the national ones.
func (HolidaysSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	country := strings.ToUpper(asStr(sctx.Params["country"]))
	state := strings.ToUpper(asStr(sctx.Params["state"]))
	today := time.Now().UTC().Format(isoDay)
	year := time.Now().UTC().Year()

	out := &HolidaysResult{}
	for _, y := range []int{year, year + 1} {
		target := nagerBase + "/api/v3/PublicHolidays/" + strconv.Itoa(y) + "/" + url.PathEscape(country)
		body, _, err := httpclient.GetJSON(ctx, target, httpclient.Options{})
		if err != nil {
			return nil, newSourceError("%s", err.Error())
		}
		for _, raw := range asList(body) {
			h := asMap(raw)
			if asStr(h["date"]) < today || !holidayApplies(h, state) {
				continue
			}
			out.Days = append(out.Days, Holiday{Day: asStr(h["date"]), Name: asStr(h["localName"])})
		}
	}
	sort.SliceStable(out.Days, func(i, j int) bool { return out.Days[i].Day < out.Days[j].Day })
	return out, nil
}

// holidayApplies: national holidays always, regional ones for state only.
func holidayApplies(h map[string]any, state string) bool {
	if asBool(h["global"]) {
		return true
	}
	for _, c := range asList(h["counties"]) {
		if asStr(c) == state {
			return true
		}
	}
	return false
}

// ── jokes ──

// Joke is a one-liner (Setup only) or setup plus punchline.
type Joke struct {
	Setup, Delivery string
}

type JokesSource struct{}

func (JokesSource) Key() string                { return "jokes" }
func (JokesSource) TTL() time.Duration         { return jokesTTL }
func (JokesSource) Service() enums.ServiceType { return "" }

func (JokesSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	category := asStr(sctx.Params["category"])
	if category == "" {
		category = "Any"
	}
	query := url.Values{"lang": {asStr(sctx.Params["lang"])}, "blacklistFlags": {jokeBlacklist}, "safe-mode": {""}}
	body, _, err := httpclient.GetJSON(ctx, jokeBase+"/joke/"+url.PathEscape(category), httpclient.Options{Params: query})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	m := asMap(body)
	if asBool(m["error"]) {
		return nil, newSourceError("%s", asStr(m["message"]))
	}
	if asStr(m["type"]) == "single" {
		return &Joke{Setup: asStr(m["joke"])}, nil
	}
	return &Joke{Setup: asStr(m["setup"]), Delivery: asStr(m["delivery"])}, nil
}

// ── crypto ──

// Coin is one cryptocurrency's price and 24 h change in percent.
type Coin struct {
	ID            string
	Price, Change float64
}

// CryptoResult lists coins in the configured order.
type CryptoResult struct {
	Currency string
	Coins    []Coin
}

type CryptoSource struct{}

func (CryptoSource) Key() string                { return "crypto" }
func (CryptoSource) TTL() time.Duration         { return cryptoTTL }
func (CryptoSource) Service() enums.ServiceType { return "" }

func (CryptoSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	ids := limitList(sctx.Params["coins"])
	currency := strings.ToLower(asStr(sctx.Params["currency"]))
	query := url.Values{"ids": {strings.Join(ids, ",")}, "vs_currencies": {currency}, "include_24hr_change": {"true"}}
	body, _, err := httpclient.GetJSON(ctx, coingeckoBase+"/api/v3/simple/price", httpclient.Options{Params: query})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	prices := asMap(body)
	out := &CryptoResult{Currency: strings.ToUpper(currency)}
	for _, id := range ids {
		p, ok := prices[id]
		if !ok {
			continue
		}
		m := asMap(p)
		out.Coins = append(out.Coins, Coin{ID: id, Price: asFloat(m[currency]), Change: asFloat(m[currency+"_24h_change"])})
	}
	return out, nil
}

func limitList(v any) []string {
	list, _ := v.([]string)
	if len(list) > maxSymbols {
		list = list[:maxSymbols]
	}
	out := make([]string, 0, len(list))
	for _, s := range list {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ── stocks ──

// Quote is one stock's last price and change since the open, in percent.
type Quote struct {
	Symbol, Day   string
	Close, Change float64
}

type StocksResult struct{ Quotes []Quote }

type StocksSource struct{}

func (StocksSource) Key() string                { return "stocks" }
func (StocksSource) TTL() time.Duration         { return stocksTTL }
func (StocksSource) Service() enums.ServiceType { return "" }

// Stooq CSV columns for f=sd2t2ohlcv.
const (
	colSymbol = iota
	colDate
	colTime
	colOpen
	colHigh
	colLow
	colClose
	stooqCols
)

// Fetch reads stooq's free CSV quotes; symbols carry a market suffix
// ("aapl.us", "sap.de"). Unknown symbols come back as "N/D" and are skipped.
func (StocksSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	symbols := limitList(sctx.Params["symbols"])
	query := url.Values{"s": {strings.Join(symbols, ",")}, "f": {"sd2t2ohlcv"}, "h": {""}, "e": {"csv"}}
	text, err := httpclient.GetText(ctx, stooqBase+"/q/l/", httpclient.Options{Params: query})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	rows, err := csv.NewReader(strings.NewReader(text)).ReadAll()
	if err != nil {
		return nil, newSourceError("invalid CSV")
	}

	out := &StocksResult{}
	for i, row := range rows {
		if i == 0 || len(row) < stooqCols {
			continue
		}
		open, err1 := strconv.ParseFloat(row[colOpen], 64)
		last, err2 := strconv.ParseFloat(row[colClose], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		q := Quote{Symbol: strings.ToUpper(row[colSymbol]), Day: row[colDate], Close: last}
		if open != 0 {
			q.Change = (last - open) / open * percent
		}
		out.Quotes = append(out.Quotes, q)
	}
	return out, nil
}

const percent = 100

// ── json_api ──

// JSONResult is any decoded JSON body; the widget picks fields from it.
type JSONResult struct{ Body any }

type JSONAPISource struct{}

func (JSONAPISource) Key() string                { return "json_api" }
func (JSONAPISource) TTL() time.Duration         { return jsonAPITTL }
func (JSONAPISource) Service() enums.ServiceType { return "" }

func (JSONAPISource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	headers, _ := sctx.Params["headers"].(map[string]string)
	body, _, err := httpclient.GetJSON(ctx, asStr(sctx.Params["url"]), httpclient.Options{Headers: headers})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	return &JSONResult{Body: body}, nil
}

func init() {
	Register(HolidaysSource{})
	Register(JokesSource{})
	Register(CryptoSource{})
	Register(StocksSource{})
	Register(JSONAPISource{})
}
