package sources_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"andon/internal/sources"
)

// fake answers fixed JSON per path and checks one auth header.
func fake(t *testing.T, header, value string, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if header != "" && r.Header.Get(header) != value {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHeadscaleDevices(t *testing.T) {
	srv := fake(t, "Authorization", "Bearer key", map[string]string{
		"GET /api/v1/node": `{"nodes": [{"givenName": "nas", "online": true, "expiry": "0001-01-01T00:00:00Z"},
			{"name": "pi", "online": false, "lastSeen": "2026-09-01T10:00:00Z", "expiry": "2026-10-01T00:00:00Z"}]}`,
	})
	out, err := sources.TailscaleData{}.Fetch(t.Context(), sources.Ctx{URL: srv.URL, Secret: "key"})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.TailscaleDataset)
	if !d.Headscale || len(d.Devices) != 2 || !d.Devices[0].KeyExpiry.IsZero() || d.Devices[1].Online || d.Devices[1].KeyExpiry.IsZero() {
		t.Fatalf("devices: %+v", d)
	}
}

func TestOPNsenseGateway(t *testing.T) {
	srv := fake(t, "Authorization", "Basic a2V5OnNlY3JldA==", map[string]string{
		"GET /api/core/firmware/status": `{"status": "update", "product_version": "25.7.2", "upgrade_packages": [{}, {}]}`,
		"GET /api/routes/gateway/status": `{"items": [{"name": "WAN", "status": "none", "delay": "11.4 ms", "loss": "0.0 %"},
			{"name": "LTE", "status": "down", "status_translated": "Offline", "loss": "100.0 %"}]}`,
	})
	out, err := sources.GatewayData{}.Fetch(t.Context(), sources.Ctx{URL: srv.URL, Secret: "key:secret"})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.GatewayDataset)
	if d.Updates != 2 || d.Version != "25.7.2" || !d.Gateways[0].Up || d.Gateways[0].DelayMS != 11.4 || d.Gateways[1].Up || d.Gateways[1].Loss != 100 {
		t.Fatalf("gateway: %+v", d)
	}
}

func TestSonarr(t *testing.T) {
	srv := fake(t, "X-Api-Key", "k", map[string]string{
		"GET /api/v3/system/status":  `{"appName": "Sonarr", "version": "4.0.15"}`,
		"GET /api/v3/health":         `[{"type": "error", "message": "No download client"}]`,
		"GET /api/v3/queue":          `{"totalRecords": 2, "records": [{"title": "A", "trackedDownloadStatus": "warning"}, {"title": "B", "trackedDownloadStatus": "ok"}]}`,
		"GET /api/v3/wanted/missing": `{"totalRecords": 7}`,
		"GET /api/v3/calendar":       `[{"series": {"title": "Dark"}, "seasonNumber": 2, "episodeNumber": 5, "airDateUtc": "2030-01-01T20:00:00Z"}]`,
	})
	out, err := sources.ArrData{}.Fetch(t.Context(), sources.Ctx{URL: srv.URL, Secret: "k"})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.ArrDataset)
	if d.Queue != 2 || len(d.Stuck) != 1 || d.Missing != 7 || d.Health[0].Level != "error" || d.Upcoming[0].Title != "Dark 2x05" {
		t.Fatalf("arr: %+v", d)
	}
}

func TestJellyfin(t *testing.T) {
	srv := fake(t, "X-Emby-Token", "k", map[string]string{
		"GET /System/Info":  `{"Version": "10.10.7", "HasUpdateAvailable": true}`,
		"GET /Items/Counts": `{"MovieCount": 12, "SeriesCount": 3, "EpisodeCount": 40}`,
		"GET /Sessions":     `[{"UserName": "anna", "NowPlayingItem": {"Name": "E1", "SeriesName": "Dark"}}, {"UserName": "idle"}]`,
	})
	out, err := sources.MediaServerData{}.Fetch(t.Context(), sources.Ctx{URL: srv.URL, Secret: "k"})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.MediaServerDataset)
	if !d.Update || d.Movies != 12 || len(d.Streams) != 1 || d.Streams[0].Title != "Dark" {
		t.Fatalf("jellyfin: %+v", d)
	}
}

func TestVaultwardenLogin(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /admin", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("token") != "admintoken" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "VW_ADMIN", Value: "jwt"})
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
	})
	mux.HandleFunc("GET /admin/users", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("VW_ADMIN"); err != nil || c.Value != "jwt" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`[{"email": "a@x", "twoFactorEnabled": false, "userEnabled": true, "lastActive": "2026-09-20 10:00:00 UTC"}]`))
	})
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`"1.34.3"`)) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := sources.VaultwardenData{}.Fetch(t.Context(), sources.Ctx{URL: srv.URL, Secret: "admintoken"})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.VaultwardenDataset)
	if d.Version != "1.34.3" || len(d.Users) != 1 || d.Users[0].TwoFactor || d.Users[0].LastActive.Day() != 20 {
		t.Fatalf("vaultwarden: %+v", d)
	}
}

func TestGrocyAndTibber(t *testing.T) {
	grocy := fake(t, "GROCY-API-KEY", "k", map[string]string{
		"GET /api/stock/volatile": `{"expired_products": [{"product": {"name": "Joghurt"}, "best_before_date": "2026-09-20"}],
			"missing_products": [{"name": "Kaffee", "amount_missing": 1}]}`,
		"GET /api/chores": `[{"chore_name": "Bad", "next_estimated_execution_time": "2026-09-24 10:00:00"}, {"chore_name": "nie", "next_estimated_execution_time": null}]`,
	})
	out, err := sources.GrocyData{}.Fetch(t.Context(), sources.Ctx{URL: grocy.URL, Secret: "k"})
	if err != nil {
		t.Fatal(err)
	}
	g := out.(*sources.GrocyDataset)
	if len(g.Expired) != 1 || g.Expired[0].Name != "Joghurt" || g.Missing[0].Name != "Kaffee" || len(g.Chores) != 1 {
		t.Fatalf("grocy: %+v", g)
	}

	tibber := fake(t, "Authorization", "Bearer t", map[string]string{
		"POST /": `{"data": {"viewer": {"homes": [{"appNickname": "Zuhause",
			"currentSubscription": {"priceInfo": {"current": {"total": 0.31, "level": "NORMAL", "currency": "EUR"},
				"today": [{"total": 0.30, "startsAt": "2026-09-25T00:00:00+02:00"}], "tomorrow": []}},
			"consumption": {"nodes": [{"from": "2026-09-24T00:00:00+02:00", "cost": 2.1, "consumption": 7.5}]}}]}}}`,
	})
	out, err = sources.TibberData{}.Fetch(t.Context(), sources.Ctx{URL: tibber.URL + "/", Secret: "t"})
	if err != nil {
		t.Fatal(err)
	}
	e := out.(*sources.TibberDataset)
	if e.Current != 0.31 || len(e.Prices) != 1 || e.Days[0].KWh != 7.5 || e.Days[0].Day != "2026-09-24" {
		t.Fatalf("tibber: %+v", e)
	}
}
