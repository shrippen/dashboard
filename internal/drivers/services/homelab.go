package services

// REST clients for homelab services: Scrutiny, Immich, Umami.
//
//	ScrutinyApi   no auth
//	ImmichApi     x-api-key
//	UmamiApi      login (user:password → bearer) or x-umami-api-key

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"dashboard/internal/drivers/httpclient"
)

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// postJSON sends body as JSON and decodes the JSON answer.
func postJSON(ctx context.Context, rawURL string, headers map[string]string, body any, skipVerify bool) (any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	all := map[string]string{"Content-Type": "application/json", "Accept": "application/json"}
	for k, v := range headers {
		all[k] = v
	}
	resp, err := httpclient.Request(ctx, http.MethodPost, rawURL, httpclient.Options{Headers: all, Body: raw, SkipVerify: skipVerify})
	if err != nil {
		return nil, ApiError{err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, ApiError{fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	var out any
	if err := json.NewDecoder(io.LimitReader(resp.Body, httpclient.MaxBody)).Decode(&out); err != nil {
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
	return fetchJSON(ctx, joinURL(a.URL, "api/summary"), nil, nil, !a.Verify)
}

// ── Immich ──

type ImmichApi struct {
	URL    string
	Key    string
	Verify bool
}

// Get performs one GET against /api/<path>.
func (a ImmichApi) Get(ctx context.Context, path string) (any, error) {
	return fetchJSON(ctx, joinURL(a.URL, "api/"+path), map[string]string{"x-api-key": a.Key, "Accept": "application/json"}, nil, !a.Verify)
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
	body, err := postJSON(ctx, joinURL(a.URL, "api/auth/login"), nil, map[string]string{"username": user, "password": pass}, !a.Verify)
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
	return fetchJSON(ctx, joinURL(s.api.URL, "api/"+path), s.headers, params, !s.api.Verify)
}
