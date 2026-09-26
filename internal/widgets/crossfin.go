package widgets

// Table kinds that combine services (phase 13): the widget's own
// connection plus peers of the same space.
//
//	full_rates        Invoice Ninja + Kimai (+ Dawarich travel)
//	unbilled_aging    Kimai
//	payment_matches   Sure + Invoice Ninja
//	missing_receipts  Sure + Invoice Ninja, Paperless, Mail
//	subscriptions     Sure + Paperless, authentik
//	budget_forecast   Kimai
//	project_margins   Kimai + Invoice Ninja

import (
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	"andon/internal/rules"
	"andon/internal/sources"
)

const (
	TableFullRates     TableKind = "full_rates"
	TableAging         TableKind = "unbilled_aging"
	TablePayments      TableKind = "payment_matches"
	TableReceipts      TableKind = "missing_receipts"
	TableSubscriptions TableKind = "subscriptions"
	TableBudgetRunOut  TableKind = "budget_forecast"
	TableMargins       TableKind = "project_margins"
)

const (
	crossRateDays    = 90
	crossMarginDays  = 180
	crossPaymentDays = 90
	crossReceiptDays = 365
	peerNinja        = "invoiceninja"
	peerDawarich     = "dawarich"
	peerPaperless    = "paperless"
	peerMail         = "mail"
	peerAuthentik    = "authentik"
)

func peer(name string, service enums.ServiceType) Query {
	return Query{Name: name, Source: "data", Conn: ConnPeer, Service: service}
}

// crossQueries adds the peers a cross table needs.
func crossQueries(kind TableKind) []Query {
	switch kind {
	case TableFullRates:
		return []Query{kimaiPeer, peer(peerDawarich, enums.ServiceDawarich)}
	case TablePayments:
		return []Query{peer(peerNinja, enums.ServiceInvoiceNinja)}
	case TableReceipts:
		return []Query{peer(peerNinja, enums.ServiceInvoiceNinja), peer(peerPaperless, enums.ServicePaperless), peer(peerMail, enums.ServiceMail)}
	case TableSubscriptions:
		return []Query{peer(peerPaperless, enums.ServicePaperless), peer(peerAuthentik, enums.ServiceAuthentik)}
	case TableMargins:
		return []Query{peer(peerNinja, enums.ServiceInvoiceNinja)}
	}
	return nil
}

func crossCols(kind TableKind) []Col {
	switch kind {
	case TableFullRates:
		return []Col{{"customer", "text"}, {"billable_hours", "hours"}, {"other_hours", "hours"}, {"travel_hours", "hours"}, {"nominal_rate", "money"}, {"full_rate", "money"}}
	case TableAging:
		return []Col{{"customer", "text"}, {"age_fresh", "money"}, {"age_mid", "money"}, {"age_old", "money"}}
	case TablePayments:
		return []Col{{"day", "day"}, {"booking", "text"}, {"amount", "money"}, {"number", "text"}, {"match", "match"}}
	case TableReceipts:
		return []Col{{"day", "day"}, {"booking", "text"}, {"amount", "money"}, {"account", "text"}}
	case TableSubscriptions:
		return []Col{{"name", "text"}, {"monthly", "money"}, {"deadline", "day"}, {"last_use", "day"}}
	case TableBudgetRunOut:
		return []Col{{"project", "text"}, {"used", "bar"}, {"runout", "day"}, {"end", "day"}}
	case TableMargins:
		return []Col{{"project", "text"}, {"revenue", "money"}, {"expenses", "money"}, {"time_cost", "money"}, {"margin", "pct"}}
	}
	return nil
}

// ruleConfig reads a rule's settings of the space (defaults filled in).
func ruleConfig(id string, settings map[string]any) map[string]any {
	for _, spec := range rules.AllRules() {
		if spec.ID == id {
			return rules.Config(spec, settings)
		}
	}
	return map[string]any{}
}

