package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBookMatchedPayment: a bank income naming an open invoice shows on
// the billing page; booking it posts a payment to Invoice Ninja with the
// invoice's original key and at most the open amount.
func TestBookMatchedPayment(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	var mu sync.Mutex
	var posted map[string]any
	page := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(`{"data": [` + body + `], "meta": {"pagination": {"total_pages": 1}}}`))
		}
	}
	issued := time.Now().AddDate(0, 0, -20).Format(time.DateOnly)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/invoices", page(`{"id": "Inv9x", "number": "RE-2026-017", "client_id": "Cl1", "status_id": "2", "date": "`+issued+`", "amount": 2380, "balance": 2380}`))
	mux.HandleFunc("GET /api/v1/clients", page(`{"id": "Cl1", "display_name": "Muster GmbH"}`))
	for _, empty := range []string{"payments", "expenses", "quotes", "recurring_invoices", "vendors"} {
		mux.HandleFunc("GET /api/v1/"+empty, page(""))
	}
	mux.HandleFunc("POST /api/v1/payments", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		_ = json.NewDecoder(r.Body).Decode(&posted)
		w.Write([]byte(`{"data": {}}`))
	})
	ninja := httptest.NewServer(mux)
	defer ninja.Close()

	space := string(regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/connections/new?service=kimai"))[1])
	for _, c := range []url.Values{
		{"service": {"sure"}, "name": {"Sure"}, "url": {"demo://sure"}},
		{"service": {"invoiceninja"}, "name": {"Ninja"}, "url": {ninja.URL}, "secret": {"tok"}},
	} {
		c.Set("csrf", csrfToken(t, srv, client))
		c.Set("space_id", space)
		c.Set("mode", "shared")
		c.Set("tls", "verify")
		postForm(t, client, srv.URL+"/connections", c)
	}

	// Stored data fills in the background after the first page view.
	var billingPage string
	for range 100 {
		billingPage = string(mustGet(t, srv, client, "/billing"))
		if strings.Contains(billingPage, `name="invoice_id"`) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	form := regexp.MustCompile(`name="space_id" value="(\d+)"><input type="hidden" name="txn" value="([^"]+)">\s*<input type="hidden" name="invoice_id" value="(\d+)">`).FindStringSubmatch(billingPage)
	if form == nil {
		t.Fatalf("no payment proposal:\n%s", billingPage)
	}
	resp := postForm(t, client, srv.URL+"/billing/payment", url.Values{"csrf": {csrfToken(t, srv, client)}, "space_id": {form[1]},
		"txn": {form[2]}, "invoice_id": {form[3]}, "number": {"RE-2026-017"}})
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "booked=RE-2026-017") {
		t.Fatalf("book: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	mu.Lock()
	defer mu.Unlock()
	invoices, _ := posted["invoices"].([]any)
	if posted["client_id"] != "Cl1" || posted["amount"] != 2380.0 || len(invoices) != 1 || invoices[0].(map[string]any)["invoice_id"] != "Inv9x" {
		t.Fatalf("posted: %+v", posted)
	}
}

// TestCrossTablesRender: each new table kind renders its header with demo
// connections of the same space.
func TestCrossTablesRender(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	space := string(regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/connections/new?service=kimai"))[1])

	conns := map[string]string{}
	for _, service := range []string{"kimai", "invoiceninja", "sure"} {
		resp := postForm(t, client, srv.URL+"/connections", url.Values{"csrf": {csrfToken(t, srv, client)}, "space_id": {space},
			"service": {service}, "name": {service}, "url": {"demo://" + service}, "mode": {"shared"}, "tls": {"verify"}})
		conns[service] = regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(resp.Header.Get("Location"))[1]
	}

	cases := []struct{ service, widget, kind, want string }{
		{"invoiceninja", "table", "full_rates", "Satz real"},
		{"kimai", "table", "unbilled_aging", "über 60 Tage"},
		{"kimai", "table", "budget_forecast", "Aufgebraucht"},
		{"kimai", "table", "project_margins", "Marge"},
		{"sure", "table", "payment_matches", "Erkannt an"},
		{"sure", "table", "missing_receipts", "Konto"},
		{"sure", "table", "subscriptions", "Kündbar bis"},
		{"invoiceninja", "kpi", "safe_to_spend", "zurückzulegen"},
	}
	for _, c := range cases {
		title := "X-" + c.kind
		field := "cfg.table"
		if c.widget == "kpi" {
			field = "cfg.metric"
		}
		postForm(t, client, srv.URL+"/widgets", url.Values{"csrf": {csrfToken(t, srv, client)}, "space_id": {space}, "type": {c.widget},
			"title": {title}, "connection_id": {conns[c.service]}, field: {c.kind}})
		boardURL, sectionID, version, widgetID := placeTarget(t, srv, client, title)
		postForm(t, client, srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+sectionID+"/place", url.Values{
			"csrf": {csrfToken(t, srv, client)}, "widget_id": {widgetID}, "version": {version}})
		placement := string(regexp.MustCompile(`/placements/(\d+)/unplace`).FindSubmatch(mustGet(t, srv, client, boardURL+"?edit"))[1])
		frag := string(awaitFragment(t, srv, client, placement, c.want))
		if !strings.Contains(frag, c.want) || strings.Contains(frag, "col.") {
			t.Errorf("%s: %q missing or raw key:\n%s", c.kind, c.want, frag)
		}
		version = regexp.MustCompile(`data-version="(\d+)"`).FindStringSubmatch(string(mustGet(t, srv, client, boardURL)))[1]
		postForm(t, client, srv.URL+"/placements/"+placement+"/unplace", url.Values{"csrf": {csrfToken(t, srv, client)}, "version": {version}})
	}
}
