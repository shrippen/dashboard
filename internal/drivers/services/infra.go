package services

// Infrastructure clients:
//
//	TrueNASApi   JSON-RPC 2.0 over WebSocket (/api/current, 25.04+),
//	             REST v2.0 as fallback for older systems
//	KomodoApi    POST /read/<Request>, X-Api-Key + X-Api-Secret
//	PangolinApi  integration API /v1, bearer API key
//	AuthentikApi /api/v3, bearer token

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"

	"dashboard/internal/drivers/httpclient"
)

const (
	rpcTimeout = 20 * time.Second
	rpcMaxRead = 8 << 20
)

// ── TrueNAS ──

type TrueNASApi struct {
	URL    string
	Key    string
	Verify bool
}

// restPaths maps JSON-RPC methods to the REST v2.0 fallback.
var restPaths = map[string]string{
	"system.info": "system/info", "pool.query": "pool", "alert.list": "alert/list", "app.query": "app",
}

// Session runs several calls on one connection.
type TrueNASSession struct {
	api  TrueNASApi
	conn *websocket.Conn // nil: REST fallback
	id   int
}

// Open logs in over WebSocket, or falls back to REST when the server has
// no JSON-RPC endpoint.
func (a TrueNASApi) Open(ctx context.Context) (*TrueNASSession, error) {
	u, err := url.Parse(strings.TrimRight(a.URL, "/") + "/api/current")
	if err != nil {
		return nil, ApiError{"bad url"}
	}
	u.Scheme = map[string]string{"https": "wss", "http": "ws"}[u.Scheme]
	dialCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	conn, resp, err := websocket.Dial(dialCtx, u.String(), &websocket.DialOptions{HTTPClient: httpclient.ClientTLS(rpcTimeout, !a.Verify)})
	if err != nil {
		if resp != nil && resp.StatusCode == notFound {
			return &TrueNASSession{api: a}, nil
		}
		return nil, ApiError{"connect failed"}
	}
	conn.SetReadLimit(rpcMaxRead)
	s := &TrueNASSession{api: a, conn: conn}
	var ok bool
	if err := s.rpc(ctx, "auth.login_with_api_key", []any{a.Key}, &ok); err != nil || !ok {
		s.Close()
		return nil, ApiError{"login failed"}
	}
	return s, nil
}

// Close ends the session.
func (s *TrueNASSession) Close() {
	if s.conn != nil {
		s.conn.Close(websocket.StatusNormalClosure, "")
	}
}

// Call runs one method without parameters and decodes its result.
func (s *TrueNASSession) Call(ctx context.Context, method string) (any, error) {
	var out any
	if s.conn != nil {
		err := s.rpc(ctx, method, []any{}, &out)
		return out, err
	}
	path, ok := restPaths[method]
	if !ok {
		return nil, ApiError{"unsupported: " + method}
	}
	return fetchJSON(ctx, joinURL(s.api.URL, "api/v2.0/"+path), map[string]string{"Authorization": "Bearer " + s.api.Key}, nil, !s.api.Verify)
}

func (s *TrueNASSession) rpc(ctx context.Context, method string, params []any, out any) error {
	s.id++
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": s.id, "method": method, "params": params})
	callCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	if err := s.conn.Write(callCtx, websocket.MessageText, req); err != nil {
		return ApiError{"write failed"}
	}
	// Skip notifications until the answer with our id arrives.
	for {
		_, raw, err := s.conn.Read(callCtx)
		if err != nil {
			return ApiError{"read failed"}
		}
		var msg struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &msg) != nil || msg.ID == nil || *msg.ID != s.id {
			continue
		}
		if msg.Error != nil {
			return ApiError{method + ": " + msg.Error.Message}
		}
		if json.Unmarshal(msg.Result, out) != nil {
			return ApiError{method + ": invalid result"}
		}
		return nil
	}
}

// ── Komodo ──

// KomodoApi: Secret is "key:secret".
type KomodoApi struct {
	URL    string
	Secret string
	Verify bool
}

// Read runs one read request, e.g. ("GetStacksSummary", {}).
func (a KomodoApi) Read(ctx context.Context, request string, params map[string]any) (any, error) {
	key, secret, _ := strings.Cut(a.Secret, ":")
	if params == nil {
		params = map[string]any{}
	}
	return postJSON(ctx, joinURL(a.URL, "read/"+request), map[string]string{"X-Api-Key": key, "X-Api-Secret": secret}, params, !a.Verify)
}

// ── Pangolin ──

type PangolinApi struct {
	URL    string // integration API base, e.g. https://api.example.com/v1
	Key    string
	Verify bool
}

// Get performs one GET against <base>/<path> and returns its "data".
func (a PangolinApi) Get(ctx context.Context, path string, params url.Values) (any, error) {
	body, err := fetchJSON(ctx, joinURL(a.URL, path), map[string]string{"Authorization": "Bearer " + a.Key, "Accept": "application/json"}, params, !a.Verify)
	if err != nil {
		return nil, err
	}
	return asMap(body)["data"], nil
}

// ── authentik ──

type AuthentikApi struct {
	URL    string
	Token  string
	Verify bool
}

// Get performs one GET against /api/v3/<path>.
func (a AuthentikApi) Get(ctx context.Context, path string, params url.Values) (any, error) {
	return fetchJSON(ctx, joinURL(a.URL, "api/v3/"+path), map[string]string{"Authorization": "Bearer " + a.Token, "Accept": "application/json"}, params, !a.Verify)
}
