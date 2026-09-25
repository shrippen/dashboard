package sources_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"dashboard/internal/sources"
)

func jsonServer(t *testing.T, routes map[string]any, check func(*http.Request) bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range routes {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if check != nil && !check(r) {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestScrutinyReadsSummary(t *testing.T) {
	srv := jsonServer(t, map[string]any{"/api/summary": map[string]any{"data": map[string]any{"summary": map[string]any{
		"0x1": map[string]any{"device": map[string]any{"device_name": "sdb", "model_name": "ST4000", "device_status": 1},
			"smart": map[string]any{"temp": 41, "power_on_hours": 42000, "collector_date": "2026-09-25T03:00:00Z"}},
		"0x2": map[string]any{"device": map[string]any{"device_name": "sda", "device_status": 0}, "smart": map[string]any{}},
	}}}}, nil)

	out, err := sources.ScrutinyData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	disks := out.(*sources.ScrutinyDataset).Disks
	if len(disks) != 2 || disks[1].Name != "sdb" || disks[1].Status != 1 || disks[1].Temp != 41 || disks[1].Seen.IsZero() {
		t.Fatalf("disks: %+v", disks)
	}
}

func TestImmichReadsStorageJobsVersion(t *testing.T) {
	srv := jsonServer(t, map[string]any{
		"/api/server/storage":       map[string]any{"diskUsagePercentage": 87.4, "diskAvailable": "412 GiB"},
		"/api/server/statistics":    map[string]any{"photos": 100, "videos": 5},
		"/api/jobs":                 map[string]any{"faceDetection": map[string]any{"jobCounts": map[string]any{"failed": 3}}, "sidecar": map[string]any{"jobCounts": map[string]any{"failed": 0}}},
		"/api/server/version":       map[string]any{"major": 1, "minor": 131, "patch": 0},
		"/api/server/version-check": map[string]any{"releaseVersion": "v1.132.3"},
	}, func(r *http.Request) bool { return r.Header.Get("x-api-key") == "k" })

	out, err := sources.ImmichData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "k", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.ImmichDataset)
	if d.Photos != 100 || d.DiskPercent != 87.4 || d.FailedJobs["faceDetection"] != 3 || len(d.FailedJobs) != 1 ||
		d.Version != "v1.131.0" || d.Latest != "v1.132.3" {
		t.Fatalf("data: %+v", d)
	}
}

func TestUmamiLoginAndBothStatShapes(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"token": "tok"})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("/api/websites", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"id": "a", "name": "Blog"}, map[string]any{"id": "b", "name": "Shop"}}})
	}))
	mux.HandleFunc("/api/websites/a/stats", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"visitors": map[string]any{"value": 10, "prev": 20}, "pageviews": map[string]any{"value": 30, "prev": 40}})
	}))
	mux.HandleFunc("/api/websites/b/stats", authed(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"visitors": 5, "pageviews": 7, "comparison": map[string]any{"visitors": 50, "pageviews": 70}})
	}))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := sources.UmamiData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "admin:pw", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	sites := out.(*sources.UmamiDataset).Sites
	if len(sites) != 2 || sites[0].Visitors != 10 || sites[0].PrevVisit != 20 || sites[1].Visitors != 5 || sites[1].PrevVisit != 50 {
		t.Fatalf("sites: %+v", sites)
	}
}

func TestFreshRSSGoogleReader(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/greader.php/accounts/ClientLogin", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("Passwd") != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte("SID=alex/x\nLSID=null\nAuth=alex/tok\n"))
	})
	authed := func(body any) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "GoogleLogin auth=alex/tok" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(body)
		}
	}
	mux.HandleFunc("/api/greader.php/reader/api/0/subscription/list", authed(map[string]any{"subscriptions": []any{
		map[string]any{"id": "feed/1", "title": "heise", "categories": []any{map[string]any{"label": "News"}}},
		map[string]any{"id": "feed/2", "title": "Old"},
	}}))
	mux.HandleFunc("/api/greader.php/reader/api/0/unread-count", authed(map[string]any{"unreadcounts": []any{
		map[string]any{"id": "feed/1", "count": 7, "newestItemTimestampUsec": "1758780000000000"},
		map[string]any{"id": "user/-/state/com.google/reading-list", "count": 7},
	}}))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := sources.FreshRSSData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "alex:pw", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.FreshRSSDataset)
	if d.Unread != 7 || len(d.Feeds) != 2 || d.Feeds[0].Title != "Old" || d.Feeds[1].Category != "News" || d.Feeds[1].Newest.IsZero() {
		t.Fatalf("data: %+v", d)
	}
}

