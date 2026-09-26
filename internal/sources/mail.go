package sources

// Incoming invoices from a mailbox (IMAP, read-only). A mail counts as an
// invoice when its subject or an attachment name carries a keyword
// ("Rechnung", "Invoice", …); the amount is read from the text:
//
//	"Rechnungsbetrag: 1.234,56 €"  → 1234.56
//	"Total due: EUR 1,234.56"      → 1234.56

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"andon/internal/drivers/imapmail"
	"andon/internal/enums"
)

const (
	mailTTL        = 30 * time.Minute
	mailDays       = 60
	mailBodies     = 80
	imapsPort      = 993
	imapPort       = 143
	defaultMailbox = "INBOX"
)

var defaultInvoiceWords = []string{"rechnung", "invoice", "receipt", "quittung", "beleg", "faktura", "gutschrift"}

// MailInvoice is one mail that looks like an incoming invoice.
type MailInvoice struct {
	UID                  uint32
	Date                 time.Time
	Sender, Addr, Domain string
	Subject              string
	Amount               float64 // 0 if not found
	Attachments          []string
}

type MailDataset struct {
	Mailbox  string
	Scanned  int
	Invoices []MailInvoice
}

// amountRe finds money amounts with a currency marker on either side.
var amountRe = regexp.MustCompile(`(?i)(?:€|eur|euro)\s*(\d{1,3}(?:[.,\s]\d{3})*(?:[.,]\d{2})?)|(\d{1,3}(?:[.,\s]\d{3})*(?:[.,]\d{2})?)\s*(?:€|eur\b|euro)`)

// totalWords mark the amount to prefer over line items.
var totalWords = regexp.MustCompile(`(?i)(rechnungsbetrag|gesamtbetrag|gesamtsumme|zu zahlen|endbetrag|summe|total|amount due|betrag)`)

// ParseAmount reads "1.234,56" and "1,234.56" alike.
func ParseAmount(raw string) float64 {
	s := strings.ReplaceAll(strings.TrimSpace(raw), " ", "")
	lastComma, lastDot := strings.LastIndex(s, ","), strings.LastIndex(s, ".")
	switch {
	case lastComma > lastDot && len(s)-lastComma == 3: // German decimal comma
		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
	case lastDot > lastComma && len(s)-lastDot == 3: // English decimal dot
		s = strings.ReplaceAll(s, ",", "")
	default: // no decimals: separators group thousands
		s = strings.NewReplacer(".", "", ",", "").Replace(s)
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

// InvoiceAmount picks the amount after a total word, else the largest.
func InvoiceAmount(text string) float64 {
	if loc := totalWords.FindAllStringIndex(text, -1); len(loc) > 0 {
		for i := len(loc) - 1; i >= 0; i-- {
			tail := text[loc[i][1]:min(len(text), loc[i][1]+60)]
			if m := amountRe.FindStringSubmatch(tail); m != nil {
				return ParseAmount(m[1] + m[2])
			}
		}
	}
	best := 0.0
	for _, m := range amountRe.FindAllStringSubmatch(text, -1) {
		best = max(best, ParseAmount(m[1]+m[2]))
	}
	return best
}

func looksLikeInvoice(m imapmail.Message, words []string) bool {
	hay := strings.ToLower(m.Subject + " " + strings.Join(m.Attachments, " "))
	for _, w := range words {
		if strings.Contains(hay, w) {
			return true
		}
	}
	return false
}

// mailConfig reads imaps://host:993/INBOX; the secret is user:password.
func mailConfig(sctx Ctx) (imapmail.Config, error) {
	u, err := url.Parse(sctx.URL)
	if err != nil || u.Hostname() == "" {
		return imapmail.Config{}, newSourceError("bad mail URL")
	}
	user, pass, ok := strings.Cut(sctx.Secret, ":")
	if !ok {
		return imapmail.Config{}, newSourceError("credential.missing")
	}
	cfg := imapmail.Config{Host: u.Hostname(), TLS: u.Scheme != "imap", SkipVerify: !sctx.VerifyTLS, User: user, Pass: pass,
		Mailbox: strings.Trim(u.Path, "/")}
	if cfg.Mailbox == "" {
		cfg.Mailbox = defaultMailbox
	}
	cfg.Port, _ = strconv.Atoi(u.Port())
	if cfg.Port == 0 {
		cfg.Port = imapPort
		if cfg.TLS {
			cfg.Port = imapsPort
		}
	}
	return cfg, nil
}

type MailData struct{}

func (MailData) Key() string                { return "mail.data" }
func (MailData) TTL() time.Duration         { return mailTTL }
func (MailData) Service() enums.ServiceType { return enums.ServiceMail }

func (MailData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoMail(time.Now()), nil
	}
	cfg, err := mailConfig(sctx)
	if err != nil {
		return nil, err
	}
	words := defaultInvoiceWords
	for _, w := range asList(sctx.Options["keywords"]) {
		words = append(words, strings.ToLower(asStr(w)))
	}
	ignore := map[string]bool{strings.ToLower(cfg.User): true}
	for _, s := range asList(sctx.Options["ignore_senders"]) {
		ignore[strings.ToLower(asStr(s))] = true
	}
	days := int(asFloat(sctx.Options["days"]))
	if days <= 0 {
		days = mailDays
	}

	since := time.Now().UTC().AddDate(0, 0, -days)
	pick := func(m imapmail.Message) bool { return looksLikeInvoice(m, words) && !ignored(m.FromAddr, ignore) }
	msgs, err := imapmail.Recent(ctx, cfg, since, pick, mailBodies)
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}

	data := &MailDataset{Mailbox: cfg.Mailbox, Scanned: len(msgs)}
	for _, m := range msgs {
		if !pick(m) {
			continue
		}
		_, domain, _ := strings.Cut(m.FromAddr, "@")
		sender := m.FromName
		if sender == "" {
			sender = m.FromAddr
		}
		data.Invoices = append(data.Invoices, MailInvoice{UID: m.UID, Date: m.Date, Sender: sender, Addr: m.FromAddr, Domain: domain,
			Subject: m.Subject, Amount: InvoiceAmount(m.Subject + " " + m.Text), Attachments: m.Attachments})
	}
	return data, nil
}

// ignored matches whole addresses and domains ("shop.example").
func ignored(addr string, ignore map[string]bool) bool {
	_, domain, _ := strings.Cut(addr, "@")
	return ignore[addr] || ignore[domain]
}

func init() {
	Register(MailData{})
	Register(testOf{MailData{}, func(d any) map[string]any {
		m := d.(*MailDataset)
		return map[string]any{"mailbox": m.Mailbox, "scanned": m.Scanned, "invoices": len(m.Invoices)}
	}})
}

// paperlessTypes are attachment types Paperless can consume.
var paperlessTypes = map[string]bool{
	"application/pdf": true, "image/png": true, "image/jpeg": true, "image/tiff": true, "image/webp": true,
}

// MailFile is an attachment worth archiving.
type MailFile struct {
	Name    string
	Content []byte
}

// MailFiles downloads the PDF and image attachments of one mail.
func MailFiles(ctx context.Context, sctx Ctx, uid uint32) ([]MailFile, error) {
	cfg, err := mailConfig(sctx)
	if err != nil {
		return nil, err
	}
	all, err := imapmail.Attachments(ctx, cfg, uid)
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	var out []MailFile
	for _, a := range all {
		if paperlessTypes[a.MediaType] || strings.HasSuffix(strings.ToLower(a.Name), ".pdf") {
			out = append(out, MailFile{Name: a.Name, Content: a.Content})
		}
	}
	return out, nil
}
