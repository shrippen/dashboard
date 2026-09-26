package web

import (
	"database/sql"
	"net/http"
	"net/url"
	"regexp"

	"andon/internal/services/access"
	"andon/internal/services/onboarding"
)

// Onboarding: the welcome page with its checklist and concepts, and the
// intro box each main page shows until it was closed.
//
//	login ─► /start ─► /welcome the first time, else /
//	page  ─► {{template "intro"}} ─► × ─► POST /welcome/intro/{page}
const startPath = "/start"

// introPage is a page key as used in intro boxes, e.g. "hints".
var introPage = regexp.MustCompile(`^[a-z_]{1,32}$`)

func (d Deps) RegisterWelcomeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+startPath, d.handleStart)
	mux.HandleFunc("GET /welcome", d.handleWelcome)
	mux.HandleFunc("POST /welcome/intro/{page}", d.handleIntroSeen)
	mux.HandleFunc("POST /welcome/dismiss", d.welcomeAction(onboarding.Dismiss))
	mux.HandleFunc("POST /welcome/resume", d.welcomeAction(onboarding.Resume))
	mux.HandleFunc("POST /welcome/intros", d.welcomeAction(onboarding.ShowIntros))
}

// handleStart opens the welcome page once after the first login.
func (d Deps) handleStart(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	state, err := onboarding.Load(d.DB, ctx.Who)
	if err == nil && !state.Shown {
		_ = onboarding.MarkShown(d.DB, ctx.Who)
		http.Redirect(w, r, "/welcome", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (d Deps) handleWelcome(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	state, err := onboarding.Load(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = d.Page(w, ctx, "welcome", http.StatusOK, map[string]any{"Onboarding": state})
}

// handleIntroSeen closes one page's intro; htmx removes the box.
func (d Deps) handleIntroSeen(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	page := r.PathValue("page")
	if !introPage.MatchString(page) {
		http.NotFound(w, r)
		return
	}
	if err := onboarding.SeeIntro(d.DB, ctx.Who, page); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// htmx swaps the box away; a plain form post goes back to the page.
	if r.Header.Get("HX-Request") == "" {
		back := "/"
		if ref, err := url.Parse(r.Referer()); err == nil && ref.Path != "" {
			back = safeNext(ref.Path)
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// welcomeAction runs one change and returns to the welcome page.
func (d Deps) welcomeAction(change func(*sql.DB, *access.Principal) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, err := d.Require(r)
		if err != nil {
			d.handleAuthError(w, r, err)
			return
		}
		if err := change(d.DB, ctx.Who); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/welcome", http.StatusSeeOther)
	}
}
