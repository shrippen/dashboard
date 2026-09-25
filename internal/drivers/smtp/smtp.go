// Package smtp is the raw SMTP driver.
//
// SMTP_URL forms:
//
//	smtp://user@host:587?starttls=true
//	smtps://user@host:465
//
// The password comes separately (Docker secret smtp_password).
package smtp

import (
	"crypto/tls"
	"net"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	smtpPort    = 25
	smtpsPort   = 465
	schemeSSL   = "smtps"
	dialTimeout = 20 * time.Second
)

// Target is a parsed SMTP_URL.
type Target struct {
	Host     string
	Port     int
	User     string
	UseSSL   bool
	StartTLS bool
}

// Parse reads an SMTP_URL.
func Parse(raw string) (Target, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Target{}, err
	}

	t := Target{Host: u.Hostname(), UseSSL: u.Scheme == schemeSSL}
	if t.Host == "" {
		t.Host = "localhost"
	}

	t.Port = smtpPort
	if t.UseSSL {
		t.Port = smtpsPort
	}
	if p := u.Port(); p != "" {
		if t.Port, err = strconv.Atoi(p); err != nil {
			return Target{}, err
		}
	}

	if u.User != nil {
		t.User = u.User.Username()
	}
	t.StartTLS = strings.EqualFold(u.Query().Get("starttls"), "true")
	return t, nil
}

// Send delivers one prepared RFC 5322 message.
func Send(t Target, password, from string, to []string, message []byte) error {
	addr := net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
	tlsCfg := &tls.Config{ServerName: t.Host, MinVersion: tls.VersionTLS12}

	conn, err := dial(t, addr, tlsCfg)
	if err != nil {
		return err
	}

	client, err := smtp.NewClient(conn, t.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer client.Close()

	if t.StartTLS {
		if err := client.StartTLS(tlsCfg); err != nil {
			return err
		}
	}
	if t.User != "" && password != "" {
		if err := client.Auth(smtp.PlainAuth("", t.User, password, t.Host)); err != nil {
			return err
		}
	}

	if err := client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil {
			return err
		}
	}

	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(message); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func dial(t Target, addr string, tlsCfg *tls.Config) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: dialTimeout}
	if t.UseSSL {
		return tls.DialWithDialer(dialer, "tcp", addr, tlsCfg)
	}
	return dialer.Dial("tcp", addr)
}
