package sources

// Travel boards: departures or arrivals at an airport (AeroDataBox via
// RapidAPI, needs a key) and at a public transport stop (transport.rest,
// Deutsche Bahn data, no key).
//
//	flights  GET /flights/airports/iata/{IATA}/{from}/{to}   X-RapidAPI-Key
//	transit  GET /stops/{id}/departures?duration=&results=
//	         GET /locations?query=<name>  (when the stop is given by name)

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"

	"andon/internal/drivers/httpclient"
	"andon/internal/enums"
)

const (
	flightsTTL    = 10 * time.Minute
	transitTTL    = time.Minute
	flightsWindow = 12 * time.Hour // AeroDataBox allows at most 12 h
	flightsHost   = "aerodatabox.p.rapidapi.com"
	flightsLocal  = "2006-01-02T15:04"
	transitWindow = 60 // minutes
	secondsPerMin = 60
)

var (
	flightsBase = "https://" + flightsHost
	transitBase = "https://v6.db.transport.rest"
)

// Movement is one flight or train leaving or arriving.
type Movement struct {
	When     time.Time
	Line     string // flight number or line name
	Place    string // other airport or direction
	Status   string
	Delay    int // minutes, transit only
	Platform string
	Canceled bool
}

// BoardResult is a departure or arrival board, soonest first.
type BoardResult struct {
	Stop      string
	Movements []Movement
}

// ── flights ──

type FlightsSource struct{}

func (FlightsSource) Key() string                { return "flights" }
func (FlightsSource) TTL() time.Duration         { return flightsTTL }
func (FlightsSource) Service() enums.ServiceType { return "" }

// Fetch: params airport (IATA), direction (Departure|Arrival), api_key.
func (FlightsSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	key := asStr(sctx.Params["api_key"])
	if key == "" {
		return nil, newSourceError("api_key missing")
	}
	airport := strings.ToUpper(asStr(sctx.Params["airport"]))
	direction := asStr(sctx.Params["direction"])
	now := time.Now().UTC()
	target := flightsBase + "/flights/airports/iata/" + url.PathEscape(airport) + "/" +
		now.Format(flightsLocal) + "/" + now.Add(flightsWindow).Format(flightsLocal)
	query := url.Values{"direction": {direction}, "withCancelled": {"true"}, "withCodeshared": {"false"}}
	headers := map[string]string{"X-RapidAPI-Key": key, "X-RapidAPI-Host": flightsHost}

	body, _, err := httpclient.GetJSON(ctx, target, httpclient.Options{Params: query, Headers: headers})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	list := "departures"
	if direction == "Arrival" {
		list = "arrivals"
	}
	out := &BoardResult{Stop: airport}
	for _, raw := range asList(asMap(body)[list]) {
		f := asMap(raw)
		move := asMap(f["movement"])
		when := flightTime(asStr(asMap(move["scheduledTime"])["utc"]))
		out.Movements = append(out.Movements, Movement{
			When: when, Line: asStr(f["number"]), Place: asStr(asMap(move["airport"])["name"]), Status: asStr(f["status"]),
			Canceled: strings.EqualFold(asStr(f["status"]), "Canceled"),
		})
	}
	return out, nil
}

// flightLayouts: AeroDataBox writes "2026-09-25 10:15Z".
var flightLayouts = []string{"2006-01-02 15:04Z07:00", "2006-01-02 15:04Z", time.RFC3339}

func flightTime(v string) time.Time {
	for _, layout := range flightLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

// ── transit ──

type TransitSource struct{}

func (TransitSource) Key() string                { return "transit" }
func (TransitSource) TTL() time.Duration         { return transitTTL }
func (TransitSource) Service() enums.ServiceType { return "" }

// Fetch: params stop (IBNR id like "8000261" or a name), results.
func (TransitSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	stop := strings.TrimSpace(asStr(sctx.Params["stop"]))
	name := stop
	if _, err := strconv.Atoi(stop); err != nil {
		id, found, err := findStop(ctx, stop)
		if err != nil {
			return nil, err
		}
		stop, name = id, found
	}

	query := url.Values{"duration": {strconv.Itoa(transitWindow)}, "results": {strconv.Itoa(int(asFloat(sctx.Params["results"])))}}
	body, _, err := httpclient.GetJSON(ctx, transitBase+"/stops/"+url.PathEscape(stop)+"/departures", httpclient.Options{Params: query})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}

	// transport.rest v6 wraps the list; older versions return it bare.
	list := asList(asMap(body)["departures"])
	if list == nil {
		list = asList(body)
	}
	out := &BoardResult{Stop: name}
	for _, raw := range list {
		d := asMap(raw)
		when, _ := time.Parse(time.RFC3339, firstNonBlank(asStr(d["when"]), asStr(d["plannedWhen"])))
		if name == stop {
			out.Stop = asStr(asMap(d["stop"])["name"])
		}
		out.Movements = append(out.Movements, Movement{
			When: when, Line: asStr(asMap(d["line"])["name"]), Place: asStr(d["direction"]),
			Delay: int(asFloat(d["delay"])) / secondsPerMin, Platform: asStr(d["platform"]), Canceled: asBool(d["cancelled"]),
		})
	}
	return out, nil
}

// findStop resolves a stop name to its id.
func findStop(ctx context.Context, name string) (string, string, error) {
	query := url.Values{"query": {name}, "results": {"1"}, "addresses": {"false"}, "poi": {"false"}}
	body, _, err := httpclient.GetJSON(ctx, transitBase+"/locations", httpclient.Options{Params: query})
	if err != nil {
		return "", "", newSourceError("%s", err.Error())
	}
	list := asList(body)
	if len(list) == 0 {
		return "", "", newSourceError("stop not found")
	}
	s := asMap(list[0])
	return asStr(s["id"]), asStr(s["name"]), nil
}

func init() {
	Register(FlightsSource{})
	Register(TransitSource{})
}
