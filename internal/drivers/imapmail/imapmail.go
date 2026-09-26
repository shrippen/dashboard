// Package imapmail reads a mailbox over IMAP, read-only: envelopes and
// attachment names of recent mail, and the text part of mail the caller
// picks. Attachments are downloaded only on request (Attachments), for
// one message a user chose to forward.
package imapmail

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"html"
	"io"
	"mime/quotedprintable"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message/charset"

	"andon/internal/drivers/httpclient"
)

const (
	dialTimeout = 10 * time.Second
	cmdTimeout  = 30 * time.Second
	maxScan     = 500
	textMax     = 20_000
)

// Config addresses one mailbox.
type Config struct {
	Host       string
	Port       int
	TLS        bool // implicit TLS (993); false = plain, e.g. a local bridge
	SkipVerify bool
	User, Pass string
	Mailbox    string
}

// Message is one mail's metadata, Text only for picked mail.
type Message struct {
	UID                uint32
	Date               time.Time
	FromName, FromAddr string
	Subject            string
	Attachments        []string
	Text               string
}

// Pick decides from metadata whether a mail's text is needed.
type Pick func(m Message) bool

var tagRe = regexp.MustCompile(`<[^>]*>`)

// Recent returns mail since a date, newest last; Text is filled for up
// to maxBodies picked messages.
func Recent(ctx context.Context, cfg Config, since time.Time, pick Pick, maxBodies int) ([]Message, error) {
	c, err := open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	criteria := imap.NewSearchCriteria()
	criteria.Since = since
	uids, err := c.UidSearch(criteria)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	if len(uids) > maxScan {
		uids = uids[len(uids)-maxScan:]
	}
	if len(uids) == 0 {
		return nil, nil
	}

	msgs, texts, err := envelopes(c, uids)
	if err != nil {
		return nil, err
	}
	picked := 0
	for i := range msgs {
		if picked >= maxBodies || !pick(msgs[i]) {
			continue
		}
		picked++
		part := texts[msgs[i].UID]
		if part == nil {
			continue
		}
		msgs[i].Text = fetchText(c, msgs[i].UID, part)
	}
	return msgs, nil
}

func open(ctx context.Context, cfg Config) (*client.Client, error) {
	if err := httpclient.CheckHost(cfg.Host); err != nil {
		return nil, err
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: dialTimeout}
	var conn net.Conn
	var err error
	if cfg.TLS {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.SkipVerify}}).DialContext(ctx, "tcp", addr) //nolint:gosec // opt-in per connection
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("connect failed")
	}
	c, err := client.New(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("imap greeting failed")
	}
	c.Timeout = cmdTimeout
	if err := c.Login(cfg.User, cfg.Pass); err != nil {
		c.Logout()
		return nil, fmt.Errorf("login failed")
	}
	if _, err := c.Select(cfg.Mailbox, true); err != nil {
		c.Logout()
		return nil, fmt.Errorf("mailbox %q not found", cfg.Mailbox)
	}
	return c, nil
}

// textPart is where a message's readable text lives.
type textPart struct {
	path             []int
	html             bool
	encoding, charst string
}

func envelopes(c *client.Client, uids []uint32) ([]Message, map[uint32]*textPart, error) {
	set := new(imap.SeqSet)
	set.AddNum(uids...)
	ch := make(chan *imap.Message, 16)
	done := make(chan error, 1)
	go func() {
		done <- c.UidFetch(set, []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope, imap.FetchBodyStructure}, ch)
	}()

	var out []Message
	texts := map[uint32]*textPart{}
	for m := range ch {
		if m.Envelope == nil {
			continue
		}
		msg := Message{UID: m.Uid, Date: m.Envelope.Date.UTC(), Subject: m.Envelope.Subject}
		if len(m.Envelope.From) > 0 {
			from := m.Envelope.From[0]
			msg.FromName, msg.FromAddr = from.PersonalName, strings.ToLower(from.Address())
		}
		if m.BodyStructure != nil {
			msg.Attachments, texts[m.Uid] = walk(m.BodyStructure)
		}
		out = append(out, msg)
	}
	if err := <-done; err != nil {
		return nil, nil, fmt.Errorf("fetch: %w", err)
	}
	return out, texts, nil
}

