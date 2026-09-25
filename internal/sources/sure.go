package sources

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"dashboard/internal/drivers/services"
	"dashboard/internal/enums"
)

const (
	surePage     = 100
	sureMaxPages = 10
	sureDays     = 90
	centsPerUnit = 100
)

type SureAccount struct {
	ID, Name, Type string // Type: depository, credit_card, investment, …
	Classification string // asset or liability
	Balance        float64
	Currency       string
}

// SureTxn is one booking; Amount is signed: income > 0, expense < 0.
type SureTxn struct {
	ID, Date, Name     string
	Amount             float64
	Category, Merchant string
	Account            string
}

// SureRecurring is an expected payment; Amount is positive, Expense tells
// the direction (Sure stores outflows as positive amounts).
type SureRecurring struct {
	Name, Status  string // active, inactive
	Amount        float64
	Expense       bool
	Next, Last    string // "2026-10-01"
	Min, Max, Avg float64
}

type SureDataset struct {
	URL          string
	Currency     string
	NetWorth     float64
	Accounts     []SureAccount
	Transactions []SureTxn // last 90 days
	Recurring    []SureRecurring
	SyncError    string // latest sync failed: its message
}

type SureData struct{}

func (SureData) Key() string                { return "sure.data" }
func (SureData) TTL() time.Duration         { return dataTTL }
func (SureData) Service() enums.ServiceType { return enums.ServiceSure }

func (SureData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoSure(time.Now()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	data, err := loadSure(ctx, services.SureApi{URL: sctx.URL, Key: secret, Verify: sctx.VerifyTLS}, sctx.URL, time.Now())
	if err != nil {
		return nil, fetchError(err)
	}
	return data, nil
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// cents reads *_cents fields as currency units.
func cents(v any) float64 { return asFloat(v) / centsPerUnit }

// moneyOf reads a Money#as_json value ({"amount": "12.5", "currency": …})
// or a plain number.
func moneyOf(v any) float64 {
	if m, ok := v.(map[string]any); ok {
		return asFloat(m["amount"])
	}
	return asFloat(v)
}

func loadSure(ctx context.Context, api services.SureApi, base string, now time.Time) (*SureDataset, error) {
	sheet, err := api.Get(ctx, "balance_sheet", nil)
	if err != nil {
		return nil, err
	}
	data := &SureDataset{URL: base, Currency: asStr(asMap(sheet)["currency"]), NetWorth: moneyOf(asMap(sheet)["net_worth"])}

	accounts, err := api.Get(ctx, "accounts", url.Values{"per_page": {strconv.Itoa(surePage)}})
	if err != nil {
		return nil, err
	}
	for _, raw := range asList(asMap(accounts)["accounts"]) {
		a := asMap(raw)
		data.Accounts = append(data.Accounts, SureAccount{
			ID: asStr(a["id"]), Name: asStr(a["name"]), Type: asStr(a["account_type"]),
			Classification: asStr(a["classification"]), Balance: cents(a["balance_cents"]), Currency: asStr(a["currency"]),
		})
	}

	since := now.AddDate(0, 0, -sureDays).Format(time.DateOnly)
	for page := 1; page <= sureMaxPages; page++ {
		txns, err := api.Get(ctx, "transactions", url.Values{"start_date": {since}, "per_page": {strconv.Itoa(surePage)}, "page": {strconv.Itoa(page)}})
		if err != nil {
			return nil, err
		}
		list := asList(asMap(txns)["transactions"])
		for _, raw := range list {
			t := asMap(raw)
			data.Transactions = append(data.Transactions, SureTxn{
				ID: asStr(t["id"]), Date: asStr(t["date"]), Name: asStr(t["name"]), Amount: cents(t["signed_amount_cents"]),
				Category: asStr(asMap(t["category"])["name"]), Merchant: asStr(asMap(t["merchant"])["name"]),
				Account: asStr(asMap(t["account"])["name"]),
			})
		}
		if len(list) < surePage {
			break
		}
	}

	// Recurring detection and sync state are newer API parts; older
	// servers simply lack them.
	if rec, err := api.Get(ctx, "recurring_transactions", url.Values{"per_page": {strconv.Itoa(surePage)}}); err == nil {
		list := asList(asMap(rec)["recurring_transactions"])
		if list == nil {
			list = asList(rec)
		}
		for _, raw := range list {
			r := asMap(raw)
			data.Recurring = append(data.Recurring, SureRecurring{
				Name: asStr(r["name"]), Status: asStr(r["status"]), Amount: abs(cents(r["amount_cents"])), Expense: cents(r["amount_cents"]) > 0,
				Next: asStr(r["next_expected_date"]), Last: asStr(r["last_occurrence_date"]),
				Min: cents(r["expected_amount_min_cents"]), Max: cents(r["expected_amount_max_cents"]), Avg: cents(r["expected_amount_avg_cents"]),
			})
		}
	}
	if sync, err := api.Get(ctx, "syncs/latest", nil); err == nil {
		s := asMap(sync)
		if d, ok := s["data"].(map[string]any); ok {
			s = d
		}
		if asStr(s["status"]) == "failed" {
			data.SyncError = asStr(asMap(s["syncable"])["name"])
		}
	}
	return data, nil
}

func init() {
	Register(SureData{})
	Register(testOf{SureData{}, func(d any) map[string]any { return map[string]any{"accounts": len(d.(*SureDataset).Accounts)} }})
}
