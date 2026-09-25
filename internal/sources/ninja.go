package sources

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"dashboard/internal/drivers/services"
	"dashboard/internal/enums"
)

var ninjaStatus = map[string]string{
	"1": "draft", "2": "sent", "3": "partial", "4": "paid", "5": "cancelled", "6": "reversed",
}

var quoteStatus = map[string]string{
	"1": "draft", "2": "sent", "3": "approved", "4": "converted", "-1": "expired",
}

func ninjaAPI(sctx Ctx) (services.NinjaApi, error) {
	secret, err := needSecret(sctx)
	if err != nil {
		return services.NinjaApi{}, err
	}
	return services.NinjaApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}, nil
}

func ninjaInvoice(raw any) NinjaInvoice {
	m := asMap(raw)
	amount, taxes := asFloat(m["amount"]), asFloat(m["total_taxes"])
	return NinjaInvoice{
		ID: asInt64(m["id"]), Number: asStr(m["number"]), ClientID: asInt64(m["client_id"]),
		Status: statusFor(ninjaStatus, m["status_id"]), Date: day(m["date"]), DueDate: day(m["due_date"]),
		Amount: amount, Balance: asFloat(m["balance"]), Taxes: taxes, Net: amount - taxes,
	}
}

func statusFor(table map[string]string, raw any) string {
	key := asStr(raw)
	if key == "" {
		key = itoa(asInt64(raw))
	}
	if s, ok := table[key]; ok {
		return s
	}
	return "draft"
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// expenseTax is an expense's input VAT: explicit tax amounts, else computed
// from the rate (inclusive or exclusive).
func expenseTax(m map[string]any) float64 {
	amounts := asFloat(m["tax_amount1"]) + asFloat(m["tax_amount2"]) + asFloat(m["tax_amount3"])
	if amounts != 0 {
		return amounts
	}
	rate := asFloat(m["tax_rate1"]) + asFloat(m["tax_rate2"]) + asFloat(m["tax_rate3"])
	amount := asFloat(m["amount"])
	if rate == 0 {
		return 0
	}
	if asBool(m["uses_inclusive_taxes"]) {
		return amount * rate / (100 + rate)
	}
	return amount * rate / 100
}

// NinjaData is the "invoiceninja.data" source: invoices, payments, clients,
// expenses, quotes and recurring invoices from the last window.
type NinjaData struct{}

func (NinjaData) Key() string                { return "invoiceninja.data" }
func (NinjaData) TTL() time.Duration         { return dataTTL }
func (NinjaData) Service() enums.ServiceType { return enums.ServiceInvoiceNinja }

func (NinjaData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoNinja(time.Now()), nil
	}
	api, err := ninjaAPI(sctx)
	if err != nil {
		return nil, err
	}
	data, err := loadNinja(ctx, api, sctx)
	if err != nil {
		var apiErr services.ApiError
		if isApiError(err, &apiErr) {
			return nil, newSourceError("%s", apiErr.Error())
		}
		return nil, err
	}
	return data, nil
}

