package sources

// Sign-in flows that end in a lasting token, so the source keeps using a
// plain secret:
//
//	Home Assistant  code → short token → WebSocket: long-lived token
//	Nextcloud       Login Flow v2: login link + poll → "user:app-password"
//	Jellyfin        Quick Connect: code shown, user approves → access token

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"andon/internal/drivers/httpclient"
)

const (
	// appName is how Andon introduces itself to the service.
	appName = "Andon"
	// hassTokenDays is the lifespan of Home Assistant's long-lived token.
	hassTokenDays = 3650
	// socketWait bounds the WebSocket exchange with Home Assistant.
	socketWait = 15 * time.Second
	// socketMaxRead caps one WebSocket message.
	socketMaxRead = 1 << 16
)

// callJSON sends one request and decodes a JSON answer into out. It
// returns the status, so callers can tell "not yet" (e.g. 404) from errors.
func callJSON(ctx context.Context, method, rawURL string, headers map[string]string, body []byte, tls httpclient.TLS, out any) (int, error) {
	resp, err := httpclient.Request(ctx, method, rawURL, httpclient.Options{
		Headers: headers, Body: body, SkipVerify: tls == httpclient.TLSSkip,
	})
	if err != nil {
		return 0, newSourceError("%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, httpclient.MaxBody)).Decode(out); err != nil {
		return resp.StatusCode, newSourceError("invalid JSON")
	}
	return resp.StatusCode, nil
}

func joinPath(base, path string) string {
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// ── Home Assistant ──

// HassToken trades an authorization code for a short-lived access token.
// clientID is Andon's own address (IndieAuth, no registration).
func HassToken(ctx context.Context, baseURL, code, clientID string, tls httpclient.TLS) (string, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}}
	var t tokenAnswer
	status, err := callJSON(ctx, http.MethodPost, joinPath(baseURL, "auth/token"),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, []byte(form.Encode()), tls, &t)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK || t.Access == "" {
		return "", newSourceError("token: HTTP %d", status)
	}
	return t.Access, nil
}

// HassLongLived creates a long-lived access token over the WebSocket API,
// authenticated with a short-lived one. The name carries the time: Home
// Assistant refuses a second token of the same name.
func HassLongLived(ctx context.Context, baseURL, access string, tls httpclient.TLS, now time.Time) (string, error) {
	u, err := url.Parse(joinPath(baseURL, "api/websocket"))
	if err != nil {
		return "", newSourceError("bad url")
	}
	u.Scheme = map[string]string{"https": "wss", "http": "ws"}[u.Scheme]

	ctx, cancel := context.WithTimeout(ctx, socketWait)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPClient: httpclient.ClientTLS(socketWait, tls)})
	if err != nil {
		return "", newSourceError("websocket: connect failed")
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(socketMaxRead)

	var msg struct {
		Type    string `json:"type"`
		Success bool   `json:"success"`
		Result  string `json:"result"`
	}
	read := func() error {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return newSourceError("websocket: read failed")
		}
		return json.Unmarshal(raw, &msg)
	}
	write := func(v any) error {
		raw, _ := json.Marshal(v)
		return conn.Write(ctx, websocket.MessageText, raw)
	}

	// auth_required → auth → auth_ok → token request → result
	if err := read(); err != nil {
		return "", err
	}
	if err := write(map[string]any{"type": "auth", "access_token": access}); err != nil {
		return "", newSourceError("websocket: write failed")
	}
	if err := read(); err != nil || msg.Type != "auth_ok" {
		return "", newSourceError("websocket: login refused")
	}
	name := appName + " " + now.Format("2006-01-02 15:04")
	if err := write(map[string]any{"id": 1, "type": "auth/long_lived_access_token", "client_name": name, "lifespan": hassTokenDays}); err != nil {
		return "", newSourceError("websocket: write failed")
	}
	if err := read(); err != nil || !msg.Success || msg.Result == "" {
		return "", newSourceError("websocket: no token")
	}
	return msg.Result, nil
}

// ── Nextcloud Login Flow v2 ──

// NextcloudLogin is a started login: the user opens Link, Andon polls.
type NextcloudLogin struct {
	Link     string
	PollURL  string
	PollCode string
}

