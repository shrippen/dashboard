// Package services holds the raw REST clients for the four services. Only
// HTTP, paging and auth live here.
//
//	KimaiApi        Bearer token, pages via X-Total-Pages
//	NinjaApi        X-API-TOKEN, pages via meta.pagination
//	SnipeApi        Bearer token, pages via limit/offset
//	DawarichApi     api_key query parameter
//	KumaApi         basic auth (API key), Prometheus text
//	ProxmoxApi      PVEAPIToken header
//	PaperlessApi    Token header
package services

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"dashboard/internal/drivers/httpclient"
)

const (
	notFound  = 404
	kimaiPage = 250
	ninjaPage = 100
	snipePage = 500
	maxPages  = 200
)

// ApiError means the service answered with an error or could not be reached.
type ApiError struct{ msg string }

func (e ApiError) Error() string { return e.msg }

// ApiMissing means the endpoint does not exist (e.g. a plugin isn't installed).
type ApiMissing struct{ msg string }

func (e ApiMissing) Error() string { return e.msg }

func fetchJSON(ctx context.Context, rawURL string, headers map[string]string, params url.Values, mode httpclient.TLS) (any, error) {
	body, _, err := fetchJSONWithHeaders(ctx, rawURL, headers, params, mode)
	return body, err
}

// fetchJSONWithHeaders is like fetchJSON but also returns the response
// headers, needed for paging (X-Total-Pages) and version headers.
func fetchJSONWithHeaders(ctx context.Context, rawURL string, headers map[string]string, params url.Values, mode httpclient.TLS) (any, http.Header, error) {
	body, respHeaders, err := httpclient.GetJSON(ctx, rawURL, httpclient.Options{
		Headers: headers, Params: params, SkipVerify: mode == httpclient.TLSSkip,
	})
	if err != nil {
		if isNotFound(err) {
			return nil, nil, ApiMissing{rawURL}
		}
		return nil, nil, ApiError{err.Error()}
	}
	return body, respHeaders, nil
}

func isNotFound(err error) bool {
	he, ok := err.(httpclient.HttpError)
	return ok && he.Error() == fmt.Sprintf("HTTP %d", notFound)
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

// ── Kimai ──

type KimaiApi struct {
	URL    string
	Token  string
	Verify bool // true = verify TLS (matches the connection's own setting, not inverted)
}

func (a KimaiApi) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + a.Token, "Accept": "application/json"}
}

// Get performs one GET against /api/<path>.
func (a KimaiApi) Get(ctx context.Context, path string, params url.Values) (any, error) {
	return fetchJSON(ctx, a.URL+"/api/"+path, a.headers(), params, httpclient.TLSOf(a.Verify))
}

// Pages follows Kimai's X-Total-Pages paging and returns every item.
func (a KimaiApi) Pages(ctx context.Context, path string, params url.Values) ([]any, error) {
	var items []any
	for page := 1; page <= maxPages; page++ {
		query := cloneValues(params)
		query.Set("page", strconv.Itoa(page))
		query.Set("size", strconv.Itoa(kimaiPage))

		body, headers, err := fetchJSONWithHeaders(ctx, a.URL+"/api/"+path, a.headers(), query, httpclient.TLSOf(a.Verify))
		if err != nil {
			return nil, err
		}
		items = append(items, asList(body)...)

		total := 1
		if v := headers.Get("X-Total-Pages"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				total = n
			}
		}
		if page >= total {
			break
		}
	}
	return items, nil
}

// ── Invoice Ninja ──

type NinjaApi struct {
	URL    string
	Token  string
	Verify bool
}

func (a NinjaApi) headers() map[string]string {
	return map[string]string{
		"X-API-TOKEN": a.Token, "X-Requested-With": "XMLHttpRequest", "Accept": "application/json",
	}
}

// Version pings the API and returns its X-App-Version header.
func (a NinjaApi) Version(ctx context.Context) (string, error) {
	_, headers, err := fetchJSONWithHeaders(ctx, a.URL+"/api/v1/ping", a.headers(), nil, httpclient.TLSOf(a.Verify))
	if err != nil {
		return "", err
	}
	return headers.Get("X-App-Version"), nil
}

// Pages follows Invoice Ninja's meta.pagination paging for one entity.
func (a NinjaApi) Pages(ctx context.Context, entity string, params url.Values) ([]any, error) {
	var items []any
	for page := 1; page <= maxPages; page++ {
		query := cloneValues(params)
		query.Set("per_page", strconv.Itoa(ninjaPage))
		query.Set("page", strconv.Itoa(page))

		body, err := fetchJSON(ctx, a.URL+"/api/v1/"+entity, a.headers(), query, httpclient.TLSOf(a.Verify))
		if err != nil {
			return nil, err
		}
		m := asMap(body)
		items = append(items, asList(m["data"])...)

		total := 1
		if meta := asMap(m["meta"]); meta != nil {
			if pagination := asMap(meta["pagination"]); pagination != nil {
				if n, ok := pagination["total_pages"].(float64); ok && n > 0 {
					total = int(n)
				}
			}
		}
		if page >= total {
			break
		}
	}
	return items, nil
}

// ── Snipe-IT ──

type SnipeApi struct {
	URL    string
	Token  string
	Verify bool
}

func (a SnipeApi) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + a.Token, "Accept": "application/json"}
}

// Get performs one GET against /api/v1/<path>.
func (a SnipeApi) Get(ctx context.Context, path string, params url.Values) (any, error) {
	return fetchJSON(ctx, a.URL+"/api/v1/"+path, a.headers(), params, httpclient.TLSOf(a.Verify))
}

