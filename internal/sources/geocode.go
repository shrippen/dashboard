package sources

// Place search for settings that need coordinates (e.g. DWD warnings):
// Open-Meteo's free geocoding API, no key.

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"andon/internal/drivers/httpclient"
	"andon/internal/enums"
)

// GeocodeURL is Open-Meteo's place search.
const GeocodeURL = "https://geocoding-api.open-meteo.com/v1/search"

// placeLimit caps the suggestions shown while typing.
const placeLimit = 8

// Place is one search hit.
type Place struct {
	Name, Region, Country string
	Lat, Lon              float64
}

// Label reads like "Weimar, Thüringen, Deutschland".
func (p Place) Label() string {
	parts := []string{p.Name}
	for _, s := range []string{p.Region, p.Country} {
		if s != "" && s != p.Name {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

// SearchPlaces looks places up by name, in the given language.
func SearchPlaces(ctx context.Context, baseURL, query string, locale enums.Locale) ([]Place, error) {
	params := url.Values{"name": {query}, "count": {strconv.Itoa(placeLimit)}, "language": {string(locale)}, "format": {"json"}}
	body, _, err := httpclient.GetJSON(ctx, baseURL, httpclient.Options{Params: params})
	if err != nil {
		return nil, newSourceError("%v", err)
	}
	var out []Place
	for _, raw := range asList(asMap(body)["results"]) {
		r := asMap(raw)
		out = append(out, Place{Name: asStr(r["name"]), Region: asStr(r["admin1"]), Country: asStr(r["country"]),
			Lat: asFloat(r["latitude"]), Lon: asFloat(r["longitude"])})
	}
	return out, nil
}