// NextcloudStart begins Login Flow v2.
func NextcloudStart(ctx context.Context, baseURL string, tls httpclient.TLS) (NextcloudLogin, error) {
	var answer struct {
		Poll struct {
			Token    string `json:"token"`
			Endpoint string `json:"endpoint"`
		} `json:"poll"`
		Login string `json:"login"`
	}
	status, err := callJSON(ctx, http.MethodPost, joinPath(baseURL, "index.php/login/v2"),
		map[string]string{"User-Agent": appName, "Accept": "application/json"}, nil, tls, &answer)
	if err != nil {
		return NextcloudLogin{}, err
	}
	if status != http.StatusOK || answer.Login == "" {
		return NextcloudLogin{}, newSourceError("login flow: HTTP %d", status)
	}
	return NextcloudLogin{Link: answer.Login, PollURL: answer.Poll.Endpoint, PollCode: answer.Poll.Token}, nil
}

// NextcloudPoll asks whether the user finished the login. done=false
// means "not yet"; on success the secret is "user:app-password".
func NextcloudPoll(ctx context.Context, login NextcloudLogin, tls httpclient.TLS) (secret string, done bool, err error) {
	var answer struct {
		LoginName   string `json:"loginName"`
		AppPassword string `json:"appPassword"`
	}
	form := url.Values{"token": {login.PollCode}}
	status, err := callJSON(ctx, http.MethodPost, login.PollURL,
		map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json"},
		[]byte(form.Encode()), tls, &answer)
	if err != nil {
		return "", false, err
	}
	if status == http.StatusNotFound {
		return "", false, nil
	}
	if status != http.StatusOK || answer.AppPassword == "" {
		return "", false, newSourceError("login flow: HTTP %d", status)
	}
	return answer.LoginName + ":" + answer.AppPassword, true, nil
}

// ── Jellyfin Quick Connect ──

// jellyfinAuth identifies Andon; one device ID per connection and user, as
// Jellyfin replaces an older token of the same device.
func jellyfinAuth(deviceID string) map[string]string {
	value := fmt.Sprintf(`MediaBrowser Client="%s", Device="%s", DeviceId="%s", Version="1"`, appName, appName, deviceID)
	return map[string]string{"Authorization": value, "X-Emby-Authorization": value,
		"Content-Type": "application/json", "Accept": "application/json"}
}

// JellyfinQuick is a started Quick Connect: the user enters Code in
// Jellyfin, Andon polls with Secret.
type JellyfinQuick struct {
	Code   string
	Secret string
}

// JellyfinStart begins Quick Connect.
func JellyfinStart(ctx context.Context, baseURL, deviceID string, tls httpclient.TLS) (JellyfinQuick, error) {
	var answer struct {
		Code   string `json:"Code"`
		Secret string `json:"Secret"`
	}
	status, err := callJSON(ctx, http.MethodPost, joinPath(baseURL, "QuickConnect/Initiate"), jellyfinAuth(deviceID), nil, tls, &answer)
	if err != nil {
		return JellyfinQuick{}, err
	}
	if status != http.StatusOK || answer.Code == "" {
		return JellyfinQuick{}, newSourceError("quick connect: HTTP %d (in Jellyfin enabled?)", status)
	}
	return JellyfinQuick{Code: answer.Code, Secret: answer.Secret}, nil
}

// JellyfinPoll asks whether the code was approved and then signs in.
func JellyfinPoll(ctx context.Context, baseURL, deviceID string, quick JellyfinQuick, tls httpclient.TLS) (token string, done bool, err error) {
	var state struct {
		Authenticated bool `json:"Authenticated"`
	}
	status, err := callJSON(ctx, http.MethodGet, joinPath(baseURL, "QuickConnect/Connect?secret="+url.QueryEscape(quick.Secret)),
		jellyfinAuth(deviceID), nil, tls, &state)
	if err != nil {
		return "", false, err
	}
	if status != http.StatusOK {
		return "", false, newSourceError("quick connect: HTTP %d", status)
	}
	if !state.Authenticated {
		return "", false, nil
	}

	var login struct {
		AccessToken string `json:"AccessToken"`
	}
	body, _ := json.Marshal(map[string]string{"Secret": quick.Secret})
	status, err = callJSON(ctx, http.MethodPost, joinPath(baseURL, "Users/AuthenticateWithQuickConnect"),
		jellyfinAuth(deviceID), body, tls, &login)
	if err != nil {
		return "", false, err
	}
	if status != http.StatusOK || login.AccessToken == "" {
		return "", false, newSourceError("quick connect: HTTP %d", status)
	}
	return login.AccessToken, true, nil
}
