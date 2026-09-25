// Package web wires HTTP routes to the services layer: routes never call
// repos, sources or drivers directly (see agent.md's layering rule).
package web

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"dashboard/internal/crypto"
	"dashboard/internal/enums"
	"dashboard/internal/i18n"
	"dashboard/internal/services/access"
	"dashboard/internal/services/auth"
	"dashboard/internal/settings"
)

const (
	CookieName = "dsh_session"
	CSRFHeader = "X-CSRF-Token"
	CSRFField  = "csrf"
	bearer     = "Bearer "
)

// Ctx is everything a page needs about the requester.
type Ctx struct {
	Who    *access.Principal
	CSRF   string
	Method enums.AuthMethod
	Locale enums.Locale
}

// ErrLoginRequired is turned into a redirect to /login by the caller.
var ErrLoginRequired = errors.New("web: login required")

// ErrTOTPPending means the session exists but still needs its second factor.
var ErrTOTPPending = errors.New("web: totp pending")

// ErrCSRFFailed means the request's CSRF token was missing or wrong.
var ErrCSRFFailed = errors.New("web: csrf failed")

var safeMethods = map[string]bool{http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true}

// ClientIP returns the request's client address. Behind the reverse proxy,
// the server must be started with trusted proxy headers applied upstream
// of this handler (see cmd/dashboard's ReverseProxy wiring).
func ClientIP(r *http.Request) string {
	host, _, err := splitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func splitHostPort(addr string) (string, string, error) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return addr, "", nil
	}
	return addr[:i], addr[i+1:], nil
}

// Agent returns the request's User-Agent header.
func Agent(r *http.Request) string { return r.Header.Get("User-Agent") }

// Deps bundles what request handling needs from the rest of the app.
type Deps struct {
	DB       *sql.DB
	Settings settings.Settings
}

func (d Deps) session(r *http.Request) (*auth.SessionInfo, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return nil, nil
	}
	return auth.Resolve(d.DB, d.Settings, cookie.Value)
}

// Context builds a Ctx for any request, authenticated or not.
func (d Deps) Context(r *http.Request) (Ctx, error) {
	info, err := d.session(r)
	if err != nil {
		return Ctx{}, err
	}
	if info == nil {
		return Ctx{Locale: i18n.Pick(r.Header.Get("Accept-Language"))}, nil
	}
	locale := i18n.Pick(r.Header.Get("Accept-Language"))
	if info.Principal != nil {
		locale = info.Principal.Locale
	}
	return Ctx{Who: info.Principal, CSRF: info.CSRF, Method: info.Method, Locale: locale}, nil
}

// Require builds a Ctx and enforces that the caller is fully logged in
// (not pending 2FA) with a valid CSRF token on unsafe methods.
func (d Deps) Require(r *http.Request) (Ctx, error) {
	ctx, err := d.Context(r)
	if err != nil {
		return Ctx{}, err
	}
	info, err := d.session(r)
	if err != nil {
		return Ctx{}, err
	}
	if info != nil && info.Pending2FA {
		return Ctx{}, ErrTOTPPending
	}
	if ctx.Who == nil {
		return Ctx{}, ErrLoginRequired
	}
	if err := d.checkCSRF(r, ctx.CSRF); err != nil {
		return Ctx{}, err
	}
	return ctx, nil
}

func (d Deps) checkCSRF(r *http.Request, expected string) error {
	if safeMethods[r.Method] {
		return nil
	}
	sent := r.Header.Get(CSRFHeader)
	if sent == "" {
		sent = r.FormValue(CSRFField)
	}
	if sent == "" || expected == "" || !crypto.Same(sent, expected) {
		return ErrCSRFFailed
	}
	return nil
}

// Viewer builds a Ctx from the session, or from a bearer/query embed token
// on GET requests (iframes embedded in other dashboards).
func (d Deps) Viewer(r *http.Request) (Ctx, error) {
	ctx, err := d.Context(r)
	if err != nil {
		return Ctx{}, err
	}
	if ctx.Who != nil {
		return ctx, nil
	}
	who, err := d.tokenPrincipal(r, enums.TokenEmbed)
	if err != nil {
		return Ctx{}, err
	}
	if who == nil {
		return Ctx{}, ErrLoginRequired
	}
	return Ctx{Who: who, Locale: who.Locale}, nil
}

func (d Deps) tokenPrincipal(r *http.Request, scope enums.TokenScope) (*access.Principal, error) {
	header := r.Header.Get("Authorization")
	secret := r.URL.Query().Get("token")
	if strings.HasPrefix(header, bearer) {
		secret = strings.TrimPrefix(header, bearer)
	}
	if secret == "" {
		return nil, nil
	}
	return auth.PrincipalForToken(d.DB, secret, scope)
}

// IsHTMX reports whether the request came from an HTMX element.
func IsHTMX(r *http.Request) bool { return r.Header.Get("HX-Request") == "true" }

// SetSessionCookie writes the session cookie for token.
func SetSessionCookie(w http.ResponseWriter, token string, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie removes the session cookie.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// ctxKey is an unexported type so context values never collide across
// packages.
type ctxKey int

const requestCtxKey ctxKey = 0

// WithCtx stores a Ctx on the request context, for handlers reached via
// middleware to retrieve with FromRequest.
func WithCtx(r *http.Request, c Ctx) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), requestCtxKey, c))
}

// FromRequest retrieves a Ctx stored by WithCtx, if any.
func FromRequest(r *http.Request) (Ctx, bool) {
	c, ok := r.Context().Value(requestCtxKey).(Ctx)
	return c, ok
}
