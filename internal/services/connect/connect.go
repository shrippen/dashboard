// Package connect signs a connection in with the service instead of a
// pasted token. Both ways are equal: the result lands where a token typed
// into the form would (see connections.StoreSignIn).
//
//	Home Assistant  redirect → callback → long-lived token      no registration
//	Nextcloud       login link in a new tab, Andon polls         no registration
//	Jellyfin        code shown, approved in Jellyfin, Andon polls
//	Gitea, Snipe-IT redirect → callback → refresh grant          OAuth client in the service
//	Tailscale       client credentials, no browser step          OAuth client in Tailscale
//
// Flows in progress live in memory for flowTTL, bound to user and
// connection.
package connect

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"andon/internal/drivers/httpclient"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/services/access"
	"andon/internal/services/connections"
	"andon/internal/settings"
	"andon/internal/sources"
)

// CallbackPath is where services send the browser back to.
const CallbackPath = "/connections/connect/callback"

const (
	flowTTL     = 10 * time.Minute
	randomBytes = 24

	// tailscaleTokenURL issues access tokens for Tailscale OAuth clients.
	tailscaleTokenURL = "https://api.tailscale.com/api/v2/oauth/token"
	tailscaleHost     = "api.tailscale.com"
	// giteaScopes cover what the Gitea source reads.
	giteaScopes = "read:user read:repository read:issue read:notification"
	// mediaJellyfin is the media server kind with Quick Connect.
	mediaJellyfin = "jellyfin"
)

var (
	// ErrUnsupported means the service has no sign-in, only a token.
	ErrUnsupported = errors.New("connect.unsupported")
	// ErrFlow means the flow is unknown, expired or someone else's.
	ErrFlow = errors.New("connect.flow")
	// ErrNoClient means an OAuth client must be registered first.
	ErrNoClient = connections.ErrNoClient
)

// Method is how a service signs in.
type Method string

const (
	MethodNone     Method = ""
	MethodRedirect Method = "redirect" // browser goes to the service and back
	MethodLink     Method = "link"     // login link in a new tab, Andon polls
	MethodCode     Method = "code"     // code shown here, approved there, Andon polls
	MethodClient   Method = "client"   // registered client only, no browser step
)

// MethodOf says whether and how a connection can sign in.
func MethodOf(service enums.ServiceType, url string, options map[string]any) Method {
	switch service {
	case enums.ServiceHomeAssistant, enums.ServiceGitea, enums.ServiceSnipeIT:
		return MethodRedirect
	case enums.ServiceNextcloud:
		return MethodLink
	case enums.ServiceMediaServer:
		if kind, _ := options["kind"].(string); kind == "" || kind == mediaJellyfin {
			return MethodCode
		}
	case enums.ServiceTailscale:
		if strings.Contains(url, tailscaleHost) {
			return MethodClient
		}
	}
	return MethodNone
}

// NeedsClient says whether a manager registers an OAuth client first.
func NeedsClient(service enums.ServiceType) bool {
	return service == enums.ServiceGitea || service == enums.ServiceSnipeIT || service == enums.ServiceTailscale
}

// Step tells the page what to do next.
type Step struct {
	Redirect string // send the browser here
	Link     string // open this in a new tab
	Code     string // show this code
	Flow     string // poll with this id
	Done     bool   // stored, back to the connection
}

type flow struct {
	user, connID int64
	back         string // local page to return to
	service      enums.ServiceType
	verifier     string
	nextcloud    sources.NextcloudLogin
	jellyfin     sources.JellyfinQuick
	at           time.Time
}

var (
	flowsMu sync.Mutex
	flows   = map[string]flow{}
)

func remember(f flow) string {
	id := randomToken()
	flowsMu.Lock()
	defer flowsMu.Unlock()
	for k, v := range flows {
		if time.Since(v.at) > flowTTL {
			delete(flows, k)
		}
	}
	f.at = time.Now()
	flows[id] = f
	return id
}

// recall returns who's flow id; take removes it (callback, success).
func recall(who *access.Principal, id string, take bool) (flow, error) {
	flowsMu.Lock()
	defer flowsMu.Unlock()
	f, ok := flows[id]
	if !ok || time.Since(f.at) > flowTTL || f.user != who.UserID {
		return flow{}, ErrFlow
	}
	if take {
		delete(flows, id)
	}
	return f, nil
}

func randomToken() string {
	b := make([]byte, randomBytes)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// challenge is the PKCE S256 code challenge of verifier.
func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func base(env settings.Settings) string {
	return strings.TrimRight(env.BaseURL, "/")
}

func service(conn *model.Connection) string {
	return strings.TrimRight(conn.URL, "/")
}

func tlsOf(conn *model.Connection) httpclient.TLS {
	return httpclient.TLSOf(conn.VerifyTLS)
}

// deviceID names Andon towards Jellyfin, one per connection and user.
func deviceID(connID, user int64) string {
	return "andon-" + strconv.FormatInt(connID, 10) + "-" + strconv.FormatInt(user, 10)
}

// localPath keeps back on this site: "/me/credentials", never "//evil".
func localPath(back string) string {
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") {
		return "/"
	}
	return back
}

