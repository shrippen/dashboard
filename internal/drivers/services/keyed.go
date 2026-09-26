package services

// Services that authenticate with fixed headers (API key, bearer token)
// and answer JSON:
//
//	tailscale / headscale   Authorization: Bearer
//	opnsense                basic key:secret
//	pfsense (REST package)  X-API-Key
//	unifi (integration API) X-API-KEY
//	jellyfin                X-Emby-Token
//	plex                    X-Plex-Token
//	sonarr / radarr         X-Api-Key
//	speedtest tracker       Authorization: Bearer
//	grocy                   GROCY-API-KEY
//	github                  Authorization: Bearer (optional)
//	tibber                  Authorization: Bearer, GraphQL

import (
	"context"
	"encoding/base64"
	"net/url"

	"andon/internal/drivers/httpclient"
)

// KeyedApi reads JSON with fixed request headers.
type KeyedApi struct {
	URL     string
	Headers map[string]string
	Verify  bool
}

// BearerApi authenticates with "Authorization: Bearer <token>" ("" = none).
func BearerApi(base, token string, mode httpclient.TLS) KeyedApi {
	headers := map[string]string{"Accept": "application/json"}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	return KeyedApi{URL: base, Headers: headers, Verify: mode == httpclient.TLSVerify}
}

// HeaderApi authenticates with one named header.
func HeaderApi(base, name, value string, mode httpclient.TLS) KeyedApi {
	return KeyedApi{URL: base, Headers: map[string]string{"Accept": "application/json", name: value}, Verify: mode == httpclient.TLSVerify}
}

// BasicApi authenticates with "user:secret" as basic auth.
func BasicApi(base, userSecret string, mode httpclient.TLS) KeyedApi {
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(userSecret))
	return KeyedApi{URL: base, Headers: map[string]string{"Accept": "application/json", "Authorization": auth}, Verify: mode == httpclient.TLSVerify}
}

// Get reads one endpoint relative to the base URL.
func (a KeyedApi) Get(ctx context.Context, path string, params url.Values) (any, error) {
	return fetchJSON(ctx, joinURL(a.URL, path), a.Headers, params, httpclient.TLSOf(a.Verify))
}

// Post sends a JSON body (e.g. a GraphQL query) and decodes the answer;
// path "" posts to the base URL itself.
func (a KeyedApi) Post(ctx context.Context, path string, body any) (any, error) {
	target := a.URL
	if path != "" {
		target = joinURL(a.URL, path)
	}
	return postJSON(ctx, target, a.Headers, body, httpclient.TLSOf(a.Verify))
}