// Rows follows Snipe-IT's limit/offset paging and returns every "rows" entry.
func (a SnipeApi) Rows(ctx context.Context, path string, params url.Values) ([]any, error) {
	var items []any
	offset := 0
	for i := 0; i < maxPages; i++ {
		query := cloneValues(params)
		query.Set("limit", strconv.Itoa(snipePage))
		query.Set("offset", strconv.Itoa(offset))

		body, err := a.Get(ctx, path, query)
		if err != nil {
			return nil, err
		}
		m := asMap(body)
		rows := asList(m["rows"])
		items = append(items, rows...)
		offset += len(rows)

		total := 0
		if n, ok := m["total"].(float64); ok {
			total = int(n)
		}
		if len(rows) == 0 || offset >= total {
			break
		}
	}
	return items, nil
}

// ── Glances ──

const defaultGlancesAPIVersion = 4

type GlancesApi struct {
	URL     string
	Token   string // "" if the Glances API has no auth configured
	Verify  bool
	Version int // 0 = defaultGlancesAPIVersion
}

func (a GlancesApi) headers() map[string]string {
	if a.Token == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + a.Token}
}

// Get performs one GET against /api/<version>/<path>.
func (a GlancesApi) Get(ctx context.Context, path string) (any, error) {
	version := a.Version
	if version == 0 {
		version = defaultGlancesAPIVersion
	}
	return fetchJSON(ctx, fmt.Sprintf("%s/api/%d/%s", strings.TrimRight(a.URL, "/"), version, path), a.headers(), nil, httpclient.TLSOf(a.Verify))
}

// ── Dawarich ──

type DawarichApi struct {
	URL    string
	Token  string
	Verify bool
}

// Get performs one GET against /api/v1/<path>, with api_key merged in.
func (a DawarichApi) Get(ctx context.Context, path string, params url.Values) (any, error) {
	query := cloneValues(params)
	query.Set("api_key", a.Token)
	return fetchJSON(ctx, a.URL+"/api/v1/"+path, map[string]string{"Accept": "application/json"}, query, httpclient.TLSOf(a.Verify))
}

// Version calls /api/v1/health and returns its X-Dawarich-Version header.
func (a DawarichApi) Version(ctx context.Context) (string, error) {
	query := url.Values{"api_key": {a.Token}}
	_, headers, err := fetchJSONWithHeaders(ctx, a.URL+"/api/v1/health", nil, query, httpclient.TLSOf(a.Verify))
	if err != nil {
		return "", err
	}
	return headers.Get("X-Dawarich-Version"), nil
}

func cloneValues(v url.Values) url.Values {
	out := url.Values{}
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// ── Uptime Kuma ──

// KumaApi reads Uptime Kuma's Prometheus metrics; the API key is the
// basic-auth password with an empty user.
type KumaApi struct {
	URL    string
	Key    string
	Verify bool
}

// Metrics returns the raw /metrics text. Redirects are not followed: a
// reverse proxy that sends /metrics to its login page would otherwise
// answer 200 with HTML, which reads as an instance without monitors.
func (a KumaApi) Metrics(ctx context.Context) (string, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(":" + a.Key))
	resp, err := httpclient.Request(ctx, http.MethodGet, strings.TrimRight(a.URL, "/")+"/metrics", httpclient.Options{
		Headers: map[string]string{"Authorization": "Basic " + auth}, SkipVerify: !a.Verify, NoRedirect: true,
	})
	if err != nil {
		return "", ApiError{err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusMultipleChoices && resp.StatusCode < http.StatusBadRequest {
		target, _ := url.Parse(resp.Header.Get("Location"))
		host := ""
		if target != nil {
			host = target.Hostname()
		}
		return "", ApiError{"redirected to " + host + " (login in front of /metrics?)"}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", ApiError{fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, httpclient.MaxBody))
	if err != nil {
		return "", ApiError{"read failed"}
	}
	return string(body), nil
}

// ── Proxmox VE ──

// ProxmoxApi talks to /api2/json with an API token
// ("user@pam!name=uuid").
type ProxmoxApi struct {
	URL    string
	Token  string
	Verify bool
}

// Get returns the "data" member of /api2/json/<path>.
func (a ProxmoxApi) Get(ctx context.Context, path string, params url.Values) (any, error) {
	body, err := fetchJSON(ctx, strings.TrimRight(a.URL, "/")+"/api2/json/"+path,
		map[string]string{"Authorization": "PVEAPIToken=" + a.Token}, params, httpclient.TLSOf(a.Verify))
	if err != nil {
		return nil, err
	}
	return asMap(body)["data"], nil
}

// ── Paperless-ngx ──

type PaperlessApi struct {
	URL    string
	Token  string
	Verify bool
}

// Get performs one GET against /api/<path>. A pinned Accept version gets
// rejected with 406 once a paperless-ngx instance drops support for it, so
// this asks for whatever version the server currently serves.
func (a PaperlessApi) Get(ctx context.Context, path string, params url.Values) (any, error) {
	return fetchJSON(ctx, strings.TrimRight(a.URL, "/")+"/api/"+path, map[string]string{
		"Authorization": "Token " + a.Token, "Accept": "application/json",
	}, params, httpclient.TLSOf(a.Verify))
}

// ── TLS certificates ──

// CertInfo is what a TLS endpoint's leaf certificate says about itself.
type CertInfo struct {
	NotAfter time.Time
	Issuer   string
}

// PeerCert reads host:port's leaf certificate.
func PeerCert(ctx context.Context, hostPort string) (CertInfo, error) {
	cert, err := httpclient.PeerCert(ctx, hostPort)
	if err != nil {
		return CertInfo{}, ApiError{err.Error()}
	}
	return CertInfo{NotAfter: cert.NotAfter.UTC(), Issuer: cert.Issuer.CommonName}, nil
}