func TestGiteaAssignedReviewsRepos(t *testing.T) {
	srv := jsonServer(t, map[string]any{
		"/api/v1/user":              map[string]any{"login": "alex"},
		"/api/v1/notifications/new": map[string]any{"new": 3},
		"/api/v1/repos/issues/search": []any{map[string]any{"number": 12, "title": "Bug", "html_url": "u",
			"repository": map[string]any{"full_name": "alex/app"}, "due_date": "2026-09-20T00:00:00Z", "updated_at": "2026-09-01T00:00:00Z"}},
		"/api/v1/user/repos": []any{map[string]any{"full_name": "alex/app", "updated_at": "2020-01-01T00:00:00Z"},
			map[string]any{"full_name": "alex/old", "archived": true}},
	}, func(r *http.Request) bool { return r.Header.Get("Authorization") == "token t" })

	out, err := sources.GiteaData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "t", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.GiteaDataset)
	if d.User != "alex" || d.Notifications != 3 || len(d.Assigned) != 1 || d.Assigned[0].Due.IsZero() || len(d.Repos) != 1 {
		t.Fatalf("data: %+v", d)
	}
}

func TestBorgDashboardAndClients(t *testing.T) {
	srv := jsonServer(t, map[string]any{
		"/api/v1/dashboard": map[string]any{"jobs": map[string]any{"failed_24h": 2, "completed_24h": 9},
			"storage":  map[string]any{"used_bytes": 90, "total_bytes": 100},
			"archives": map[string]any{"last_backup_at": "2026-09-25T03:00:00Z"},
			"updates":  map[string]any{"server_available": true, "agents_outdated": 0}},
		"/api/v1/clients": map[string]any{"clients": []any{map[string]any{"name": "nas", "status": "online", "last_heartbeat": "2026-09-25 10:00:00"}}},
	}, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer bbs_tok_x" })

	out, err := sources.BorgData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "bbs_tok_x", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.BorgDataset)
	if d.Failed24h != 2 || d.TotalBytes != 100 || d.LastBackup.IsZero() || !d.ServerUpdate || len(d.Clients) != 1 || d.Clients[0].LastSeen.IsZero() {
		t.Fatalf("data: %+v", d)
	}
}

func TestSureReadsAccountsTransactionsRecurring(t *testing.T) {
	srv := jsonServer(t, map[string]any{
		"/api/v1/balance_sheet": map[string]any{"currency": "EUR", "net_worth": map[string]any{"amount": "1234.5", "currency": "EUR"}},
		"/api/v1/accounts":      map[string]any{"accounts": []any{map[string]any{"id": "a", "name": "Giro", "account_type": "depository", "balance_cents": 12345}}},
		"/api/v1/transactions": map[string]any{"transactions": []any{map[string]any{"id": "t", "date": "2026-09-20", "name": "Kunde",
			"signed_amount_cents": 50000, "account": map[string]any{"name": "Giro"}, "category": nil}}},
		"/api/v1/recurring_transactions": map[string]any{"recurring_transactions": []any{map[string]any{"name": "Miete", "status": "active",
			"amount_cents": 45000, "next_expected_date": "2026-10-01"}}},
		"/api/v1/syncs/latest": map[string]any{"data": map[string]any{"status": "failed", "syncable": map[string]any{"name": "Sparkasse"}}},
	}, func(r *http.Request) bool { return r.Header.Get("X-Api-Key") == "k" })

	out, err := sources.SureData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "k", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.SureDataset)
	if d.NetWorth != 1234.5 || d.Accounts[0].Balance != 123.45 || d.Transactions[0].Amount != 500 ||
		!d.Recurring[0].Expense || d.Recurring[0].Amount != 450 || d.SyncError != "Sparkasse" {
		t.Fatalf("data: %+v", d)
	}
}

func TestLinkwardenCollectionsWithCursor(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/collections", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"id": 1, "name": "Homelab"}, map[string]any{"id": 2, "name": "Privat"}}})
	})
	mux.HandleFunc("/api/v1/links", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Query().Get("collectionId") != "1" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var page []any
		switch r.URL.Query().Get("cursor") {
		case "":
			page = []any{map[string]any{"id": 9, "name": "A", "url": "https://a"}, map[string]any{"id": 8, "name": "B", "url": "https://b"}}
		case "8":
			page = []any{map[string]any{"id": 7, "name": "C", "url": "https://c"}}
		}
		json.NewEncoder(w).Encode(map[string]any{"response": page})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := sources.LinkwardenData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "tok", VerifyTLS: true,
		Options: map[string]any{"collections": []any{"homelab"}}})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.LinkwardenDataset)
	if len(d.Collections) != 1 || len(d.Links) != 3 || d.Links[2].Name != "C" {
		t.Fatalf("data: %+v", d)
	}
}
