package sources_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"andon/internal/enums"
	"andon/internal/sources"
)

// TestSearchPlaces reads Open-Meteo's geocoding answer into places.
func TestSearchPlaces(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "Weimar" || r.URL.Query().Get("language") != "de" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"results":[{"name":"Weimar","latitude":50.9803,"longitude":11.32903,"country":"Deutschland","admin1":"Thüringen"},
			{"name":"Weimar","latitude":29.7,"longitude":-96.78,"country":"Vereinigte Staaten","admin1":"Texas"}]}`))
	}))
	defer srv.Close()

	places, err := sources.SearchPlaces(context.Background(), srv.URL, "Weimar", enums.LocaleDE)
	if err != nil || len(places) != 2 {
		t.Fatalf("places: %+v %v", places, err)
	}
	if p := places[0]; p.Label() != "Weimar, Thüringen, Deutschland" || p.Lat != 50.9803 || p.Lon != 11.32903 {
		t.Fatalf("first place: %+v %q", p, p.Label())
	}
}
