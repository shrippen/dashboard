package web

import (
	"net/http"
	"strings"

	"dashboard/internal/services/auth"
	"dashboard/internal/services/oidc"
)

const profileHome = "/me/security"

// RegisterOIDCRoutes wires single sign-on: start, callback, and linking an
// existing account from the security page.
func (d Deps) RegisterOIDCRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /auth/oidc/login", d.handleOIDCLogin)
	mux.HandleFunc("GET "+oidc.CallbackPath, d.handleOIDCCallback)
	mux.HandleFunc("POST /me/oidc/link", d.handleOIDCLink)
}

// safeNext keeps ?next= to local paths: no open redirect ("//evil" is not local).
func safeNext(target string) string {
	if !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") {
		return "/"
	}
	return target
}

func (d Deps) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	target, err := oidc.AuthorizeURL(r.Context(), d.DB, d.Settings, safeNext(r.URL.Query().Get("next")), nil)
	if err != nil {
		d.loginError(w, r, http.StatusServiceUnavailable, errKey(err))
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (d Deps) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	result, err := oidc.Complete(r.Context(), d.DB, d.Settings, r.URL.Query())
	if err != nil {
		d.loginError(w, r, http.StatusUnauthorized, errKey(err))
		return
	}
	token, err := auth.OpenOIDCSession(d.DB, d.Settings, result.UserID, ClientIP(r), Agent(r), result.IDToken)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	SetSessionCookie(w, token, d.Settings.SecureCookies())
	http.Redirect(w, r, safeNext(result.Next), http.StatusSeeOther)
}

func (d Deps) handleOIDCLink(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	userID := ctx.Who.UserID
	target, err := oidc.AuthorizeURL(r.Context(), d.DB, d.Settings, profileHome, &userID)
	if err != nil {
		d.securityPage(w, ctx, http.StatusServiceUnavailable, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// loginError shows the login page with a translated error.
func (d Deps) loginError(w http.ResponseWriter, r *http.Request, status int, key string) {
	ctx, _ := d.Context(r)
	_ = d.Page(w, ctx, "login", status, d.loginExtras(map[string]any{"Error": key}))
}
