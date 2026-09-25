package sources_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"

	"dashboard/internal/sources"
)

const testKey = "k"

// truenasResults answers the calls TrueNASData makes.
var truenasResults = map[string]any{
	"auth.login_with_api_key": true,
	"system.info":             map[string]any{"hostname": "nas", "version": "25.04.2"},
	"pool.query":              []any{map[string]any{"name": "tank", "status": "ONLINE", "healthy": true, "size": 100, "allocated": 90}},
	"alert.list": []any{
		map[string]any{"uuid": "a", "level": "WARNING", "formatted": "disk slow", "dismissed": false},
		map[string]any{"uuid": "b", "level": "WARNING", "formatted": "old", "dismissed": true},
	},
	"app.query": []any{map[string]any{"name": "jellyfin", "state": "RUNNING", "upgrade_available": true}},
}

func TestTrueNASOverWebSocket(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx := r.Context()
		for {
			_, raw, err := conn.Read(ctx)
			if err != nil {
				return
			}
			var req struct {
				ID     int    `json:"id"`
				Method string `json:"method"`
			}
			json.Unmarshal(raw, &req)

			// A notification first: the client must skip it.
			note, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": "collection_update"})
			conn.Write(ctx, websocket.MessageText, note)
			answer, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": truenasResults[req.Method]})
			conn.Write(ctx, websocket.MessageText, answer)
		}
	}))
	t.Cleanup(srv.Close)

	out, err := sources.TrueNASData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: testKey, VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	data := out.(*sources.TrueNASDataset)
	if data.Version != "25.04.2" || len(data.Pools) != 1 || data.Pools[0].Allocated != 90 || len(data.Alerts) != 1 || !data.Apps[0].Update {
		t.Fatalf("data: %+v", data)
	}
}

func TestTrueNASRestFallback(t *testing.T) {
	routes := map[string]any{}
	for method, path := range map[string]string{"system.info": "system/info", "pool.query": "pool", "alert.list": "alert/list", "app.query": "app"} {
		routes["/api/v2.0/"+path] = truenasResults[method]
	}
	srv := jsonServer(t, routes, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+testKey })

	out, err := sources.TrueNASData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: testKey, VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	if data := out.(*sources.TrueNASDataset); data.Host != "nas" || len(data.Pools) != 1 {
		t.Fatalf("data: %+v", data)
	}
}

func TestKomodoReadsStacksAlerts(t *testing.T) {
	srv := jsonServer(t, map[string]any{
		"/read/GetServersSummary": map[string]any{"total": 3, "healthy": 2, "warning": 1, "unhealthy": 0},
		"/read/ListStacks": []any{map[string]any{"name": "immich", "info": map[string]any{"state": "Running",
			"services": []any{map[string]any{"service": "server", "update_available": true}}}}},
		"/read/ListAlerts": map[string]any{"alerts": []any{map[string]any{"ts": 1758790000000, "level": "CRITICAL",
			"data": map[string]any{"type": "ServerUnreachable", "data": map[string]any{"name": "pi"}}}}},
	}, func(r *http.Request) bool {
		return r.Header.Get("X-Api-Key") == "id" && r.Header.Get("X-Api-Secret") == "sec"
	})

	out, err := sources.KomodoData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "id:sec", VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	data := out.(*sources.KomodoDataset)
	if data.ServersProblem != 1 || data.Stacks[0].State != "running" || len(data.Stacks[0].Updates) != 1 || data.Alerts[0].Name != "pi" {
		t.Fatalf("data: %+v", data)
	}
}

func TestPangolinNeedsOrg(t *testing.T) {
	srv := jsonServer(t, map[string]any{
		"/v1/org/home/sites": map[string]any{"data": map[string]any{"sites": []any{
			map[string]any{"name": "lab", "type": "newt", "online": false, "megabytesIn": 5},
			map[string]any{"name": "local", "type": "local"},
		}}},
		"/v1/org/home/resources": map[string]any{"data": map[string]any{"resources": []any{
			map[string]any{"name": "Vault", "fullDomain": "vault.example.org", "enabled": true, "healthStatus": "unhealthy"},
		}}},
	}, nil)
	sctx := sources.Ctx{URL: srv.URL + "/v1", Secret: testKey, VerifyTLS: true}

	if _, err := (sources.PangolinData{}).Fetch(context.Background(), sctx); err == nil {
		t.Fatal("missing org accepted")
	}

	sctx.Options = map[string]any{"org": "home"}
	out, err := sources.PangolinData{}.Fetch(context.Background(), sctx)
	if err != nil {
		t.Fatal(err)
	}
	data := out.(*sources.PangolinDataset)
	if *data.Sites[0].Online || data.Sites[1].Online != nil || data.Resources[0].Health != "unhealthy" {
		t.Fatalf("data: %+v", data)
	}
}

func TestAuthentikUsage(t *testing.T) {
	now := time.Now().UTC()
	recent, old := now.Add(-2*time.Hour).Format(time.RFC3339), now.AddDate(0, 0, -3).Format(time.RFC3339)
	srv := jsonServer(t, map[string]any{
		"/api/v3/admin/version/": map[string]any{"version_current": "2025.6.3", "version_latest": "2025.8.1", "outdated": true},
		"/api/v3/events/events/volume/": []any{
			map[string]any{"action": "login", "time": recent, "count": 10},
			map[string]any{"action": "login_failed", "time": recent, "count": 4},
			map[string]any{"action": "login_failed", "time": old, "count": 6},
		},
		"/api/v3/events/events/top_per_user/": []any{map[string]any{"application": map[string]any{"name": "Gitea"}, "counted_events": 7, "unique_users": 2}},
		"/api/v3/core/users/": map[string]any{"pagination": map[string]any{"next": 0}, "results": []any{
			map[string]any{"username": "alex", "type": "internal", "last_login": recent},
			map[string]any{"username": "ak-outpost", "type": "internal_service_account"},
		}},
	}, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer "+testKey })

	out, err := sources.AuthentikData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: testKey, VerifyTLS: true})
	if err != nil {
		t.Fatal(err)
	}
	data := out.(*sources.AuthentikDataset)
	if data.Logins7d != 10 || data.Failed7d != 10 || data.Failed24h != 4 || len(data.Apps) != 1 || len(data.Users) != 1 || !data.Outdated {
		t.Fatalf("data: %+v", data)
	}
}
