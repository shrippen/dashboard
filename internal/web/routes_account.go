package web

import (
	"net/http"

	"andon/internal/enums"
	"andon/internal/services/admin"
	"andon/internal/services/auth"
	"andon/internal/services/invites"
	"andon/internal/services/oidc"
)

// RegisterAccountRoutes wires the public account flows: accept an
// invitation, self-register (if open), request and perform a password reset.
func (d Deps) RegisterAccountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /invite/{token}", d.handleInviteForm)
	mux.HandleFunc("POST /invite/{token}", d.handleInviteSubmit)
	mux.HandleFunc("GET /register", d.handleRegisterForm)
	mux.HandleFunc("POST /register", d.handleRegisterSubmit)
	mux.HandleFunc("GET /reset", d.handleResetRequestForm)
	mux.HandleFunc("POST /reset", d.handleResetRequest)
	mux.HandleFunc("GET /reset/{token}", d.handleResetForm)
	mux.HandleFunc("POST /reset/{token}", d.handleResetSubmit)
}

// loginAfter opens a session for a just-created account and goes home.
func (d Deps) loginAfter(w http.ResponseWriter, r *http.Request, email, password string) {
	result, err := auth.Login(d.DB, d.Settings, email, password, ClientIP(r), Agent(r))
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	d.setSession(w, result.Token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (d Deps) handleInviteForm(w http.ResponseWriter, r *http.Request) {
	ctx, _ := d.Context(r)
	token := r.PathValue("token")
	found, err := invites.Peek(d.DB, token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if found == nil {
		_ = d.Page(w, ctx, "account_message", http.StatusNotFound, map[string]any{"Message": "invite.invalid"})
		return
	}
	_ = d.Page(w, ctx, "invite", http.StatusOK, map[string]any{"Invite": found, "Token": token, "OIDCLabel": oidc.Button(d.DB, d.Settings)})
}

func (d Deps) handleInviteSubmit(w http.ResponseWriter, r *http.Request) {
	ctx, _ := d.Context(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	token, password := r.PathValue("token"), r.FormValue("password")

	email, err := invites.Accept(d.DB, token, r.FormValue("name"), password, enums.Locale(r.FormValue("locale")))
	if err != nil {
		found, _ := invites.Peek(d.DB, token)
		_ = d.Page(w, ctx, "invite", http.StatusBadRequest,
			map[string]any{"Invite": found, "Token": token, "Error": errKey(err)})
		return
	}
	d.loginAfter(w, r, email, password)
}

func (d Deps) handleRegisterForm(w http.ResponseWriter, r *http.Request) {
	ctx, _ := d.Context(r)
	open, err := admin.RegistrationOpen(d.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !open {
		_ = d.Page(w, ctx, "account_message", http.StatusNotFound, map[string]any{"Message": "register.closed"})
		return
	}
	_ = d.Page(w, ctx, "register", http.StatusOK, nil)
}

func (d Deps) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	ctx, _ := d.Context(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	password := r.FormValue("password")
	email, err := admin.Register(d.DB, r.FormValue("email"), r.FormValue("name"), password, enums.Locale(r.FormValue("locale")))
	if err != nil {
		_ = d.Page(w, ctx, "register", http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	d.loginAfter(w, r, email, password)
}

func (d Deps) handleResetRequestForm(w http.ResponseWriter, r *http.Request) {
	ctx, _ := d.Context(r)
	_ = d.Page(w, ctx, "reset_request", http.StatusOK, nil)
}

func (d Deps) handleResetRequest(w http.ResponseWriter, r *http.Request) {
	ctx, _ := d.Context(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := invites.RequestReset(d.DB, r.FormValue("email"), ClientIP(r)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = d.Page(w, ctx, "reset_request", http.StatusOK, map[string]any{"Sent": true})
}

func (d Deps) handleResetForm(w http.ResponseWriter, r *http.Request) {
	ctx, _ := d.Context(r)
	token := r.PathValue("token")
	valid, err := invites.ResetValid(d.DB, token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !valid {
		_ = d.Page(w, ctx, "account_message", http.StatusNotFound, map[string]any{"Message": "reset.invalid"})
		return
	}
	_ = d.Page(w, ctx, "reset", http.StatusOK, map[string]any{"Token": token})
}

func (d Deps) handleResetSubmit(w http.ResponseWriter, r *http.Request) {
	ctx, _ := d.Context(r)
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	token := r.PathValue("token")
	if err := invites.Reset(d.DB, token, r.FormValue("password"), ClientIP(r)); err != nil {
		_ = d.Page(w, ctx, "reset", http.StatusBadRequest, map[string]any{"Token": token, "Error": errKey(err)})
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}
