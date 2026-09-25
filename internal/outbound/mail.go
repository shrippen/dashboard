package outbound

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"sync"
	"time"

	"dashboard/internal/drivers/smtp"
	"dashboard/internal/settings"
)

// Mail is one outgoing message: plain text plus optional HTML alternative.
type Mail struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

var (
	outboxMu sync.Mutex
	outbox   []Mail
)

// MailConfigured reports whether mail can be sent (SMTP set, or test mode).
func MailConfigured(cfg settings.Settings) bool {
	return cfg.SMTPURL != "" || cfg.Testing
}

// Deliver sends m in the background so requests never wait for SMTP.
// In test mode messages land in the outbox instead (see TakeOutbox).
func Deliver(cfg settings.Settings, m Mail) {
	if cfg.Testing {
		outboxMu.Lock()
		outbox = append(outbox, m)
		outboxMu.Unlock()
		return
	}
	if cfg.SMTPURL == "" {
		slog.Info("mail skipped (no SMTP_URL)", "subject", m.Subject)
		return
	}
	go sendNow(cfg, m)
}

// SendMail delivers m synchronously and returns the SMTP error, if any.
func SendMail(cfg settings.Settings, m Mail) error {
	target, err := smtp.Parse(cfg.SMTPURL)
	if err != nil {
		return err
	}
	from, err := mail.ParseAddress(cfg.SMTPFrom)
	if err != nil {
		return err
	}
	body, err := compose(cfg.SMTPFrom, m)
	if err != nil {
		return err
	}
	return smtp.Send(target, cfg.SMTPPassword, from.Address, []string{m.To}, body)
}

// TakeOutbox returns and clears the test-mode outbox.
func TakeOutbox() []Mail {
	outboxMu.Lock()
	defer outboxMu.Unlock()
	taken := outbox
	outbox = nil
	return taken
}

func sendNow(cfg settings.Settings, m Mail) {
	// SMTP errors must not break the app; they are logged.
	if err := SendMail(cfg, m); err != nil {
		slog.Error("mail failed", "to", m.To, "err", err)
	}
}

// compose builds a multipart/alternative RFC 5322 message.
func compose(from string, m Mail) ([]byte, error) {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	header := func(k, v string) { fmt.Fprintf(&buf, "%s: %s\r\n", k, v) }
	header("From", from)
	header("To", m.To)
	header("Subject", mime.QEncoding.Encode("utf-8", m.Subject))
	header("Date", time.Now().Format(time.RFC1123Z))
	header("Message-ID", "<"+randomID()+"@dashboard>")
	header("MIME-Version", "1.0")
	header("Content-Type", "multipart/alternative; boundary="+writer.Boundary())
	buf.WriteString("\r\n")

	if err := addPart(writer, "text/plain", m.Text); err != nil {
		return nil, err
	}
	if m.HTML != "" {
		if err := addPart(writer, "text/html", m.HTML); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func addPart(w *multipart.Writer, contentType, body string) error {
	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {contentType + "; charset=utf-8"},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	qp := quotedprintable.NewWriter(part)
	if _, err := qp.Write([]byte(body)); err != nil {
		return err
	}
	return qp.Close()
}

func randomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
