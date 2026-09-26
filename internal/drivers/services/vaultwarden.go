package services

// Vaultwarden admin API: POST /admin with the admin token sets the
// VW_ADMIN cookie; /admin/users then answers JSON.

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"andon/internal/drivers/httpclient"
)

const vaultwardenCookie = "VW_ADMIN"

type VaultwardenApi struct {
	URL    string
	Token  string // ADMIN_TOKEN (plain, as typed into the admin page)
	Verify bool
}

// Users logs in and lists the users.
func (a VaultwardenApi) Users(ctx context.Context) (any, error) {
	form := url.Values{"token": {a.Token}}.Encode()
	resp, err := httpclient.Request(ctx, http.MethodPost, joinURL(a.URL, "admin"), httpclient.Options{
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"},
		Body:    []byte(form), SkipVerify: !a.Verify, NoRedirect: true,
	})
	if err != nil {
		return nil, ApiError{err.Error()}
	}
	resp.Body.Close()

	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == vaultwardenCookie {
			cookie = c.Name + "=" + c.Value
		}
	}
	if cookie == "" {
		return nil, ApiError{"login failed"}
	}
	return fetchJSON(ctx, joinURL(a.URL, "admin/users"), map[string]string{"Cookie": cookie, "Accept": "application/json"}, nil, httpclient.TLSOf(a.Verify))
}

// Version reads the server version ("" if the endpoint is missing).
func (a VaultwardenApi) Version(ctx context.Context) string {
	text, err := httpclient.GetText(ctx, joinURL(a.URL, "api/version"), httpclient.Options{SkipVerify: !a.Verify})
	if err != nil {
		return ""
	}
	return strings.Trim(strings.TrimSpace(text), `"`)
}
