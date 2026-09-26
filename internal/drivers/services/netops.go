package services

// Network and download services:
//
//	PiholeApi    v6: POST /api/auth → sid, X-FTL-SID, DELETE /api/auth
//	             v5: /admin/api.php?summaryRaw&auth=<token>
//	AdGuardApi   basic auth, /control/*
//	NextcloudApi serverinfo OCS: NC-Token or user:app-password
//	SabnzbdApi   /api?mode=…&apikey=
//	GluetunApi   control server /v1/*, optional X-API-Key
//	DNSBL        reverse-octet lookup in a blocklist zone

import (
	"context"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"andon/internal/drivers/httpclient"
)

// ── Pi-hole ──

type PiholeApi struct {
	URL      string
	Password string // v6 app password, or the v5 API token
	Verify   bool
}

// PiholeSession is a logged-in v6 session; sid "" means v5.
type PiholeSession struct {
	api PiholeApi
	sid string
}

// Open logs in to Pi-hole v6; on a v5 system (no /api/auth) it returns a
// session that uses the legacy API instead.
func (a PiholeApi) Open(ctx context.Context) (*PiholeSession, error) {
	body, err := postJSON(ctx, joinURL(a.URL, "api/auth"), nil, map[string]string{"password": a.Password}, httpclient.TLSOf(a.Verify))
	if err != nil {
		if strings.Contains(err.Error(), "HTTP 404") {
			return &PiholeSession{api: a}, nil
		}
		return nil, err
	}
	session := asMap(asMap(body)["session"])
	sid, _ := session["sid"].(string)
	if valid, _ := session["valid"].(bool); !valid || sid == "" {
		return nil, ApiError{"login failed"}
	}
	return &PiholeSession{api: a, sid: sid}, nil
}

// Legacy reports a v5 session.
func (s *PiholeSession) Legacy() bool { return s.sid == "" }

// Get reads one v6 endpoint, e.g. "stats/summary".
func (s *PiholeSession) Get(ctx context.Context, path string) (any, error) {
	return fetchJSON(ctx, joinURL(s.api.URL, "api/"+path), map[string]string{"X-FTL-SID": s.sid}, nil, httpclient.TLSOf(s.api.Verify))
}

// Summary reads the v5 summary.
func (s *PiholeSession) Summary(ctx context.Context) (any, error) {
	query := url.Values{"summaryRaw": {""}, "auth": {s.api.Password}}
	return fetchJSON(ctx, joinURL(s.api.URL, "admin/api.php"), nil, query, httpclient.TLSOf(s.api.Verify))
}

// Close ends a v6 session: Pi-hole allows only a few at once.
func (s *PiholeSession) Close(ctx context.Context) {
	if s.sid == "" {
		return
	}
	resp, err := httpclient.Request(ctx, http.MethodDelete, joinURL(s.api.URL, "api/auth"),
		httpclient.Options{Headers: map[string]string{"X-FTL-SID": s.sid}, SkipVerify: !s.api.Verify})
	if err == nil {
		resp.Body.Close()
	}
}

// ── AdGuard Home ──

type AdGuardApi struct {
	URL    string
	Secret string // "user:password"
	Verify bool
}

// Get reads /control/<path>.
func (a AdGuardApi) Get(ctx context.Context, path string) (any, error) {
	return fetchJSON(ctx, joinURL(a.URL, "control/"+path), basicAuth(a.Secret), nil, httpclient.TLSOf(a.Verify))
}

func basicAuth(secret string) map[string]string {
	return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(secret)), "Accept": "application/json"}
}

// ── Nextcloud ──

type NextcloudApi struct {
	URL    string
	Secret string // serverinfo token, or "user:app-password"
	Verify bool
}

// ServerInfo returns the serverinfo app's ocs.data.
func (a NextcloudApi) ServerInfo(ctx context.Context) (any, error) {
	headers := map[string]string{"OCS-APIRequest": "true", "Accept": "application/json"}
	if strings.Contains(a.Secret, ":") {
		headers = basicAuth(a.Secret)
		headers["OCS-APIRequest"] = "true"
	} else {
		headers["NC-Token"] = a.Secret
	}
	query := url.Values{"format": {"json"}, "skipApps": {"false"}, "skipUpdate": {"false"}}
	body, err := fetchJSON(ctx, joinURL(a.URL, "ocs/v2.php/apps/serverinfo/api/v1/info"), headers, query, httpclient.TLSOf(a.Verify))
	if err != nil {
		return nil, err
	}
	return asMap(asMap(body)["ocs"])["data"], nil
}

// ── Sabnzbd ──

type SabnzbdApi struct {
	URL    string
	Key    string
	Verify bool
}

// Mode calls /api?mode=<mode>&output=json.
func (a SabnzbdApi) Mode(ctx context.Context, mode string, params url.Values) (any, error) {
	query := cloneValues(params)
	query.Set("mode", mode)
	query.Set("output", "json")
	query.Set("apikey", a.Key)
	return fetchJSON(ctx, joinURL(a.URL, "api"), nil, query, httpclient.TLSOf(a.Verify))
}

// ── Gluetun ──

type GluetunApi struct {
	URL    string
	Key    string // "" when the control server has no auth
	Verify bool
}

// Get reads /v1/<path>.
func (a GluetunApi) Get(ctx context.Context, path string) (any, error) {
	var headers map[string]string
	if a.Key != "" {
		headers = map[string]string{"X-API-Key": a.Key}
	}
	return fetchJSON(ctx, joinURL(a.URL, "v1/"+path), headers, nil, httpclient.TLSOf(a.Verify))
}

// ── DNSBL ──

// ErrDNSBLRefused: the list refuses the resolver (Spamhaus answers
// 127.255.255.x to public resolvers).
var ErrDNSBLRefused = errors.New("dnsbl refused")

const dnsblRefused = "127.255.255."

// DNSBL looks an IPv4 address up in one blocklist zone.
//
//	1.2.3.4 in zen.spamhaus.org → 4.3.2.1.zen.spamhaus.org → 127.0.0.2 = listed
func DNSBL(ctx context.Context, ip net.IP, zone string) (bool, string, error) {
	name, ok := dnsblName(ip, zone)
	if !ok {
		return false, "", ApiError{"IPv4 only"}
	}
	addrs, err := net.DefaultResolver.LookupHost(ctx, name)
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		return false, "", nil
	}
	if err != nil {
		return false, "", ApiError{err.Error()}
	}
	for _, a := range addrs {
		if strings.HasPrefix(a, dnsblRefused) {
			return false, a, ErrDNSBLRefused
		}
	}
	return len(addrs) > 0, strings.Join(addrs, ","), nil
}

// dnsblName reverses the octets in front of the zone.
func dnsblName(ip net.IP, zone string) (string, bool) {
	v4 := ip.To4()
	if v4 == nil {
		return "", false
	}
	return strings.Join([]string{itoa(v4[3]), itoa(v4[2]), itoa(v4[1]), itoa(v4[0]), zone}, "."), true
}

func itoa(b byte) string { return strconv.Itoa(int(b)) }

// ResolveIPv4 returns a host's IPv4 addresses.
func ResolveIPv4(ctx context.Context, host string) ([]net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil, ApiError{err.Error()}
	}
	return addrs, nil
}
