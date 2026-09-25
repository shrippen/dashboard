package sources

// Extra start-page sources for Dashy widgets: a remote image (inlined, so
// the browser never contacts the image host and CSP stays 'self') and
// ECB exchange rates.

import (
	"context"
	"encoding/base64"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"dashboard/internal/drivers/httpclient"
	"dashboard/internal/enums"
)

const (
	imageTTL     = time.Hour
	imageMax     = 1 << 20
	ratesTTL     = 6 * time.Hour
	frankfurter  = "https://api.frankfurter.app/latest"
	imagePrefix  = "image/"
	defaultBase  = "EUR"
	symbolsLimit = 20
)

// ── image ──

// ImageResult is the image as a data: URI.
type ImageResult struct{ DataURI string }

type ImageSource struct{}

func (ImageSource) Key() string                { return "image" }
func (ImageSource) TTL() time.Duration         { return imageTTL }
func (ImageSource) Service() enums.ServiceType { return "" }

func (ImageSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	return fetchImage(ctx, asStr(sctx.Params["url"]), imageMax)
}

// fetchImage downloads an image of at most limit bytes as a data: URI.
func fetchImage(ctx context.Context, target string, limit int) (*ImageResult, error) {
	resp, err := httpclient.Request(ctx, http.MethodGet, target, httpclient.Options{})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, newSourceError("HTTP %d", resp.StatusCode)
	}
	kind, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if !strings.HasPrefix(kind, imagePrefix) {
		return nil, newSourceError("not an image")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, int64(limit)+1))
	if err != nil || len(body) > limit {
		return nil, newSourceError("image too large")
	}
	return &ImageResult{DataURI: "data:" + kind + ";base64," + base64.StdEncoding.EncodeToString(body)}, nil
}

// ── exchange_rates ──

type Rate struct {
	Code  string
	Value float64
}

// RatesResult is one base currency against others (ECB reference rates).
type RatesResult struct {
	Base  string
	Day   string
	Rates []Rate
}

type RatesSource struct{}

func (RatesSource) Key() string                { return "exchange_rates" }
func (RatesSource) TTL() time.Duration         { return ratesTTL }
func (RatesSource) Service() enums.ServiceType { return "" }

func (RatesSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	base := strings.ToUpper(asStr(sctx.Params["base"]))
	if base == "" {
		base = defaultBase
	}
	query := url.Values{"from": {base}}
	symbols, _ := sctx.Params["symbols"].([]string)
	if len(symbols) > symbolsLimit {
		symbols = symbols[:symbolsLimit]
	}
	if len(symbols) > 0 {
		query.Set("to", strings.ToUpper(strings.Join(symbols, ",")))
	}

	body, _, err := httpclient.GetJSON(ctx, frankfurter, httpclient.Options{Params: query})
	if err != nil {
		return nil, newSourceError("%s", err.Error())
	}
	m := asMap(body)
	out := &RatesResult{Base: asStr(m["base"]), Day: asStr(m["date"])}
	for code, v := range asMap(m["rates"]) {
		out.Rates = append(out.Rates, Rate{Code: code, Value: asFloat(v)})
	}
	sort.Slice(out.Rates, func(i, j int) bool { return out.Rates[i].Code < out.Rates[j].Code })
	return out, nil
}

func init() {
	Register(ImageSource{})
	Register(RatesSource{})
}
