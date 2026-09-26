package services

// REST clients for homelab services.
//
//	ScrutinyApi   no auth
//	ImmichApi     x-api-key
//	UmamiApi      login (user:password → bearer) or x-umami-api-key
//	FreshRSSApi   Google Reader ClientLogin (user:apipassword)
//	GiteaApi      "token" header
//	BorgApi       bearer bbs_tok_…

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"andon/internal/drivers/httpclient"
)

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// postJSON sends body as JSON and decodes the JSON answer.
func postJSON(ctx context.Context, rawURL string, headers map[string]string, body any, mode httpclient.TLS) (any, error) {
	return sendJSON(ctx, http.MethodPost, rawURL, headers, body, mode)
}

// sendJSON sends body as JSON with any method; nil body sends none.
func sendJSON(ctx context.Context, method, rawURL string, headers map[string]string, body any, mode httpclient.TLS) (any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	all := map[string]string{"Content-Type": "application/json", "Accept": "application/json"}
	for k, v := range headers {
		all[k] = v
	}
	if body == nil {
		raw = nil
	}
	resp, err := httpclient.Request(ctx, method, rawURL, httpclient.Options{Headers: all, Body: raw, SkipVerify: mode == httpclient.TLSSkip})
	if err != nil {
		return nil, ApiError{err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, ApiError{fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	var out any
	err = json.NewDecoder(io.LimitReader(resp.Body, httpclient.MaxBody)).Decode(&out)
	if errors.Is(err, io.EOF) {
		return nil, nil // 204 No Content
	}
	if err != nil {
		return nil, ApiError{"invalid JSON"}
	}
	return out, nil
}

// ── Scrutiny ──

type ScrutinyApi struct {
	URL    string
	Verify bool
}

// Summary returns /api/summary.
func (a ScrutinyApi) Summary(ctx context.Context) (any, error) {
	return fetchJSON(ctx, joinURL(a.URL, "api/summary"), nil, nil, httpclient.TLSOf(a.Verify))
}

// ── Immich ──

type ImmichApi struct {
	URL    string
	Key    string
	Verify bool
}

// Get performs one GET against /api/<path>.
func (a ImmichApi) Get(ctx context.Context, path string) (any, error) {
	return fetchJSON(ctx, joinURL(a.URL, "api/"+path), map[string]string{"x-api-key": a.Key, "Accept": "application/json"}, nil, httpclient.TLSOf(a.Verify))
}

// ── Umami ──

// UmamiApi: Secret is "user:password" (self-hosted login) or an API key.
type UmamiApi struct {
	URL    string
	Secret string
	Verify bool
}

// headers logs in when the secret is user:password.
func (a UmamiApi) headers(ctx context.Context) (map[string]string, error) {
	user, pass, login := strings.Cut(a.Secret, ":")
	if !login {
		return map[string]string{"x-umami-api-key": a.Secret, "Accept": "application/json"}, nil
	}
	body, err := postJSON(ctx, joinURL(a.URL, "api/auth/login"), nil, map[string]string{"username": user, "password": pass}, httpclient.TLSOf(a.Verify))
	if err != nil {
		return nil, err
	}
	token, _ := asMap(body)["token"].(string)
	if token == "" {
		return nil, ApiError{"login failed"}
	}
	return map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json"}, nil
}

// Session is a logged-in Umami client for several calls.
type UmamiSession struct {
	api     UmamiApi
	headers map[string]string
}

// Open logs in once.
func (a UmamiApi) Open(ctx context.Context) (UmamiSession, error) {
	h, err := a.headers(ctx)
	return UmamiSession{api: a, headers: h}, err
}

// Get performs one GET against /api/<path>.
func (s UmamiSession) Get(ctx context.Context, path string, params url.Values) (any, error) {
	return fetchJSON(ctx, joinURL(s.api.URL, "api/"+path), s.headers, params, httpclient.TLSOf(s.api.Verify))
}

// ── FreshRSS (Google Reader API) ──

// FreshRSSApi: Secret is "user:apipassword" (FreshRSS profile → API password).
type FreshRSSApi struct {
	URL    string
	Secret string
	Verify bool
}

const greaderBase = "api/greader.php/"

// Open logs in via ClientLogin and returns the auth header.
func (a FreshRSSApi) Open(ctx context.Context) (map[string]string, error) {
	user, pass, _ := strings.Cut(a.Secret, ":")
	text, err := httpclient.PostFormText(ctx, joinURL(a.URL, greaderBase+"accounts/ClientLogin"), httpclient.Options{
		Params: url.Values{"Email": {user}, "Passwd": {pass}}, SkipVerify: !a.Verify,
	})
	if err != nil {
		return nil, ApiError{err.Error()}
	}
	for _, line := range strings.Split(text, "\n") {
		if token, ok := strings.CutPrefix(strings.TrimSpace(line), "Auth="); ok {
			return map[string]string{"Authorization": "GoogleLogin auth=" + token}, nil
		}
	}
	return nil, ApiError{"login failed"}
}

// Get performs one GET against /api/greader.php/reader/api/0/<path>.
func (a FreshRSSApi) Get(ctx context.Context, auth map[string]string, path string) (any, error) {
	return fetchJSON(ctx, joinURL(a.URL, greaderBase+"reader/api/0/"+path), auth, url.Values{"output": {"json"}}, httpclient.TLSOf(a.Verify))
}

// ── Gitea ──

type GiteaApi struct {
	URL    string
	Token  string
	Verify bool
}

// Get performs one GET against /api/v1/<path>.
func (a GiteaApi) Get(ctx context.Context, path string, params url.Values) (any, error) {
	return fetchJSON(ctx, joinURL(a.URL, "api/v1/"+path), map[string]string{"Authorization": "token " + a.Token, "Accept": "application/json"}, params, httpclient.TLSOf(a.Verify))
}

// ── Borg Backup Server ──

type BorgApi struct {
	URL    string
	Token  string // bbs_tok_…
	Verify bool
}

// Get performs one GET against /api/v1/<path>.
func (a BorgApi) Get(ctx context.Context, path string) (any, error) {
	return fetchJSON(ctx, joinURL(a.URL, "api/v1/"+path), map[string]string{"Authorization": "Bearer " + a.Token, "Accept": "application/json"}, nil, httpclient.TLSOf(a.Verify))
}

// ── Home Assistant ──

// HassApi uses a long-lived access token.
type HassApi struct {
	URL    string
	Token  string
	Verify bool
}

func (a HassApi) headers() map[string]string {
	return map[string]string{"Authorization": "Bearer " + a.Token, "Accept": "application/json"}
}

// States returns every entity state (/api/states).
func (a HassApi) States(ctx context.Context) (any, error) {
	return fetchJSON(ctx, joinURL(a.URL, "api/states"), a.headers(), nil, httpclient.TLSOf(a.Verify))
}

// Call runs a service on one entity, e.g. ("switch", "toggle", "switch.fan").
func (a HassApi) Call(ctx context.Context, domain, service, entityID string) error {
	path := "api/services/" + url.PathEscape(domain) + "/" + url.PathEscape(service)
	_, err := postJSON(ctx, joinURL(a.URL, path), a.headers(), map[string]string{"entity_id": entityID}, httpclient.TLSOf(a.Verify))
	return err
}

// ── Sure (personal finance, successor of Maybe) ──

type SureApi struct {
	URL    string
	Key    string
	Verify bool
}

// Get performs one GET against /api/v1/<path>.
func (a SureApi) Get(ctx context.Context, path string, params url.Values) (any, error) {
	return fetchJSON(ctx, joinURL(a.URL, "api/v1/"+path), map[string]string{"X-Api-Key": a.Key, "Accept": "application/json"}, params, httpclient.TLSOf(a.Verify))
}

// ── Linkwarden ──

type LinkwardenApi struct {
	URL    string
	Token  string
	Verify bool
}

// Get performs one GET against /api/v1/<path> and returns its "response".
func (a LinkwardenApi) Get(ctx context.Context, path string, params url.Values) (any, error) {
	body, err := fetchJSON(ctx, joinURL(a.URL, "api/v1/"+path), map[string]string{"Authorization": "Bearer " + a.Token, "Accept": "application/json"}, params, httpclient.TLSOf(a.Verify))
	if err != nil {
		return nil, err
	}
	return asMap(body)["response"], nil
}
