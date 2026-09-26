package sources

// Sign-in instead of a pasted token: an OAuth2 grant stands where the
// token would be. The connection (or personal credential) then holds only
// GrantMarker; the grant itself lives in oauth_grants and is resolved to a
// current access token right before a fetch.
//
//	refresh: access + refresh token from an authorization code (Gitea, Snipe-IT)
//	client:  client credentials, access token fetched on demand (Tailscale)

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"andon/internal/drivers/httpclient"
)

// GrantMarker is the secret of a connection whose token comes from a grant.
const GrantMarker = "grant:v1"

// renewAhead renews an access token this long before it expires.
const renewAhead = time.Minute

// GrantKind says how a grant gets a new access token.
type GrantKind string

const (
	GrantRefresh GrantKind = "refresh"
	GrantClient  GrantKind = "client"
)

// Grant is the stored token state of one sign-in.
type Grant struct {
	Kind     GrantKind `json:"kind"`
	TokenURL string    `json:"token_url"`
	Scope    string    `json:"scope,omitempty"`
	Access   string    `json:"access,omitempty"`
	Refresh  string    `json:"refresh,omitempty"`
	Expires  time.Time `json:"expires,omitzero"`
}

// OAuthClient is the client a manager registered in the service.
type OAuthClient struct {
	ID     string
	Secret string
}

// ParseClient splits a stored "id:secret".
func ParseClient(raw string) OAuthClient {
	id, secret, _ := strings.Cut(raw, ":")
	return OAuthClient{ID: id, Secret: secret}
}

// tokenAnswer is a token endpoint's JSON (RFC 6749 section 5.1).
type tokenAnswer struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ExpiresIn int64  `json:"expires_in"`
}

// ExchangeCode trades an authorization code (+ PKCE verifier, if any) for
// a refresh grant.
func ExchangeCode(ctx context.Context, tokenURL, code, redirectURI, verifier string, client OAuthClient, tls httpclient.TLS, now time.Time) (Grant, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}}
	if verifier != "" {
		form.Set("code_verifier", verifier)
	}
	g := Grant{Kind: GrantRefresh, TokenURL: tokenURL}
	return g.apply(ctx, form, client, tls, now)
}

// Fresh returns g with an access token that is valid for at least a minute;
// renewed reports that g changed and must be stored again.
func (g Grant) Fresh(ctx context.Context, client OAuthClient, tls httpclient.TLS, now time.Time) (Grant, bool, error) {
	if g.Access != "" && (g.Expires.IsZero() || now.Add(renewAhead).Before(g.Expires)) {
		return g, false, nil
	}

	form := url.Values{"grant_type": {"client_credentials"}}
	if g.Kind == GrantRefresh {
		if g.Refresh == "" {
			return g, false, newSourceError("grant.expired")
		}
		form = url.Values{"grant_type": {"refresh_token"}, "refresh_token": {g.Refresh}}
	}
	if g.Scope != "" {
		form.Set("scope", g.Scope)
	}
	next, err := g.apply(ctx, form, client, tls, now)
	if err != nil {
		return g, false, err
	}
	return next, true, nil
}

// apply posts form to the token endpoint and takes over the answer. A
// refresh answer without a new refresh token keeps the old one.
func (g Grant) apply(ctx context.Context, form url.Values, client OAuthClient, tls httpclient.TLS, now time.Time) (Grant, error) {
	form.Set("client_id", client.ID)
	if client.Secret != "" {
		form.Set("client_secret", client.Secret)
	}
	resp, err := httpclient.Request(ctx, http.MethodPost, g.TokenURL, httpclient.Options{
		Headers:    map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json"},
		Body:       []byte(form.Encode()),
		SkipVerify: tls == httpclient.TLSSkip,
	})
	if err != nil {
		return g, newSourceError("token: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return g, newSourceError("token: HTTP %d", resp.StatusCode)
	}

	var t tokenAnswer
	if err := json.NewDecoder(io.LimitReader(resp.Body, httpclient.MaxBody)).Decode(&t); err != nil || t.Access == "" {
		return g, newSourceError("token: invalid answer")
	}
	g.Access = t.Access
	if t.Refresh != "" {
		g.Refresh = t.Refresh
	}
	g.Expires = time.Time{}
	if t.ExpiresIn > 0 {
		g.Expires = now.Add(time.Duration(t.ExpiresIn) * time.Second)
	}
	return g, nil
}
