package web

import (
	"net/http"
	"strconv"

	"dashboard/internal/enums"
	"dashboard/internal/services/accounts"
	"dashboard/internal/services/auth"
	"dashboard/internal/services/oidc"
)

// RegisterSecurityRoutes wires /me/security: password, TOTP, sessions, API tokens.
func (d Deps) RegisterSecurityRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /me/security", d.handleSecurityPage)
	mux.HandleFunc("POST /me/security/password", d.handlePasswordChange)
	mux.HandleFunc("POST /me/security/totp/begin", d.handleTOTPBeginForm)
	mux.HandleFunc("POST /me/security/totp/confirm", d.handleTOTPConfirmForm)
	mux.HandleFunc("POST /me/security/totp/disable", d.handleTOTPDisableForm)
	mux.HandleFunc("POST /me/security/sessions/{id}/end", d.handleSessionEnd)
	mux.HandleFunc("POST /me/security/tokens", d.handleTokenCreate)
	mux.HandleFunc("POST /me/security/tokens/{id}/revoke", d.handleTokenRevoke)
}

func (d Deps) securityPage(w http.ResponseWriter, ctx Ctx, status int, extra map[string]any) {
	profile, err := accounts.GetProfile(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sessions, err := auth.MySessions(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tokens, err := auth.MyTokens(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	values := map[string]any{
		"Profile": profile, "Sessions": sessions, "Tokens": tokens, "OIDCLabel": oidc.Button(d.DB, d.Settings),
	}
	for k, v := range extra {
		values[k] = v
	}
	_ = d.Page(w, ctx, "security", status, values)
}

func (d Deps) handleSecurityPage(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	d.securityPage(w, ctx, http.StatusOK, nil)
}

func (d Deps) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	err = accounts.ChangePassword(d.DB, ctx.Who, r.FormValue("current"), r.FormValue("new"), ClientIP(r))
	if err != nil {
		d.securityPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, "/me/security", http.StatusSeeOther)
}

func (d Deps) handleTOTPBeginForm(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	secret, uri, err := auth.TOTPBegin(d.DB, ctx.Who)
	if err != nil {
		d.securityPage(w, ctx, http.StatusInternalServerError, map[string]any{"Error": errKey(err)})
		return
	}
	d.securityPage(w, ctx, http.StatusOK, map[string]any{"TOTPSecret": secret, "TOTPURI": uri})
}

func (d Deps) handleTOTPConfirmForm(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	codes, err := auth.TOTPConfirm(d.DB, ctx.Who, r.FormValue("code"), ClientIP(r))
	if err != nil {
		d.securityPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	d.securityPage(w, ctx, http.StatusOK, map[string]any{"RecoveryCodes": codes})
}

func (d Deps) handleTOTPDisableForm(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := auth.TOTPDisable(d.DB, ctx.Who, r.FormValue("code"), ClientIP(r)); err != nil {
		d.securityPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, "/me/security", http.StatusSeeOther)
}

func (d Deps) handleSessionEnd(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := auth.EndSession(d.DB, ctx.Who, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/me/security", http.StatusSeeOther)
}

func (d Deps) handleTokenCreate(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	scope := enums.TokenScope(r.FormValue("scope"))
	if scope == "" {
		scope = enums.TokenRead
	}
	var days *int
	if raw := r.FormValue("days"); raw != "" {
		if n, convErr := strconv.Atoi(raw); convErr == nil {
			days = &n
		}
	}
	created, err := auth.CreateToken(d.DB, ctx.Who, r.FormValue("name"), scope, nil, days)
	if err != nil {
		d.securityPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	d.securityPage(w, ctx, http.StatusOK, map[string]any{"NewToken": created.Secret})
}

func (d Deps) handleTokenRevoke(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := auth.RevokeToken(d.DB, ctx.Who, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/me/security", http.StatusSeeOther)
}
