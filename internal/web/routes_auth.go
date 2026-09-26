package web

import (
	"errors"
	"net/http"

	"andon/internal/services/admin"
	"andon/internal/services/auth"
	"andon/internal/services/oidc"
)

// RegisterAuthRoutes wires the login/logout/TOTP/setup endpoints.
func (d Deps) RegisterAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /setup", d.handleSetupForm)
	mux.HandleFunc("POST /setup", d.handleSetupSubmit)
	mux.HandleFunc("GET /login", d.handleLoginForm)
	mux.HandleFunc("POST /login", d.handleLoginSubmit)
	mux.HandleFunc("GET /login/totp", d.handleTOTPForm)
	mux.HandleFunc("POST /login/totp", d.handleTOTPSubmit)
	mux.HandleFunc("POST /logout", d.handleLogout)
}

func (d Deps) handleSetupForm(w http.ResponseWriter, r *http.Request) {
	needed, err := auth.SetupNeeded(d.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !needed {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	ctx, _ := d.Context(r)
	_ = d.Page(w, ctx, "setup", http.StatusOK, nil)
}

func (d Deps) handleSetupSubmit(w http.ResponseWriter, r *http.Request) {
	ctx, _ := d.Context(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	err := auth.CreateAdmin(d.DB, r.FormValue("code"), r.FormValue("email"), r.FormValue("name"),
		r.FormValue("password"), ctx.Locale)
	if err != nil {
		_ = d.Page(w, ctx, "setup", http.StatusUnauthorized, map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (d Deps) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	ctx, _ := d.Context(r)
	if ctx.Who != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	// Fresh instance: nobody can log in yet, the first admin comes from /setup.
	if needed, err := auth.SetupNeeded(d.DB); err == nil && needed {
		http.Redirect(w, r, "/setup", http.StatusSeeOther)
		return
	}
	_ = d.Page(w, ctx, "login", http.StatusOK, d.loginExtras(nil))
}

// loginExtras adds what the login page links to (self-registration).
func (d Deps) loginExtras(values map[string]any) map[string]any {
	if values == nil {
		values = map[string]any{}
	}
	open, err := admin.RegistrationOpen(d.DB)
	values["RegistrationOpen"] = err == nil && open
	values["OIDCLabel"] = oidc.Button(d.DB, d.Settings)
	return values
}

func (d Deps) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	ctx, _ := d.Context(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	result, err := auth.Login(d.DB, d.Settings, r.FormValue("email"), r.FormValue("password"),
		ClientIP(r), Agent(r))
	if err != nil {
		status, key := http.StatusUnauthorized, "login.failed"
		switch {
		case errors.Is(err, auth.ErrThrottled):
			status, key = http.StatusTooManyRequests, "login.throttled"
		case errors.Is(err, auth.ErrOIDCOnly):
			key = "login.oidc_only"
		}
		_ = d.Page(w, ctx, "login", status, d.loginExtras(map[string]any{"Error": key, "Email": r.FormValue("email")}))
		return
	}

	d.setSession(w, result.Token)

	if result.Step == auth.StepTOTP {
		http.Redirect(w, r, "/login/totp", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (d Deps) handleTOTPForm(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	ctx, _ := d.Context(r)
	_ = d.Page(w, ctx, "totp", http.StatusOK, map[string]any{"Token": cookie.Value})
}

func (d Deps) handleTOTPSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	err = auth.TOTPVerify(d.DB, cookie.Value, r.FormValue("code"), ClientIP(r), Agent(r))
	if err != nil {
		ctx, _ := d.Context(r)
		_ = d.Page(w, ctx, "totp", http.StatusUnauthorized,
			map[string]any{"Token": cookie.Value, "Error": "Code ungültig."})
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (d Deps) handleLogout(w http.ResponseWriter, r *http.Request) {
	// Logout is a state change (it ends the session), so it needs the same
	// CSRF check as any other unsafe request — otherwise any third-party
	// page could force a log-out via a bare <form method=post action=...>.
	ctx, err := d.Context(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := d.checkCSRF(r, ctx.CSRF); err != nil {
		http.Error(w, "csrf", http.StatusForbidden)
		return
	}

	target := "/login"
	cookie, err := r.Cookie(CookieName)
	if err == nil {
		idToken, _ := auth.Logout(d.DB, cookie.Value)
		if idToken != "" {
			if end := oidc.LogoutURL(r.Context(), d.DB, d.Settings, idToken); end != "" {
				target = end
			}
		}
	}
	d.clearSession(w)
	// Drops the offline copies of boards along with the session.
	w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
	http.Redirect(w, r, target, http.StatusSeeOther)
}
