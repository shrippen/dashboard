package web_test

import (
	"archive/zip"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestKimaiTimerStops: the tile shows the running timer and its stop
// button patches Kimai.
func TestKimaiTimerStops(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	var mu sync.Mutex
	var writes []string
	begin := time.Now().Add(-time.Hour).Format(time.RFC3339)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/timesheets/active", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id": 77, "begin": "` + begin + `", "project": {"id": 3, "name": "Relaunch", "customer": {"name": "Acme"}}, "activity": {"id": 7, "name": "Dev"}}]`))
	})
	mux.HandleFunc("GET /api/timesheets/recent", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	mux.HandleFunc("GET /api/timesheets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total-Pages", "1")
		w.Write([]byte(`[{"duration": 3600}]`))
	})
	mux.HandleFunc("PATCH /api/timesheets/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		writes = append(writes, r.URL.Path)
		mu.Unlock()
		w.Write([]byte(`{}`))
	})
	kimai := httptest.NewServer(mux)
	defer kimai.Close()

	space := string(regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/connections/new?service=kimai"))[1])
	csrf := csrfToken(t, srv, client)
	resp := postForm(t, client, srv.URL+"/connections", url.Values{"csrf": {csrf}, "space_id": {space}, "service": {"kimai"}, "name": {"Kimai"},
		"url": {kimai.URL}, "mode": {"shared"}, "secret": {"tok"}, "tls": {"verify"}})
	connID := regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(resp.Header.Get("Location"))[1]

	postForm(t, client, srv.URL+"/widgets", url.Values{"csrf": {csrf}, "space_id": {space}, "type": {"kimai_timer"}, "title": {"Timer"}, "connection_id": {connID}})
	boardURL, section, version, widget := placeTarget(t, srv, client, "Timer")
	postForm(t, client, srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+section+"/place", url.Values{"csrf": {csrf}, "widget_id": {widget}, "version": {version}})
	placement := string(regexp.MustCompile(`/placements/(\d+)/unplace`).FindSubmatch(mustGet(t, srv, client, boardURL+"?edit"))[1])

	frag := string(awaitFragment(t, srv, client, placement, "Relaunch"))
	if !strings.Contains(frag, `name="sheet" value="77"`) || !strings.Contains(frag, "2:00 h") {
		t.Fatalf("fragment:\n%s", frag)
	}

	resp = postForm(t, client, srv.URL+"/widget-fragments/"+placement+"/kimai", url.Values{"csrf": {csrf}, "action": {"stop"}, "sheet": {"77"}})
	if resp.StatusCode != http.StatusOK || len(writes) != 1 || writes[0] != "/api/timesheets/77/stop" {
		t.Fatalf("stop: %d %v", resp.StatusCode, writes)
	}
	if r := postForm(t, client, srv.URL+"/widget-fragments/"+placement+"/kimai", url.Values{"csrf": {csrf}, "action": {"start"}}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("start without ids: %d", r.StatusCode)
	}
}

// TestBillingDraftAndExport: unbilled Kimai time becomes a Ninja draft,
// the sheets are flagged exported, and the year package holds the CSVs.
func TestBillingDraftAndExport(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	var mu sync.Mutex
	var writes []string
	record := func(r *http.Request, body string) {
		mu.Lock()
		writes = append(writes, r.Method+" "+r.URL.Path+" "+body)
		mu.Unlock()
	}
	year := time.Now().Format("2006")
	kimaiMux := http.NewServeMux()
	kimaiMux.HandleFunc("GET /api/timesheets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total-Pages", "1")
		w.Write([]byte(`[{"id": 5, "begin": "` + year + `-01-02T09:00:00+0100", "end": "` + year + `-01-02T11:00:00+0100", "duration": 7200, "rate": 190,
			"billable": true, "exported": false, "project": {"id": 3, "customer": 9}, "activity": {"name": "Dev"}}]`))
	})
	kimaiMux.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id": 3, "name": "Relaunch", "customer": 9}]`))
	})
	kimaiMux.HandleFunc("GET /api/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id": 3, "name": "Relaunch", "customer": 9}`))
	})
	kimaiMux.HandleFunc("GET /api/customers", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[{"id": 9, "name": "Acme GmbH"}]`)) })
	kimaiMux.HandleFunc("GET /api/timesheets/active", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	kimaiMux.HandleFunc("GET /api/holiday/absences", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	kimaiMux.HandleFunc("PATCH /api/timesheets/{id}/export", func(w http.ResponseWriter, r *http.Request) { record(r, ""); w.Write([]byte(`{}`)) })
	kimai := httptest.NewServer(kimaiMux)
	defer kimai.Close()

	page := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"data": [` + body + `], "meta": {"pagination": {"total_pages": 1}}}`))
		}
	}
	ninjaMux := http.NewServeMux()
	ninjaMux.HandleFunc("GET /api/v1/clients", page(`{"id": "Opnel5aKBz", "display_name": "ACME GmbH"}`))
	ninjaMux.HandleFunc("GET /api/v1/invoices", page(`{"id": "x", "number": "R-1", "client_id": "Opnel5aKBz", "status_id": "4", "date": "`+year+`-01-10", "amount": 119, "total_taxes": 19}`))
	for _, empty := range []string{"payments", "expenses", "vendors", "quotes", "recurring_invoices"} {
		ninjaMux.HandleFunc("GET /api/v1/"+empty, page(""))
	}
	ninjaMux.HandleFunc("POST /api/v1/invoices", func(w http.ResponseWriter, r *http.Request) {
		buf := new(strings.Builder)
		_, _ = io.Copy(buf, r.Body)
		record(r, buf.String())
		w.Write([]byte(`{"data": {"number": "E-42"}}`))
	})
	ninja := httptest.NewServer(ninjaMux)
	defer ninja.Close()

	space := string(regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/connections/new?service=kimai"))[1])
	csrf := csrfToken(t, srv, client)
	for _, c := range []struct{ svc, url string }{{"kimai", kimai.URL}, {"invoiceninja", ninja.URL}} {
		postForm(t, client, srv.URL+"/connections", url.Values{"csrf": {csrf}, "space_id": {space}, "service": {c.svc}, "name": {c.svc},
			"url": {c.url}, "mode": {"shared"}, "secret": {"tok"}, "tls": {"verify"}})
	}

	var body string
	for range 100 {
		if body = string(mustGet(t, srv, client, "/billing")); strings.Contains(body, "Relaunch") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	customer := regexp.MustCompile(`name="customer_id" value="(\d+)"`).FindStringSubmatch(body)
	if customer == nil || !strings.Contains(body, "ACME GmbH") {
		t.Fatalf("billing page:\n%s", body)
	}

	resp := postForm(t, client, srv.URL+"/billing/draft", url.Values{"csrf": {csrf}, "space_id": {space}, "customer_id": {customer[1]}, "mark_exported": {"on"}})
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "E-42") {
		t.Fatalf("draft: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if len(writes) != 2 || !strings.Contains(writes[0], `"client_id":"Opnel5aKBz"`) || !strings.Contains(writes[0], `"quantity":2`) ||
		writes[1] != "PATCH /api/timesheets/5/export " {
		t.Fatalf("writes: %v", writes)
	}

	zipResp, err := client.Get(srv.URL + "/billing/export?space_id=" + space + "&year=" + year)
	if err != nil || zipResp.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("export: %v %v", err, zipResp.Header)
	}
	blob, _ := io.ReadAll(zipResp.Body)
	archive, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range archive.File {
		names = append(names, f.Name)
	}
	if strings.Join(names, ",") != "rechnungen.csv,zahlungen.csv,ausgaben.csv,ust.csv,stunden.csv" {
		t.Fatalf("zip: %v", names)
	}
}