func loadNinja(ctx context.Context, api services.NinjaApi, sctx Ctx) (*NinjaDataset, error) {
	since := windowStart(time.Now().UTC()).Format("2006-01-02")

	invoicesRaw, err := api.Pages(ctx, "invoices", url.Values{"is_deleted": {"false"}})
	if err != nil {
		return nil, err
	}
	var invoices []NinjaInvoice
	for _, i := range invoicesRaw {
		im := asMap(i)
		if day(im["date"]) >= since || asFloat(im["balance"]) > 0 {
			invoices = append(invoices, ninjaInvoice(i))
		}
	}

	paymentsRaw, err := api.Pages(ctx, "payments", url.Values{"include": {"paymentables"}})
	if err != nil {
		return nil, err
	}
	var payments []NinjaPayment
	for _, p := range paymentsRaw {
		pm := asMap(p)
		if day(pm["date"]) >= since {
			payments = append(payments, NinjaPayment{
				ID: asInt64(pm["id"]), Date: day(pm["date"]), Amount: asFloat(pm["amount"]),
				ClientID: asInt64(pm["client_id"]),
			})
		}
	}

	clientsRaw, err := api.Pages(ctx, "clients", nil)
	if err != nil {
		return nil, err
	}
	var clients []NinjaClient
	for _, c := range clientsRaw {
		cm := asMap(c)
		name := asStr(cm["display_name"])
		if name == "" {
			name = asStr(cm["name"])
		}
		clients = append(clients, NinjaClient{
			ID: asInt64(cm["id"]), Name: name, VATNumber: asStr(cm["vat_number"]),
			CountryID: itoa(asInt64(cm["country_id"])),
		})
	}

	expensesRaw, err := api.Pages(ctx, "expenses", nil)
	if err != nil {
		return nil, err
	}
	var expenses []NinjaExpense
	for _, e := range expensesRaw {
		em := asMap(e)
		if day(em["date"]) >= since {
			notes := asStr(em["public_notes"])
			expenses = append(expenses, NinjaExpense{
				ID: asInt64(em["id"]), Date: day(em["date"]), Amount: asFloat(em["amount"]),
				Tax: round2(expenseTax(em)), Notes: notes, VendorID: asInt64(em["vendor_id"]), VendorKey: idKey(em["vendor_id"]),
			})
		}
	}

	// Vendor names let other sources match senders to expenses.
	var vendors []NinjaVendor
	if vendorsRaw, err := api.Pages(ctx, "vendors", nil); err == nil {
		for _, v := range vendorsRaw {
			vm := asMap(v)
			vendors = append(vendors, NinjaVendor{Key: idKey(vm["id"]), Name: asStr(vm["name"])})
		}
	}

	quotesRaw, err := api.Pages(ctx, "quotes", nil)
	if err != nil {
		return nil, err
	}
	var quotes []NinjaQuote
	for _, q := range quotesRaw {
		qm := asMap(q)
		quotes = append(quotes, NinjaQuote{
			ID: asInt64(qm["id"]), Number: asStr(qm["number"]), ClientID: asInt64(qm["client_id"]),
			Status: statusFor(quoteStatus, qm["status_id"]), Date: day(qm["date"]), Amount: asFloat(qm["amount"]),
		})
	}

	recurringRaw, err := api.Pages(ctx, "recurring_invoices", nil)
	if err != nil {
		return nil, err
	}
	var recurring []NinjaRecurring
	for _, r := range recurringRaw {
		rm := asMap(r)
		recurring = append(recurring, NinjaRecurring{
			ID: asInt64(rm["id"]), Number: asStr(rm["number"]), ClientID: asInt64(rm["client_id"]),
			Active: asStr(rm["status_id"]) == "2", NextSendDate: day(rm["next_send_date"]),
			RemainingCycles: int(asFloat(rm["remaining_cycles"])), Amount: asFloat(rm["amount"]),
		})
	}

	currency, _ := sctx.Options["currency"].(string)
	if currency == "" {
		currency = "EUR"
	}
	homeCountry, _ := sctx.Options["home_country_id"].(string)
	if homeCountry == "" {
		homeCountry = "276"
	}

	return &NinjaDataset{
		URL: sctx.URL, Currency: currency, Invoices: invoices, Payments: payments, Clients: clients,
		Expenses: expenses, Vendors: vendors, Quotes: quotes, Recurring: recurring, HomeCountryID: homeCountry,
	}, nil
}

func round2(f float64) float64 {
	return float64(int64(f*100+0.5)) / 100
}

// NinjaTest is the "invoiceninja.test" source: a lightweight connection check.
type NinjaTest struct{}

func (NinjaTest) Key() string                { return "invoiceninja.test" }
func (NinjaTest) TTL() time.Duration         { return testTTL }
func (NinjaTest) Service() enums.ServiceType { return enums.ServiceInvoiceNinja }

func (NinjaTest) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return map[string]any{"version": "demo"}, nil
	}
	api, err := ninjaAPI(sctx)
	if err != nil {
		return nil, err
	}
	v, err := api.Version(ctx)
	if err != nil {
		var apiErr services.ApiError
		if isApiError(err, &apiErr) {
			return nil, newSourceError("%s", apiErr.Error())
		}
		return nil, err
	}
	return map[string]any{"version": v}, nil
}

// idKey reads an id that Invoice Ninja v5 sends as hashed string and
// older versions as number.
func idKey(v any) string {
	if s := asStr(v); s != "" {
		return s
	}
	if f := asFloat(v); f != 0 {
		return strconv.FormatInt(int64(f), 10)
	}
	return ""
}
