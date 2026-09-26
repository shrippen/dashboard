// Package assist asks Claude for help, only when a user clicks for it:
//
//	Advise       "Was tun?" for one hint: its title, reason, rule and
//	             services go out, never location hints; kept per language
//	             until the hint text changes
//	ReadInvoice  invoice fields from a mail's PDF or image attachments
package assist

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"andon/internal/db"
	"andon/internal/drivers/llm"
	"andon/internal/i18n"
	data "andon/internal/repos/data"
	"andon/internal/services/access"
	"andon/internal/services/hints"
	"andon/internal/settings"
)

// locationRules never leave the instance (where a user was).
const locationRules = "geo."

var (
	// ErrOff means the instance has no API key.
	ErrOff = errors.New("assist.off")
	// ErrPrivate means the hint must not leave the instance.
	ErrPrivate = errors.New("assist.private")
	// ErrUnreadable means no invoice fields came back.
	ErrUnreadable = errors.New("assist.unreadable")
)

var apiKey string

// complete is the LLM call; tests swap it.
var complete = llm.CompleteFiles

// Init reads the API key; without one the features stay hidden.
func Init(cfg settings.Settings) { apiKey = cfg.AnthropicAPIKey }

// Enabled reports whether the instance has an API key.
func Enabled() bool { return apiKey != "" }

// Advise returns concrete next steps for one hint.
func Advise(ctx context.Context, d *sql.DB, who *access.Principal, hintID int64) (string, error) {
	if !Enabled() {
		return "", ErrOff
	}
	hint, err := hints.One(d, who, hintID)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(hint.Rule, locationRules) {
		return "", ErrPrivate
	}

	locale := who.Locale
	prompt := fmt.Sprintf("%s: %s\n%s: %s\n%s: %s\n%s: %s\n%s: %s\n",
		i18n.T("assist.services", locale, nil), strings.Join(hint.Sources, ", "),
		i18n.T("assist.rule", locale, nil), i18n.T("rule_name."+hint.Rule, locale, nil),
		i18n.T("assist.level", locale, nil), i18n.T("severity."+hint.Severity.Key(), locale, nil),
		i18n.T("assist.hint", locale, nil), hint.Title,
		i18n.T("assist.why", locale, nil), hint.Why)
	sum := sha256.Sum256([]byte(prompt))
	digest := fmt.Sprintf("%x", sum[:8])

	if text, ok, err := data.Advice(d, hintID, string(locale), digest); err != nil || ok {
		return text, err
	}
	text, err := complete(ctx, apiKey, i18n.T("assist.advice_system", locale, nil), prompt, nil)
	if err != nil {
		return "", err
	}
	return text, db.WithTx(d, func(tx *sql.Tx) error { return data.SaveAdvice(tx, hintID, string(locale), digest, text) })
}

// Invoice is what an invoice says about itself; empty when unknown.
type Invoice struct {
	Vendor   string  `json:"vendor"`
	Number   string  `json:"number"`
	Date     string  `json:"date"`
	Due      string  `json:"due"`
	Net      float64 `json:"net"`
	VAT      float64 `json:"vat"`
	Gross    float64 `json:"gross"`
	Currency string  `json:"currency"`
}

// Title names an invoice for Paperless: "Hetzner R0012345".
func (i Invoice) Title() string {
	return strings.TrimSpace(i.Vendor + " " + i.Number)
}

// ReadInvoice reads invoice fields from attachments.
func ReadInvoice(ctx context.Context, files []llm.File) (Invoice, error) {
	if !Enabled() {
		return Invoice{}, ErrOff
	}
	answer, err := complete(ctx, apiKey, invoiceSystem, invoicePrompt, files)
	if err != nil {
		return Invoice{}, err
	}
	start, end := strings.Index(answer, "{"), strings.LastIndex(answer, "}")
	if start < 0 || end < start {
		return Invoice{}, ErrUnreadable
	}
	var inv Invoice
	if err := json.Unmarshal([]byte(answer[start:end+1]), &inv); err != nil || (inv.Vendor == "" && inv.Gross == 0) {
		return Invoice{}, ErrUnreadable
	}
	return inv, nil
}

// The invoice prompt is fixed English: the answer is data, not prose.
const (
	invoiceSystem = "You read invoices. Answer with one JSON object only, no prose."
	invoicePrompt = `Read the attached invoice and return:
{"vendor": "", "number": "", "date": "YYYY-MM-DD", "due": "YYYY-MM-DD", "net": 0, "vat": 0, "gross": 0, "currency": "EUR"}
Use "" or 0 for anything the invoice does not state. Amounts as numbers with a dot.`
)
