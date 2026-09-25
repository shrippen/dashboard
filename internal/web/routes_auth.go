package web

import (
	"errors"
	"net/http"

	"dashboard/internal/services/admin"
	"dashboard/internal/services/auth"
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
	_ = d.Page(w, ctx, "login", http.StatusOK, d.loginExtras(nil))
}

// loginExtras adds what the login page links to (self-registration).
func (d Deps) loginExtras(values map[string]any) map[string]any {
	if values == nil {
		values = map[string]any{}
	}
	open, err := admin.RegistrationOpen(d.DB)
	values["RegistrationOpen"] = err == nil && open
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
		if errors.Is(err, auth.ErrThrottled) {
			status, key = http.StatusTooManyRequests, "login.throttled"
		}
		_ = d.Page(w, ctx, "login", status, d.loginExtras(map[string]any{"Error": key, "Email": r.FormValue("email")}))
		return
	}

	SetSessionCookie(w, result.Token, d.Settings.SecureCookies())

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

	cookie, err := r.Cookie(CookieName)
	if err == nil {
		_, _ = auth.Logout(d.DB, cookie.Value)
	}
	ClearSessionCookie(w, d.Settings.SecureCookies())
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