// crossRows returns the rows of a cross table; ok is false for other kinds
// or a wrong own service.
func crossRows(kind TableKind, data any, results map[string]any, ctx ViewCtx) ([]Row, bool) {
	today := parseToday(ctx.Today)
	var rows []Row
	switch d := data.(type) {
	case *sources.NinjaDataset:
		if kind != TableFullRates {
			return nil, false
		}
		kimai, ok := results[peerKimai].(*sources.KimaiDataset)
		if !ok {
			return nil, true
		}
		geo, _ := results[peerDawarich].(*sources.DawarichDataset)
		mapping := metrics.ParseAreaMapping(ctx.PeerOptions[peerDawarich])
		for _, r := range metrics.FullCostRates(kimai, d, geo, mapping, today, crossRateDays) {
			rows = append(rows, Row{[]any{r.Customer, r.BillableH, r.OtherH, r.TravelH, r.Nominal, r.Full}})
		}
	case *sources.KimaiDataset:
		switch kind {
		case TableAging:
			for _, a := range metrics.UnbilledAging(d, today) {
				rows = append(rows, Row{[]any{a.Customer, a.Fresh, a.Mid, a.Old}})
			}
		case TableBudgetRunOut:
			for _, f := range metrics.BudgetForecasts(d, today) {
				rows = append(rows, Row{[]any{f.Project, f.Used, dayOrEmpty(f.RunOut), dayOrEmpty(f.End)}})
			}
		case TableMargins:
			ninja, _ := results[peerNinja].(*sources.NinjaDataset)
			for _, m := range metrics.ProjectMargins(d, ninja, costPerHour(ctx.Settings), today, crossMarginDays) {
				rows = append(rows, Row{[]any{m.Project, m.Revenue, m.Expenses, m.TimeCost, m.Margin}})
			}
		default:
			return nil, false
		}
	case *sources.SureDataset:
		switch kind {
		case TablePayments:
			ninja, ok := results[peerNinja].(*sources.NinjaDataset)
			if !ok {
				return nil, true
			}
			for _, m := range metrics.PaymentMatches(d, ninja, today, crossPaymentDays) {
				rows = append(rows, Row{[]any{m.Day.Format(time.DateOnly), m.Txn.Name, m.Txn.Amount, m.Invoice.Number, m.Reason}})
			}
		case TableReceipts:
			cfg := ruleConfig("cross.expense_unrecorded", ctx.Settings)
			in := metrics.ReceiptInputs{Sure: d, Accounts: asStringList(cfg["accounts"]), MinAmount: floatOf(cfg["min_amount"]),
				Window: int(floatOf(cfg["date_window"])), Since: today.AddDate(0, 0, -crossReceiptDays)}
			in.Ninja, _ = results[peerNinja].(*sources.NinjaDataset)
			in.Paperless, _ = results[peerPaperless].(*sources.PaperlessDataset)
			in.Mail, _ = results[peerMail].(*sources.MailDataset)
			for _, t := range metrics.MissingReceipts(in) {
				rows = append(rows, Row{[]any{t.Date, t.Name, -t.Amount, t.Account}})
			}
		case TableSubscriptions:
			var contracts []sources.PaperlessContract
			if p, ok := results[peerPaperless].(*sources.PaperlessDataset); ok {
				contracts = p.Contracts
			}
			env := rules.Env{Today: today, Datasets: map[string]any{}}
			if ak, ok := results[peerAuthentik]; ok {
				env.Datasets[string(enums.ServiceAuthentik)] = ak
			}
			for _, s := range metrics.Subscriptions(d, contracts, rules.Usages(env)) {
				rows = append(rows, Row{[]any{s.Name, s.Monthly, dayOrEmpty(s.Deadline), dayOrEmpty(s.LastUse)}})
			}
		default:
			return nil, false
		}
	default:
		return nil, false
	}
	return rows, true
}

func dayOrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.DateOnly)
}

// costPerHour mirrors the rules' internal hourly cost.
func costPerHour(settings map[string]any) float64 {
	costs := settingsMap(settings, "costs")
	if v := settingsFloat(costs, "hourly_cost", 0); v > 0 {
		return v
	}
	const monthHours = 21 * 8
	return settingsFloat(costs, "fixed_monthly", 0) / monthHours
}

func floatOf(v any) float64 {
	f, _ := v.(float64)
	return f
}
