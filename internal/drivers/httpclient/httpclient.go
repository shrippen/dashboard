// Package httpclient is the shared HTTP client with timeouts and an egress
// guard. Every outbound request of the app passes the guard, so the admin
// can restrict reachable networks and invited users cannot scan the
// internal network through status checks or feeds.
package httpclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	ConnectTimeout = 5 * time.Second
	ReadTimeout    = 15 * time.Second
	UserAgent      = "dashboard/0.1 (+https://github.com/shrippen/dashboard)"
	MaxBody        = 5 * 1024 * 1024
)

// HttpError is a transport or status failure with a short, secret-free
// message (never wraps a raw error that might contain a token or URL).
type HttpError struct{ msg string }

func (e HttpError) Error() string { return e.msg }

// EgressDenied means the guard rejected the target host/addresses.
type EgressDenied struct{ Host string }

func (e EgressDenied) Error() string { return "egress denied: " + e.Host }

// Guard decides whether a host (already resolved to addrs) may be reached.
// A nil guard (the default) allows everything.
type Guard func(host string, addrs []net.IP) bool

var guard Guard

// SetGuard installs the process-wide egress guard, or nil to allow all.
func SetGuard(g Guard) { guard = g }

func checkGuard(rawURL string) error {
	if guard == nil {
		return nil
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return HttpError{"bad url"}
	}
	addrs, err := net.LookupIP(u.Hostname())
	if err != nil {
		return HttpError{"dns: " + u.Hostname()}
	}
	if !guard(u.Hostname(), addrs) {
		return EgressDenied{u.Hostname()}
	}
	return nil
}

// Options configure one Request call.
type Options struct {
	Headers map[string]string
	Params  url.Values
	// Body is the raw request body (e.g. a POST's JSON payload). nil for
	// none.
	Body []byte
	// SkipVerify disables TLS certificate verification. Defaults to false
	// (verified) so a zero-value Options is always safe; set true only for
	// a connection the user explicitly marked as self-signed.
	SkipVerify bool
	Timeout    time.Duration
	// NoRedirect returns a redirect as is, e.g. to read a login cookie.
	NoRedirect bool
}

// Request performs one guarded HTTP call and returns the raw response. The
// caller must close resp.Body.
func Request(ctx context.Context, method, rawURL string, opts Options) (*http.Response, error) {
	if err := checkGuard(rawURL); err != nil {
		return nil, err
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, HttpError{"bad url"}
	}
	if opts.Params != nil {
		u.RawQuery = opts.Params.Encode()
	}

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = ReadTimeout
	}
	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: opts.SkipVerify}, //nolint:gosec // opt-in per connection
			TLSHandshakeTimeout: ConnectTimeout,
		},
	}
	if opts.NoRedirect {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}

	var body io.Reader
	if opts.Body != nil {
		body = bytes.NewReader(opts.Body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, HttpError{"bad request"}
	}
	req.Header.Set("User-Agent", UserAgent)
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, HttpError{"timeout"}
		}
		return nil, HttpError{"request failed"}
	}
	return resp, nil
}

// GetJSON performs a guarded GET and decodes a JSON body, returning the
// response headers too (callers need X-Total-Pages etc.).
func GetJSON(ctx context.Context, rawURL string, opts Options) (any, http.Header, error) {
	resp, err := Request(ctx, http.MethodGet, rawURL, opts)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return nil, nil, HttpError{"read failed"}
	}
	if len(body) > MaxBody {
		return nil, nil, HttpError{"response too large"}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, resp.Header, HttpError{fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}

	var parsed any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, resp.Header, HttpError{"invalid JSON"}
	}
	return parsed, resp.Header, nil
}

// GetText performs a guarded GET and returns the body as text (e.g.
// Prometheus metrics).
func GetText(ctx context.Context, rawURL string, opts Options) (string, error) {
	resp, err := Request(ctx, http.MethodGet, rawURL, opts)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBody+1))
	if err != nil {
		return "", HttpError{"read failed"}
	}
	if len(body) > MaxBody {
		return "", HttpError{"response too large"}
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return "", HttpError{fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return string(body), nil
}

// PeerCert connects to host:port over TLS and returns the leaf
// certificate without verifying it, so expired ones can be reported too.
func PeerCert(ctx context.Context, hostPort string) (*x509.Certificate, error) {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		return nil, HttpError{"bad host"}
	}
	if err := checkGuard("https://" + hostPort); err != nil {
		return nil, err
	}

	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{Timeout: ConnectTimeout},
		Config:    &tls.Config{ServerName: host, InsecureSkipVerify: true}, //nolint:gosec // only reads the certificate
	}
	conn, err := dialer.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return nil, HttpError{"connect failed"}
	}
	defer conn.Close()

	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, HttpError{"no certificate"}
	}
	return certs[0], nil
}

// guardedTransport applies the egress guard to clients this package does
// not build itself (e.g. an SDK's).
type guardedTransport struct{ next http.RoundTripper }

func (g guardedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := checkGuard(req.URL.String()); err != nil {
		return nil, err
	}
	return g.next.RoundTrip(req)
}

// Client returns an http.Client whose requests pass the egress guard.
func Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: guardedTransport{http.DefaultTransport}}
}

// ClientTLS is Client with TLS verification switchable per connection
// (self-signed homelab certificates).
func ClientTLS(timeout time.Duration, skipVerify bool) *http.Client {
	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = &tls.Config{InsecureSkipVerify: skipVerify} //nolint:gosec // opt-in per connection
	return &http.Client{Timeout: timeout, Transport: guardedTransport{base}}
}

// CheckHost applies the egress guard to a non-HTTP connection (IMAP).
func CheckHost(host string) error {
	return checkGuard("https://" + host)
}
