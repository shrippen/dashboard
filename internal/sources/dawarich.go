package sources

import (
	"context"
	"net/url"
	"time"

	"dashboard/internal/drivers/services"
	"dashboard/internal/enums"
)

func dawarichAPI(sctx Ctx) (services.DawarichApi, error) {
	secret, err := needSecret(sctx)
	if err != nil {
		return services.DawarichApi{}, err
	}
	return services.DawarichApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}, nil
}

func dawarichVisit(raw any) DawarichVisit {
	m := asMap(raw)
	place := asMap(m["place"])
	area := asMap(m["area"])
	name := asStr(m["name"])
	if name == "" {
		name = asStr(place["name"])
	}
	if name == "" {
		name = asStr(area["name"])
	}
	areaID := asInt64(m["area_id"])
	if areaID == 0 {
		areaID = asInt64(area["id"])
	}
	v := DawarichVisit{
		ID: asInt64(m["id"]), Start: asStr(m["started_at"]), End: asStr(m["ended_at"]),
		Minutes: int(asFloat(m["duration"])), AreaID: areaID, Name: name,
	}
	if place["latitude"] != nil {
		lat := asFloat(place["latitude"])
		v.Lat = &lat
	}
	if place["longitude"] != nil {
		lon := asFloat(place["longitude"])
		v.Lon = &lon
	}
	return v
}

// DawarichData is the "dawarich.data" source: aggregates only — areas,
// visits, monthly distances, time of the last point.
type DawarichData struct{}

func (DawarichData) Key() string                { return "dawarich.data" }
func (DawarichData) TTL() time.Duration         { return dataTTL }
func (DawarichData) Service() enums.ServiceType { return enums.ServiceDawarich }

func (DawarichData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoDawarich(time.Now()), nil
	}
	api, err := dawarichAPI(sctx)
	if err != nil {
		return nil, err
	}
	data, err := loadDawarich(ctx, api, sctx)
	if err != nil {
		var apiErr services.ApiError
		if isApiError(err, &apiErr) {
			return nil, newSourceError("%s", apiErr.Error())
		}
		return nil, err
	}
	return data, nil
}

func loadDawarich(ctx context.Context, api services.DawarichApi, sctx Ctx) (*DawarichDataset, error) {
	now := time.Now().UTC()
	// params.days widens the window (the tax year export asks for a year).
	days := max(visitDays, int(asFloat(sctx.Params["days"])))
	window := url.Values{
		"start_at": {now.AddDate(0, 0, -days).Format(time.RFC3339)},
		"end_at":   {now.Format(time.RFC3339)},
	}

	last, err := api.Get(ctx, "points", url.Values{"per_page": {"1"}, "order": {"desc"}})
	if err != nil {
		return nil, err
	}

	areasRaw, err := api.Get(ctx, "areas", nil)
	if err != nil {
		return nil, err
	}
	var areas []DawarichArea
	for _, a := range asList(areasRaw) {
		am := asMap(a)
		areas = append(areas, DawarichArea{
			ID: asInt64(am["id"]), Name: asStr(am["name"]), Lat: asFloat(am["latitude"]),
			Lon: asFloat(am["longitude"]), Radius: asFloat(am["radius"]),
		})
	}

	visitsRaw, err := api.Get(ctx, "visits", window)
	if err != nil {
		return nil, err
	}
	var visits []DawarichVisit
	for _, v := range asList(visitsRaw) {
		visits = append(visits, dawarichVisit(v))
	}

	statsRaw, err := api.Get(ctx, "stats", nil)
	if err != nil {
		return nil, err
	}

	return &DawarichDataset{
		URL: sctx.URL, Areas: areas, Visits: visits, Stats: asMap(statsRaw), LastPoint: lastPoint(last),
	}, nil
}

func lastPoint(points any) string {
	list := asList(points)
	if len(list) == 0 {
		return ""
	}
	first := asMap(list[0])
	if ts, ok := first["timestamp"].(float64); ok && ts != 0 {
		return time.Unix(int64(ts), 0).UTC().Format(time.RFC3339)
	}
	created := asStr(first["created_at"])
	return created
}

// DawarichTest is the "dawarich.test" source: a lightweight connection check.
type DawarichTest struct{}

func (DawarichTest) Key() string                { return "dawarich.test" }
func (DawarichTest) TTL() time.Duration         { return testTTL }
func (DawarichTest) Service() enums.ServiceType { return enums.ServiceDawarich }

func (DawarichTest) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return map[string]any{"version": "demo"}, nil
	}
	api, err := dawarichAPI(sctx)
	if err != nil {
		return nil, err
	}
	v, err := api.Version(ctx)
	if err != nil {
		var apiErr services.ApiError
		if isApiError(err, &apiErr) {
			return nil, newSourceError("%s", apiErr.Error())
		}
		return nil, err
	}
	return map[string]any{"version": v}, nil
}

func init() {
	Register(KimaiData{})
	Register(KimaiTest{})
	Register(NinjaData{})
	Register(NinjaTest{})
	Register(SnipeData{})
	Register(SnipeTest{})
	Register(DawarichData{})
	Register(DawarichTest{})
}
