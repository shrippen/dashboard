package rules

// Incoming invoices (mail, Paperless) against Invoice Ninja expenses:
//
//	recorded = an expense of the same amount (gross or net, ±1 ct) within
//	           the date window, or — without a readable amount — an
//	           expense of a vendor whose name matches the sender
//
//	"Hetzner Online GmbH" <billing@hetzner.com>  ~  vendor "Hetzner"

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

const minNameToken = 4

var nonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// legalForms are dropped before comparing names.
var legalForms = map[string]bool{"gmbh": true, "mbh": true, "ag": true, "kg": true, "ug": true, "inc": true, "ltd": true,
	"llc": true, "gbr": true, "ohg": true, "co": true, "online": true, "the": true, "und": true, "and": true}

func nameTokens(s string) []string {
	var out []string
	for _, t := range nonWord.Split(strings.ToLower(s), -1) {
		if len(t) >= minNameToken && !legalForms[t] {
			out = append(out, t)
		}
	}
	return out
}

// vendorMatches: a vendor name token appears in the sender name or domain.
func vendorMatches(vendor, sender, domain string) bool {
	hay := strings.ToLower(sender + " " + domain)
	for _, t := range nameTokens(vendor) {
		if strings.Contains(hay, t) {
			return true
		}
	}
	return false
}

// incoming is an invoice seen outside Invoice Ninja.
type incoming struct {
	sender, domain string
	amount         float64
	day            time.Time
}

func invoiceRecorded(ninja *sources.NinjaDataset, in incoming, window int) bool {
	vendors := map[string]string{}
	for _, v := range ninja.Vendors {
		vendors[v.Key] = v.Name
	}
	for _, e := range ninja.Expenses {
		d, ok := metrics.ParseDay(e.Date)
		if !ok || absDays(d, in.day) > window {
			continue
		}
		if in.amount > 0 && (absF(e.Amount-in.amount) <= centTolerance || absF(e.Amount-e.Tax-in.amount) <= centTolerance) {
			return true
		}
		if in.amount == 0 && vendorMatches(vendors[e.VendorKey], in.sender, in.domain) {
			return true
		}
	}
	return false
}

func shortHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", sum[:6])
}

func unrecordedFinding(rule, msg, fp string, level enums.Severity, ninja *sources.NinjaDataset, src string, params map[string]any) Finding {
	return Finding{Fingerprint: fp, Rule: rule, Severity: level, Message: msg, Params: params,
		ActionURL: strings.TrimRight(ninja.URL, "/") + "/expenses/create", ActionLabel: "open_in_invoiceninja",
		Sources: []string{src, string(enums.ServiceInvoiceNinja)}}
}

func amountParam(amount float64, currency string) any {
	if amount == 0 {
		return "–"
	}
	return Money(amount, currency)
}

func init() {
	mail := string(enums.ServiceMail)
	paperless := string(enums.ServicePaperless)
	defaults := map[string]any{"date_window": 30.0, "warn_days": 14.0}

	Register("mail.invoice_unrecorded", Cross, defaults, func(_ any, cfg map[string]any, env Env) []Finding {
		box, ok1 := env.Datasets[mail].(*sources.MailDataset)
		ninja, ok2 := env.Datasets[string(enums.ServiceInvoiceNinja)].(*sources.NinjaDataset)
		if !ok1 || !ok2 {
			return nil
		}
		var found []Finding
		for _, m := range box.Invoices {
			in := incoming{sender: m.Sender, domain: m.Domain, amount: m.Amount, day: m.Date}
			if invoiceRecorded(ninja, in, cfgInt(cfg, "date_window")) {
				continue
			}
			level := enums.SeverityInfo
			if env.Today.Sub(m.Date).Hours()/hoursPerDay >= cfgFloat(cfg, "warn_days") {
				level = enums.SeverityWarn
			}
			found = append(found, unrecordedFinding("mail.invoice_unrecorded", "mail.unrecorded", "mail:"+shortHash(m.Addr, m.Subject, m.Date.Format(time.DateOnly)),
				level, ninja, mail, map[string]any{"sender": m.Sender, "subject": m.Subject, "amount": amountParam(m.Amount, ninja.Currency), "day": Day(m.Date)}))
		}
		return found
	})

	Register("paperless.invoice_unrecorded", Cross, defaults, func(_ any, cfg map[string]any, env Env) []Finding {
		docs, ok1 := env.Datasets[paperless].(*sources.PaperlessDataset)
		ninja, ok2 := env.Datasets[string(enums.ServiceInvoiceNinja)].(*sources.NinjaDataset)
		if !ok1 || !ok2 {
			return nil
		}
		var found []Finding
		for _, d := range docs.Invoices {
			created, ok := metrics.ParseDay(d.Created)
			if !ok {
				continue
			}
			in := incoming{sender: d.Correspondent, amount: d.Amount, day: created}
			if invoiceRecorded(ninja, in, cfgInt(cfg, "date_window")) {
				continue
			}
			level := enums.SeverityInfo
			if env.Today.Sub(created).Hours()/hoursPerDay >= cfgFloat(cfg, "warn_days") {
				level = enums.SeverityWarn
			}
			f := unrecordedFinding("paperless.invoice_unrecorded", "paperless.unrecorded", fmt.Sprintf("doc:%d", d.ID),
				level, ninja, paperless, map[string]any{"sender": d.Correspondent, "subject": d.Title, "amount": amountParam(d.Amount, ninja.Currency), "day": Day(created)})
			f.ActionURL, f.ActionLabel = strings.TrimRight(docs.URL, "/")+fmt.Sprintf("/documents/%d/details", d.ID), "open_in_paperless"
			found = append(found, f)
		}
		return found
	})
}