// Start begins signing in connection connID for who; back is the page to
// return to afterwards.
func Start(ctx context.Context, d *sql.DB, who *access.Principal, env settings.Settings, connID int64, back string) (Step, error) {
	conn, err := connections.Target(d, who, connID)
	if err != nil {
		return Step{}, err
	}
	svc := enums.ServiceType(conn.Service)
	f := flow{user: who.UserID, connID: conn.ID, service: svc, back: localPath(back)}

	switch MethodOf(svc, conn.URL, conn.Options) {
	case MethodRedirect:
		return startRedirect(d, env, conn, f)

	case MethodLink:
		login, err := sources.NextcloudStart(ctx, service(conn), tlsOf(conn))
		if err != nil {
			return Step{}, err
		}
		f.nextcloud = login
		return Step{Link: login.Link, Flow: remember(f)}, nil

	case MethodCode:
		quick, err := sources.JellyfinStart(ctx, service(conn), deviceID(conn.ID, who.UserID), tlsOf(conn))
		if err != nil {
			return Step{}, err
		}
		f.jellyfin = quick
		return Step{Code: quick.Code, Flow: remember(f)}, nil

	case MethodClient:
		client, err := connections.OAuthClientOf(d, conn.ID)
		if err != nil {
			return Step{}, err
		}
		// Fetch one token right away: a wrong client fails here, not later.
		g, _, err := sources.Grant{Kind: sources.GrantClient, TokenURL: tailscaleTokenURL}.Fresh(ctx, client, tlsOf(conn), time.Now().UTC())
		if err != nil {
			return Step{}, err
		}
		return Step{Done: true}, connections.StoreSignIn(d, who, conn.ID, "", &g)
	}
	return Step{}, ErrUnsupported
}

// startRedirect builds the service's authorize URL.
func startRedirect(d *sql.DB, env settings.Settings, conn *model.Connection, f flow) (Step, error) {
	q := url.Values{"response_type": {"code"}, "redirect_uri": {base(env) + CallbackPath}}
	path := "/auth/authorize"

	switch f.service {
	case enums.ServiceHomeAssistant:
		q.Set("client_id", base(env)+"/")
	case enums.ServiceGitea, enums.ServiceSnipeIT:
		client, err := connections.OAuthClientOf(d, conn.ID)
		if err != nil {
			return Step{}, err
		}
		q.Set("client_id", client.ID)
		path = "/oauth/authorize"
		if f.service == enums.ServiceGitea {
			f.verifier = randomToken()
			q.Set("code_challenge", challenge(f.verifier))
			q.Set("code_challenge_method", "S256")
			q.Set("scope", giteaScopes)
			path = "/login/oauth/authorize"
		}
	}
	q.Set("state", remember(f))
	return Step{Redirect: service(conn) + path + "?" + q.Encode()}, nil
}

// Callback finishes a redirect sign-in and returns the page to go back to.
func Callback(ctx context.Context, d *sql.DB, who *access.Principal, env settings.Settings, params url.Values) (string, error) {
	f, err := recall(who, params.Get("state"), true)
	if err != nil {
		return "/", err
	}
	if params.Get("code") == "" {
		return f.back, ErrFlow
	}
	conn, err := connections.Target(d, who, f.connID)
	if err != nil {
		return f.back, err
	}
	code, redirect, now := params.Get("code"), base(env)+CallbackPath, time.Now().UTC()

	switch f.service {
	case enums.ServiceHomeAssistant:
		short, err := sources.HassToken(ctx, service(conn), code, base(env)+"/", tlsOf(conn))
		if err != nil {
			return f.back, err
		}
		long, err := sources.HassLongLived(ctx, service(conn), short, tlsOf(conn), now)
		if err != nil {
			return f.back, err
		}
		return f.back, connections.StoreSignIn(d, who, conn.ID, long, nil)

	case enums.ServiceGitea, enums.ServiceSnipeIT:
		client, err := connections.OAuthClientOf(d, conn.ID)
		if err != nil {
			return f.back, err
		}
		tokenURL := service(conn) + "/oauth/token"
		if f.service == enums.ServiceGitea {
			tokenURL = service(conn) + "/login/oauth/access_token"
		}
		g, err := sources.ExchangeCode(ctx, tokenURL, code, redirect, f.verifier, client, tlsOf(conn), now)
		if err != nil {
			return f.back, err
		}
		return f.back, connections.StoreSignIn(d, who, conn.ID, "", &g)
	}
	return f.back, ErrUnsupported
}

// Poll checks a link or code sign-in; Done once the token is stored. It
// returns the page to go back to.
func Poll(ctx context.Context, d *sql.DB, who *access.Principal, id string) (Step, string, error) {
	f, err := recall(who, id, false)
	if err != nil {
		return Step{}, "/", err
	}
	conn, err := connections.Target(d, who, f.connID)
	if err != nil {
		return Step{}, f.back, err
	}

	var secret string
	var done bool
	switch f.service {
	case enums.ServiceNextcloud:
		secret, done, err = sources.NextcloudPoll(ctx, f.nextcloud, tlsOf(conn))
	case enums.ServiceMediaServer:
		secret, done, err = sources.JellyfinPoll(ctx, service(conn), deviceID(conn.ID, who.UserID), f.jellyfin, tlsOf(conn))
	default:
		return Step{}, f.back, ErrUnsupported
	}
	if err != nil || !done {
		return Step{Flow: id, Code: f.jellyfin.Code, Link: f.nextcloud.Link}, f.back, err
	}
	if _, err := recall(who, id, true); err != nil {
		return Step{}, f.back, err
	}
	return Step{Done: true}, f.back, connections.StoreSignIn(d, who, conn.ID, secret, nil)
}
