// Package services holds the raw REST clients for the four services. Only
// HTTP, paging and auth live here.
//
//	KimaiApi        Bearer token, pages via X-Total-Pages
//	NinjaApi        X-API-TOKEN, pages via meta.pagination
//	SnipeApi        Bearer token, pages via limit/offset
//	DawarichApi     api_key query parameter
package services

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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

func fetchJSON(ctx context.Context, rawURL string, headers map[string]string, params url.Values, skipVerify bool) (any, error) {
	body, _, err := fetchJSONWithHeaders(ctx, rawURL, headers, params, skipVerify)
	return body, err
}

// fetchJSONWithHeaders is like fetchJSON but also returns the response
// headers, needed for paging (X-Total-Pages) and version headers.
func fetchJSONWithHeaders(ctx context.Context, rawURL string, headers map[string]string, params url.Values, skipVerify bool) (any, http.Header, error) {
	body, respHeaders, err := httpclient.GetJSON(ctx, rawURL, httpclient.Options{
		Headers: headers, Params: params, SkipVerify: skipVerify,
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
	return fetchJSON(ctx, a.URL+"/api/"+path, a.headers(), params, !a.Verify)
}

// Pages follows Kimai's X-Total-Pages paging and returns every item.
func (a KimaiApi) Pages(ctx context.Context, path string, params url.Values) ([]any, error) {
	var items []any
	for page := 1; page <= maxPages; page++ {
		query := cloneValues(params)
		query.Set("page", strconv.Itoa(page))
		query.Set("size", strconv.Itoa(kimaiPage))

		body, headers, err := fetchJSONWithHeaders(ctx, a.URL+"/api/"+path, a.headers(), query, !a.Verify)
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
	_, headers, err := fetchJSONWithHeaders(ctx, a.URL+"/api/v1/ping", a.headers(), nil, !a.Verify)
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

		body, err := fetchJSON(ctx, a.URL+"/api/v1/"+entity, a.headers(), query, !a.Verify)
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
	return fetchJSON(ctx, a.URL+"/api/v1/"+path, a.headers(), params, !a.Verify)
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
	return fetchJSON(ctx, fmt.Sprintf("%s/api/%d/%s", strings.TrimRight(a.URL, "/"), version, path), a.headers(), nil, !a.Verify)
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
	return fetchJSON(ctx, a.URL+"/api/v1/"+path, map[string]string{"Accept": "application/json"}, query, !a.Verify)
}

// Version calls /api/v1/health and returns its X-Dawarich-Version header.
func (a DawarichApi) Version(ctx context.Context) (string, error) {
	query := url.Values{"api_key": {a.Token}}
	_, headers, err := fetchJSONWithHeaders(ctx, a.URL+"/api/v1/health", nil, query, !a.Verify)
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
