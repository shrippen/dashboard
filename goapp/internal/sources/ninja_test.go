package sources_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"dashboard/internal/sources"
)

func TestNinjaDataNormalizesInvoicesAndExpenses(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/invoices", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": [{
			"id": 1, "number": "INV-1", "client_id": 9, "status_id": "4",
			"date": "2026-01-10", "due_date": "2026-01-24",
			"amount": 119.0, "balance": 0, "total_taxes": 19.0
		}], "meta": {"pagination": {"total_pages": 1}}}`))
	})
	mux.HandleFunc("/api/v1/payments", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": [], "meta": {"pagination": {"total_pages": 1}}}`))
	})
	mux.HandleFunc("/api/v1/clients", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": [{"id": 9, "display_name": "Acme GmbH", "vat_number": "DE123", "country_id": "276"}], "meta": {"pagination": {"total_pages": 1}}}`))
	})
	mux.HandleFunc("/api/v1/expenses", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": [{
			"id": 1, "date": "2026-01-05", "amount": 100.0,
			"tax_rate1": 19, "uses_inclusive_taxes": true, "public_notes": "Laptop"
		}], "meta": {"pagination": {"total_pages": 1}}}`))
	})
	mux.HandleFunc("/api/v1/quotes", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": [], "meta": {"pagination": {"total_pages": 1}}}`))
	})
	mux.HandleFunc("/api/v1/recurring_invoices", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data": [], "meta": {"pagination": {"total_pages": 1}}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := sources.NinjaData{}
	out, err := src.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "tok", VerifyTLS: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	data := out.(*sources.NinjaDataset)

	if len(data.Invoices) != 1 {
		t.Fatalf("expected 1 invoice, got %d", len(data.Invoices))
	}
	inv := data.Invoices[0]
	if inv.Status != "paid" || inv.Net != 100.0 {
		t.Fatalf("unexpected invoice: %+v", inv)
	}
	if len(data.Clients) != 1 || data.Clients[0].Name != "Acme GmbH" {
		t.Fatalf("unexpected clients: %+v", data.Clients)
	}
	if len(data.Expenses) != 1 {
		t.Fatalf("expected 1 expense, got %d", len(data.Expenses))
	}
	// 100 inclusive of 19% VAT -> tax = 100*19/119 ≈ 15.97
	if got := data.Expenses[0].Tax; got < 15.9 || got > 16.0 {
		t.Fatalf("expected ~15.97 inclusive VAT, got %v", got)
	}
}

func TestNinjaTestReadsVersionHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-App-Version", "5.10.47")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	src := sources.NinjaTest{}
	out, err := src.Fetch(context.Background(), sources.Ctx{URL: srv.URL, Secret: "tok", VerifyTLS: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if m := out.(map[string]any); m["version"] != "5.10.47" {
		t.Fatalf("expected version, got %+v", m)
	}
}