// walk collects attachment names and the best text part (plain over html).
func walk(bs *imap.BodyStructure) ([]string, *textPart) {
	var names []string
	var text *textPart
	bs.Walk(func(path []int, part *imap.BodyStructure) bool {
		if name, _ := part.Filename(); name != "" {
			names = append(names, name)
			return true
		}
		if !strings.EqualFold(part.MIMEType, "text") {
			return true
		}
		html := strings.EqualFold(part.MIMESubType, "html")
		if text == nil || (text.html && !html) {
			p := append([]int(nil), path...)
			if len(p) == 0 {
				p = []int{1} // single-part message
			}
			text = &textPart{path: p, html: html, encoding: strings.ToLower(part.Encoding), charst: part.Params["charset"]}
		}
		return true
	})
	return names, text
}

func fetchText(c *client.Client, uid uint32, part *textPart) string {
	section := &imap.BodySectionName{BodyPartName: imap.BodyPartName{Path: part.path}, Peek: true, Partial: []int{0, textMax}}
	set := new(imap.SeqSet)
	set.AddNum(uid)
	ch := make(chan *imap.Message, 1)
	if err := c.UidFetch(set, []imap.FetchItem{section.FetchItem()}, ch); err != nil {
		return ""
	}
	m := <-ch
	if m == nil {
		return ""
	}
	body := m.GetBody(section)
	if body == nil {
		return ""
	}
	return decode(body, part)
}

func decode(r io.Reader, part *textPart) string {
	switch part.encoding {
	case "quoted-printable":
		r = quotedprintable.NewReader(r)
	case "base64":
		r = base64.NewDecoder(base64.StdEncoding, r)
	}
	if part.charst != "" {
		if cr, err := charset.Reader(part.charst, r); err == nil {
			r = cr
		}
	}
	raw, _ := io.ReadAll(io.LimitReader(r, textMax))
	text := string(raw)
	if part.html {
		text = html.UnescapeString(tagRe.ReplaceAllString(text, " "))
	}
	return strings.Join(strings.Fields(text), " ")
}

// Attachment is one downloaded file of a message.
type Attachment struct {
	Name, MediaType string
	Content         []byte
}

const attachmentMax = 20 << 20

// Attachments downloads the files of one message (by UID), leaving it
// unread (BODY.PEEK).
func Attachments(ctx context.Context, cfg Config, uid uint32) ([]Attachment, error) {
	c, err := open(ctx, cfg)
	if err != nil {
		return nil, err
	}
	defer c.Logout()

	set := new(imap.SeqSet)
	set.AddNum(uid)
	ch := make(chan *imap.Message, 1)
	if err := c.UidFetch(set, []imap.FetchItem{imap.FetchBodyStructure}, ch); err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	m := <-ch
	if m == nil || m.BodyStructure == nil {
		return nil, fmt.Errorf("message %d not found", uid)
	}

	type filePart struct {
		path     []int
		name     string
		media    string
		encoding string
	}
	var parts []filePart
	m.BodyStructure.Walk(func(path []int, part *imap.BodyStructure) bool {
		if name, _ := part.Filename(); name != "" {
			p := append([]int(nil), path...)
			if len(p) == 0 {
				p = []int{1}
			}
			parts = append(parts, filePart{p, name, strings.ToLower(part.MIMEType + "/" + part.MIMESubType), strings.ToLower(part.Encoding)})
		}
		return true
	})

	var out []Attachment
	for _, p := range parts {
		section := &imap.BodySectionName{BodyPartName: imap.BodyPartName{Path: p.path}, Peek: true}
		ch := make(chan *imap.Message, 1)
		if err := c.UidFetch(set, []imap.FetchItem{section.FetchItem()}, ch); err != nil {
			return nil, fmt.Errorf("fetch part: %w", err)
		}
		msg := <-ch
		if msg == nil {
			continue
		}
		body := msg.GetBody(section)
		if body == nil {
			continue
		}
		var r io.Reader = body
		switch p.encoding {
		case "base64":
			r = base64.NewDecoder(base64.StdEncoding, r)
		case "quoted-printable":
			r = quotedprintable.NewReader(r)
		}
		content, err := io.ReadAll(io.LimitReader(r, attachmentMax))
		if err != nil {
			return nil, fmt.Errorf("decode %s: %w", p.name, err)
		}
		out = append(out, Attachment{Name: p.name, MediaType: p.media, Content: content})
	}
	return out, nil
}
