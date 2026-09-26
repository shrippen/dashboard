package sources_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"andon/internal/sources"
)

func fetchConn(t *testing.T, key string, sctx sources.Ctx) any {
	t.Helper()
	src, err := sources.Get(key)
	if err != nil {
		t.Fatal(err)
	}
	sctx.VerifyTLS = true
	out, err := src.Fetch(context.Background(), sctx)
	if err != nil {
		t.Fatalf("%s: %v", key, err)
	}
	return out
}

// Pi-hole v6: login, read, and log out again (sessions are limited).
func TestPiholeV6Session(t *testing.T) {
	loggedOut := false
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/auth", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"session": map[string]any{"valid": true, "sid": "s1"}})
	})
	mux.HandleFunc("DELETE /api/auth", func(w http.ResponseWriter, r *http.Request) { loggedOut = r.Header.Get("X-FTL-SID") == "s1" })
	mux.HandleFunc("GET /api/stats/summary", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"queries": map[string]any{"total": 100, "blocked": 20, "percent_blocked": 20.0},
			"gravity": map[string]any{"last_update": 1758700000}, "clients": map[string]any{"active": 5}})
	})
	mux.HandleFunc("GET /api/dns/blocking", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"blocking": "disabled"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	data := fetchConn(t, "pihole.data", sources.Ctx{URL: srv.URL, Secret: "pw"}).(*sources.DNSFilterDataset)
	if data.Queries != 100 || data.Enabled || data.Clients != 5 || !loggedOut {
		t.Fatalf("data: %+v, logged out %v", data, loggedOut)
	}
}

func TestPiholeV5Fallback(t *testing.T) {
	srv := jsonServer(t, map[string]any{"/admin/api.php": map[string]any{"dns_queries_today": 50, "ads_blocked_today": 5,
		"ads_percentage_today": 10.0, "status": "enabled"}}, nil)
	data := fetchConn(t, "pihole.data", sources.Ctx{URL: srv.URL, Secret: "token"}).(*sources.DNSFilterDataset)
	if data.Queries != 50 || !data.Enabled {
		t.Fatalf("data: %+v", data)
	}
}

func TestAdGuardNextcloudSabnzbd(t *testing.T) {
	srv := jsonServer(t, map[string]any{
		"/control/status": map[string]any{"protection_enabled": true},
		"/control/stats":  map[string]any{"num_dns_queries": 200, "num_blocked_filtering": 50},
		"/ocs/v2.php/apps/serverinfo/api/v1/info": map[string]any{"ocs": map[string]any{"data": map[string]any{
			"nextcloud":   map[string]any{"system": map[string]any{"version": "31.0.8", "freespace": 1e9, "apps": map[string]any{"num_updates_available": 2}}, "storage": map[string]any{"num_users": 4}},
			"activeUsers": map[string]any{"last24hours": 2},
		}}},
		"/api": map[string]any{"queue": map[string]any{"noofslots": 2, "kbpersec": "1024", "diskspace1": "30.5"},
			"history": map[string]any{"slots": []any{map[string]any{"name": "x", "status": "Failed", "fail_message": "CRC", "completed": 1758700000}}}},
	}, nil)

	ad := fetchConn(t, "adguard.data", sources.Ctx{URL: srv.URL, Secret: "u:p"}).(*sources.DNSFilterDataset)
	if ad.Percent != 25 || !ad.Enabled {
		t.Fatalf("adguard: %+v", ad)
	}
	nc := fetchConn(t, "nextcloud.data", sources.Ctx{URL: srv.URL, Secret: "token"}).(*sources.NextcloudDataset)
	if nc.Version != "31.0.8" || nc.AppUpdates != 2 || nc.Active24 != 2 || nc.FreeBytes != 1e9 {
		t.Fatalf("nextcloud: %+v", nc)
	}
	sab := fetchConn(t, "sabnzbd.data", sources.Ctx{URL: srv.URL, Secret: "k"}).(*sources.SabnzbdDataset)
	if sab.Slots != 2 || sab.FreeGB != 30.5 || len(sab.Failures) != 1 {
		t.Fatalf("sabnzbd: %+v", sab)
	}
}

func TestGluetunLeakCheck(t *testing.T) {
	srv := jsonServer(t, map[string]any{
		"/v1/vpn/status":  map[string]any{"status": "running"},
		"/v1/publicip/ip": map[string]any{"public_ip": "1.2.3.4", "country": "Sweden"},
		"/ip":             map[string]any{"ip": "1.2.3.4"},
	}, nil)
	t.Cleanup(sources.SetBases(srv.URL))
	data := fetchConn(t, "gluetun.data", sources.Ctx{URL: srv.URL, Options: map[string]any{"country": "Sweden"}}).(*sources.GluetunDataset)
	if data.Status != "running" || data.ExitIP != "1.2.3.4" || data.OwnIP != "1.2.3.4" {
		t.Fatalf("gluetun: %+v", data)
	}
}

func TestDomainsRDAP(t *testing.T) {
	expires := time.Now().UTC().AddDate(0, 1, 0).Format(time.RFC3339)
	srv := jsonServer(t, map[string]any{
		"/domain/example.co.uk": map[string]any{"events": []any{map[string]any{"eventAction": "expiration", "eventDate": expires}}},
		"/domain/example.de":    map[string]any{"events": []any{map[string]any{"eventAction": "last changed", "eventDate": expires}}},
	}, nil)
	t.Cleanup(sources.SetBases(srv.URL))
	data := fetchConn(t, "domains.data", sources.Ctx{URL: "https://cloud.example.co.uk",
		Options: map[string]any{"domains": []any{"www.example.de", "example.de"}}}).(*sources.DomainsDataset)
	if len(data.Domains) != 2 || data.Domains[0].Name != "example.co.uk" || data.Domains[0].Expires.IsZero() || !data.Domains[1].Expires.IsZero() {
		t.Fatalf("domains: %+v", data.Domains)
	}
}

func TestGlancesHistory(t *testing.T) {
	srv := jsonServer(t, map[string]any{"/api/4/cpu/total/history/3": map[string]any{"total": []any{
		[]any{"2026-09-25T10:00:00", 10.0}, []any{"2026-09-25T10:01:00", 30.0}, []any{"2026-09-25T10:02:00", 20.0},
	}}}, nil)
	out := fetchConn(t, "glances_history", sources.Ctx{URL: srv.URL, Params: map[string]any{"metric": "cpu", "points": 3.0}}).(*sources.GlancesHistory)
	if len(out.Samples) != 3 || out.Samples[1].Value != 30 {
		t.Fatalf("history: %+v", out)
	}
}
