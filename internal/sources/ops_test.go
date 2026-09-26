package sources_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"andon/internal/sources"
)

func TestKumaParsesMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Uptime Kuma: API key as basic-auth password, empty user.
		if _, pass, ok := r.BasicAuth(); !ok || pass != "key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`# HELP monitor_status Monitor Status
monitor_status{monitor_name="NAS",monitor_type="ping",monitor_url="https://",monitor_hostname="nas",monitor_port="null"} 0
monitor_status{monitor_name="Shop",monitor_type="http",monitor_url="https://shop.example",monitor_hostname="null",monitor_port="null"} 1
monitor_cert_days_remaining{monitor_name="Shop",monitor_type="http",monitor_url="https://shop.example",monitor_hostname="null",monitor_port="null"} 12
`))
	}))
	defer srv.Close()

	out, err := sources.KumaData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "key", VerifyTLS: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	mons := out.(*sources.KumaDataset).Monitors
	if len(mons) != 2 || mons[0].Status != sources.KumaDown || mons[0].CertDays != -1 ||
		mons[1].Status != sources.KumaUp || mons[1].CertDays != 12 {
		t.Fatalf("monitors: %+v", mons)
	}
}

// A reverse proxy that sends /metrics to its login page must fail the
// fetch, not report an instance without monitors.
func TestKumaRejectsLoginRedirect(t *testing.T) {
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>Sign in</body></html>"))
	}))
	defer login.Close()
	kuma := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, login.URL+"/auth/resource/x", http.StatusFound)
	}))
	defer kuma.Close()

	_, err := sources.KumaData{}.Fetch(context.Background(), sources.Ctx{URL: kuma.URL, Secret: "key", VerifyTLS: true})
	if err == nil {
		t.Fatal("expected an error for a login redirect")
	}
}

func TestProxmoxReadsNodesGuestsBackups(t *testing.T) {
	mux := http.NewServeMux()
	reply := func(path, body string) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "PVEAPIToken=root@pam!dash=uuid" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Write([]byte(`{"data":` + body + `}`))
		})
	}
	reply("/api2/json/nodes", `[{"node":"pve","status":"online"},{"node":"old","status":"offline"}]`)
	reply("/api2/json/nodes/pve/storage", `[{"storage":"local","used":90,"total":100},{"storage":"nfs","total":0}]`)
	reply("/api2/json/nodes/pve/qemu", `[{"vmid":100,"name":"docker"},{"vmid":9000,"name":"tpl","template":1}]`)
	reply("/api2/json/nodes/pve/lxc", `[{"vmid":"101","name":"dns"}]`)
	reply("/api2/json/nodes/pve/tasks", `[{"id":"100","status":"OK","endtime":1758000000},
		{"id":"100","status":"OK","endtime":1757000000},{"id":"101","status":"job errors","endtime":1758000000}]`)
	// apt/update is missing: the token lacks Sys.Audit.
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := sources.ProxmoxData{}.Fetch(context.Background(),
		sources.Ctx{URL: srv.URL, Secret: "root@pam!dash=uuid", VerifyTLS: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	data := out.(*sources.ProxmoxDataset)
	if len(data.Nodes) != 2 || data.Nodes[1].Online || data.Nodes[0].Updates != -1 || len(data.Nodes[0].Storages) != 1 {
		t.Fatalf("nodes: %+v", data.Nodes)
	}
	if len(data.Guests) != 3 || !data.Guests[1].Template || data.Guests[2].VMID != 101 {
		t.Fatalf("guests: %+v", data.Guests)
	}
	if got := data.Backups[100]; !got.Equal(time.Unix(1758000000, 0).UTC()) || !data.Backups[101].IsZero() {
		t.Fatalf("backups: %+v", data.Backups)
	}
}

func TestPaperlessCountsInbox(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"count":1,"results":[{"id":3,"name":"Inbox"}]}`))
	})
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("tags__id__in") != "3" || r.Header.Get("Authorization") != "Token tok" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"count":5,"results":[{"title":"Strom","added":"2026-08-01T10:00:00+02:00"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	out, err := sources.PaperlessData{}.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "tok", VerifyTLS: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	data := out.(*sources.PaperlessDataset)
	if data.Inbox != 5 || data.OldestTitle != "Strom" || data.OldestAdded != "2026-08-01" {
		t.Fatalf("data: %+v", data)
	}
}

func TestCertsReadExpiry(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")

	out, err := sources.CertData{}.Fetch(context.Background(), sources.Ctx{
		URL: "https://" + host, Options: map[string]any{"hosts": []any{host, "127.0.0.1:1"}},
	})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	certs := out.(*sources.CertDataset).Certs
	if len(certs) != 2 {
		t.Fatalf("certs: %+v", certs)
	}
	var ok, failed bool
	for _, c := range certs {
		ok = ok || (c.Host == host && c.Error == "" && c.NotAfter.After(time.Now()))
		failed = failed || (c.Host == "127.0.0.1:1" && c.Error != "")
	}
	if !ok || !failed {
		t.Fatalf("certs: %+v", certs)
	}
}
