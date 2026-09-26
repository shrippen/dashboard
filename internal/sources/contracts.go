package sources

// Contracts from Paperless: documents tagged "Vertrag" (option
// contract_tag) with their end date and notice period, from custom fields
// or, failing that, from the OCR text.
//
//	custom field "Vertragsende" 2026-12-31, "Kündigungsfrist" "3 Monate"
//	text "Laufzeit bis 31.12.2026 … Kündigungsfrist von 3 Monaten"
//	  → deadline 2026-09-30 (cancel before this day)
//	"verlängert sich … um 12 Monate" rolls a passed end forward

import (
	"context"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"andon/internal/drivers/services"
)

const (
	defaultContractTag = "Vertrag"
	contractPages      = 2
	defaultRenewMonths = 12
	germanDate         = "2.1.2006"
)

// PaperlessContract is one contract with its cancellation deadline; End
// is zero when neither fields nor text name one.
type PaperlessContract struct {
	ID              int64
	Title           string
	Correspondent   string
	End             time.Time
	NoticeMonths    int
	NoticeDays      int
	Deadline        time.Time // last day to cancel
	RenewsAutomatic bool
}

var (
	endRe    = regexp.MustCompile(`(?i)(?:vertragsende|laufzeit bis|endet am|befristet bis|läuft bis)\D{0,20}(\d{1,2}\.\d{1,2}\.\d{4})`)
	noticeRe = regexp.MustCompile(`(?i)kündigungsfrist\D{0,20}(\d{1,2})\s*(monat|woche|tag)`)
	renewRe  = regexp.MustCompile(`(?i)verlängert sich[^.]{0,80}?(\d{1,2})\s*monat`)
	autoRe   = regexp.MustCompile(`(?i)verlängert sich`)
)

// fieldNames maps lower-case custom field names to their meaning.
var contractFields = map[string]string{
	"vertragsende": "end", "laufzeit bis": "end", "contract end": "end",
	"kündigungsfrist": "notice", "notice period": "notice",
}

// ContractTerms reads end and notice from fields (by meaning) and text,
// then computes the next cancellation deadline after today.
func ContractTerms(fields map[string]string, text string, today time.Time) PaperlessContract {
	var c PaperlessContract
	if v := fields["end"]; v != "" {
		c.End, _ = time.Parse(time.DateOnly, v)
	}
	if c.End.IsZero() {
		if m := endRe.FindStringSubmatch(text); m != nil {
			c.End, _ = time.Parse(germanDate, m[1])
		}
	}

	notice := fields["notice"]
	if notice == "" {
		if m := noticeRe.FindStringSubmatch(text); m != nil {
			notice = m[1] + " " + m[2]
		}
	}
	c.NoticeMonths, c.NoticeDays = parseNotice(notice)

	renew := defaultRenewMonths
	if m := renewRe.FindStringSubmatch(text); m != nil {
		renew, _ = strconv.Atoi(m[1])
	}
	c.RenewsAutomatic = autoRe.MatchString(text)
	if c.End.IsZero() {
		return c
	}

	// An auto-renewing contract whose deadline passed runs another term.
	for c.RenewsAutomatic && renew > 0 && deadline(c).Before(today) {
		c.End = addMonths(c.End, renew)
	}
	c.Deadline = deadline(c)
	return c
}

func deadline(c PaperlessContract) time.Time {
	return addMonths(c.End, -c.NoticeMonths).AddDate(0, 0, -c.NoticeDays)
}

// addMonths keeps the day within the target month: 31.12. − 3 months =
// 30.09., not 01.10. as time.AddDate would give.
func addMonths(t time.Time, months int) time.Time {
	first := time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, months, 0)
	last := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(t.Day(), last)-1)
}

// parseNotice: "3 Monate" → (3, 0), "6 Wochen" → (0, 42), "30 Tage" → (0, 30).
func parseNotice(raw string) (int, int) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return 0, 0
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0
	}
	unit := "monat"
	if len(fields) > 1 {
		unit = fields[1]
	}
	switch {
	case strings.HasPrefix(unit, "woche"), strings.HasPrefix(unit, "week"):
		return 0, n * 7
	case strings.HasPrefix(unit, "tag"), strings.HasPrefix(unit, "day"):
		return 0, n
	}
	return n, 0
}

// loadContracts reads the contract documents; failures leave it empty.
func loadContracts(ctx context.Context, api services.PaperlessApi, options map[string]any, today time.Time) []PaperlessContract {
	tag := asStr(options["contract_tag"])
	if tag == "" {
		tag = defaultContractTag
	}
	meaning := map[int64]string{}
	if raw, err := api.Get(ctx, "custom_fields/", url.Values{"page_size": {"100"}}); err == nil {
		for _, r := range asList(asMap(raw)["results"]) {
			m := asMap(r)
			if kind, ok := contractFields[strings.ToLower(asStr(m["name"]))]; ok {
				meaning[asInt64(m["id"])] = kind
			}
		}
	}
	correspondents := map[int64]string{}
	if raw, err := api.Get(ctx, "correspondents/", url.Values{"page_size": {"100"}}); err == nil {
		for _, r := range asList(asMap(raw)["results"]) {
			m := asMap(r)
			correspondents[asInt64(m["id"])] = asStr(m["name"])
		}
	}

	var out []PaperlessContract
	for page := 1; page <= contractPages; page++ {
		raw, err := api.Get(ctx, "documents/", url.Values{"tags__name__iexact": {tag}, "page_size": {"100"}, "page": {strconv.Itoa(page)}})
		if err != nil {
			break
		}
		body := asMap(raw)
		for _, r := range asList(body["results"]) {
			d := asMap(r)
			fields := map[string]string{}
			for _, f := range asList(d["custom_fields"]) {
				fm := asMap(f)
				if kind, ok := meaning[asInt64(fm["field"])]; ok {
					fields[kind] = asStr(fm["value"])
					if n := asFloat(fm["value"]); n != 0 && kind == "notice" {
						fields[kind] = strconv.Itoa(int(n)) // plain number = months
					}
				}
			}
			c := ContractTerms(fields, asStr(d["content"]), today)
			c.ID, c.Title, c.Correspondent = asInt64(d["id"]), asStr(d["title"]), correspondents[asInt64(d["correspondent"])]
			out = append(out, c)
		}
		if body["next"] == nil {
			break
		}
	}
	return out
}
