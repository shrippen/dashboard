package sources_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"andon/internal/sources"
)

func fetch(t *testing.T, key string, params map[string]any) any {
	t.Helper()
	src, err := sources.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	out, err := src.Fetch(context.Background(), sources.Ctx{Params: params})
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return out
}

func publicAPIs(t *testing.T) {
	year := time.Now().UTC().Year()
	next := time.Now().UTC().AddDate(0, 0, 3).Format("2006-01-02")
	later := strconv.Itoa(year+1) + "-01-01"
	srv := jsonServer(t, map[string]any{
		"/api/v3/PublicHolidays/" + strconv.Itoa(year) + "/DE": []any{
			map[string]any{"date": "2000-01-01", "localName": "Past", "global": true},
			map[string]any{"date": next, "localName": "Regional", "global": false, "counties": []any{"DE-BY"}},
			map[string]any{"date": next, "localName": "Elsewhere", "global": false, "counties": []any{"DE-NW"}},
		},
		"/api/v3/PublicHolidays/" + strconv.Itoa(year+1) + "/DE": []any{map[string]any{"date": later, "localName": "Neujahr", "global": true}},
		"/joke/Programming":    map[string]any{"type": "twopart", "setup": "Q", "delivery": "A"},
		"/api/v3/simple/price": map[string]any{"bitcoin": map[string]any{"eur": 50000.0, "eur_24h_change": -2.5}},
		"/info.0.json":         map[string]any{"num": 42, "safe_title": "T", "alt": "alt", "img": "IMG"},
	}, nil)
	restore := sources.SetBases(srv.URL)
	t.Cleanup(restore)
}

func TestHolidaysJokesCrypto(t *testing.T) {
	publicAPIs(t)

	days := fetch(t, "holidays", map[string]any{"country": "de", "state": "de-by"}).(*sources.HolidaysResult).Days
	if len(days) != 2 || days[0].Name != "Regional" || days[1].Name != "Neujahr" {
		t.Fatalf("holidays: %+v", days)
	}

	joke := fetch(t, "jokes", map[string]any{"category": "Programming", "lang": "de"}).(*sources.Joke)
	if joke.Setup != "Q" || joke.Delivery != "A" {
		t.Fatalf("joke: %+v", joke)
	}

	coins := fetch(t, "crypto", map[string]any{"coins": []string{"Bitcoin", "unknown"}, "currency": "EUR"}).(*sources.CryptoResult)
	if coins.Currency != "EUR" || len(coins.Coins) != 1 || coins.Coins[0].Change != -2.5 {
		t.Fatalf("crypto: %+v", coins)
	}
}

func TestStocksCSV(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Symbol,Date,Time,Open,High,Low,Close,Volume\r\nAAPL.US,2026-09-24,22:00:00,200,210,199,210,1000\r\nXX.US,N/D,N/D,N/D,N/D,N/D,N/D,N/D\r\n"))
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(sources.SetBases(srv.URL))

	quotes := fetch(t, "stocks", map[string]any{"symbols": []string{"aapl.us", "xx.us"}}).(*sources.StocksResult).Quotes
	if len(quotes) != 1 || quotes[0].Symbol != "AAPL.US" || quotes[0].Change != 5 {
		t.Fatalf("quotes: %+v", quotes)
	}
}

func TestJSONAPIWithHeaders(t *testing.T) {
	srv := jsonServer(t, map[string]any{"/stats": map[string]any{"a": map[string]any{"b": []any{1.0, 2.0}}}},
		func(r *http.Request) bool { return r.Header.Get("X-Key") == "k" })
	out := fetch(t, "json_api", map[string]any{"url": srv.URL + "/stats", "headers": map[string]string{"X-Key": "k"}}).(*sources.JSONResult)
	if out.Body == nil {
		t.Fatal("empty body")
	}
}

func TestTransitByName(t *testing.T) {
	when := time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)
	srv := jsonServer(t, map[string]any{
		"/locations": []any{map[string]any{"id": "8000261", "name": "München Hbf"}},
		"/stops/8000261/departures": map[string]any{"departures": []any{
			map[string]any{"when": when, "delay": 120, "line": map[string]any{"name": "S 1"}, "direction": "Freising", "platform": "2"},
		}},
	}, nil)
	t.Cleanup(sources.SetBases(srv.URL))

	board := fetch(t, "transit", map[string]any{"stop": "München Hbf", "results": 5.0}).(*sources.BoardResult)
	if board.Stop != "München Hbf" || len(board.Movements) != 1 || board.Movements[0].Delay != 2 || board.Movements[0].Line != "S 1" {
		t.Fatalf("board: %+v", board)
	}
}

func TestFlightsNeedKey(t *testing.T) {
	srv := jsonServer(t, map[string]any{}, nil)
	t.Cleanup(sources.SetBases(srv.URL))
	src, _ := sources.Get("flights")
	if _, err := src.Fetch(context.Background(), sources.Ctx{Params: map[string]any{"airport": "MUC"}}); err == nil {
		t.Fatal("missing key accepted")
	}
}
