package sources

import (
	"context"
	"strconv"
	"strings"
	"time"

	"andon/internal/drivers/services"
	"andon/internal/enums"
)

// Kintsugi (the acquisition copilot) reports its open suggestions, their
// counts per status, open profile gaps, the last daily run and the
// research budget on /api/status.

// kintsugiNaive is how Kintsugi stored times before they carried a zone.
const kintsugiNaive = "2006-01-02T15:04:05"

// KintsugiKind tells acquisition (new work) from development (own skills).
type KintsugiKind string

const (
	KintsugiAcquisition KintsugiKind = "acquisition"
	KintsugiDevelopment KintsugiKind = "development"
)

// KintsugiRunFailed is the status of a daily run that did not finish.
const KintsugiRunFailed = "failed"

// KintsugiSuggestion is one open suggestion, linked into Kintsugi.
type KintsugiSuggestion struct {
	ID      int64
	Kind    KintsugiKind
	Title   string
	URL     string
	Created time.Time
}

// KintsugiRun is the last suggestion run.
type KintsugiRun struct {
	Status string
	Detail string
	At     time.Time
}

type KintsugiDataset struct {
	URL  string
	Open []KintsugiSuggestion
	// New, Accepted, Snoozed, Done, Rejected count suggestions per status.
	New, Accepted, Snoozed, Done, Rejected int
	// Rate is the share of decided suggestions taken up, in percent; -1
	// while none is decided.
	Rate     int
	GapsOpen int
	LastRun  *KintsugiRun
	// Research: the monthly web search budget, if research is on.
	Research           bool
	BudgetUSD, UsedUSD float64
}

// Oldest returns the creation time of the oldest open suggestion.
func (d *KintsugiDataset) Oldest() (time.Time, bool) {
	var oldest time.Time
	for _, s := range d.Open {
		if oldest.IsZero() || s.Created.Before(oldest) {
			oldest = s.Created
		}
	}
	return oldest, !oldest.IsZero()
}

type KintsugiData struct{}

func (KintsugiData) Key() string                { return "kintsugi.data" }
func (KintsugiData) TTL() time.Duration         { return dataTTL }
func (KintsugiData) Service() enums.ServiceType { return enums.ServiceKintsugi }

func (KintsugiData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoKintsugi(time.Now()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	body, err := services.KintsugiApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}.Status(ctx)
	if err != nil {
		return nil, fetchError(err)
	}
	return parseKintsugi(sctx.URL, asMap(body)), nil
}

func parseKintsugi(base string, body map[string]any) *KintsugiDataset {
	base = strings.TrimRight(base, "/")
	counts := asMap(body["counts"])
	research := asMap(body["research"])
	data := &KintsugiDataset{
		URL:       base,
		New:       int(asInt64(counts["new"])),
		Accepted:  int(asInt64(counts["accepted"])),
		Snoozed:   int(asInt64(counts["snoozed"])),
		Done:      int(asInt64(counts["done"])),
		Rejected:  int(asInt64(counts["rejected"])),
		Rate:      -1,
		GapsOpen:  int(asInt64(body["gaps_open"])),
		Research:  asBool(research["enabled"]),
		BudgetUSD: asFloat(research["budget_usd"]),
		UsedUSD:   asFloat(research["used_usd"]),
	}
	if rate, ok := body["acceptance_rate"].(float64); ok {
		data.Rate = int(rate)
	}

	for _, raw := range asList(body["open"]) {
		s := asMap(raw)
		id := asInt64(s["id"])
		data.Open = append(data.Open, KintsugiSuggestion{
			ID: id, Kind: KintsugiKind(asStr(s["kind"])), Title: asStr(s["title"]),
			URL:     base + "/vorschlaege#s-" + strconv.FormatInt(id, 10),
			Created: kintsugiAt(asStr(s["created_at"])),
		})
	}

	if run := asMap(body["last_run"]); run != nil {
		data.LastRun = &KintsugiRun{Status: asStr(run["status"]), Detail: asStr(run["detail"]), At: kintsugiAt(asStr(run["at"]))}
	}
	return data
}

// kintsugiAt reads a Kintsugi time with its offset; one without (older
// Kintsugi) counts as Andon's local time. Zero if unreadable.
func kintsugiAt(s string) time.Time {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t
	}
	t, err := time.ParseInLocation(kintsugiNaive, s, time.Local)
	if err != nil {
		return time.Time{}
	}
	return t
}

func init() {
	Register(KintsugiData{})
	Register(testOf{KintsugiData{}, func(d any) map[string]any { return map[string]any{"open": len(d.(*KintsugiDataset).Open)} }})
}
