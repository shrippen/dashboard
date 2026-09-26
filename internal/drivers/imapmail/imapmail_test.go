package imapmail

import (
	"strings"
	"testing"

	"github.com/emersion/go-imap"
)

// walk lists attachments and prefers the plain text part over HTML.
func TestWalkPrefersPlainText(t *testing.T) {
	bs := &imap.BodyStructure{MIMEType: "multipart", MIMESubType: "mixed", Parts: []*imap.BodyStructure{
		{MIMEType: "multipart", MIMESubType: "alternative", Parts: []*imap.BodyStructure{
			{MIMEType: "text", MIMESubType: "html", Encoding: "base64"},
			{MIMEType: "text", MIMESubType: "plain", Encoding: "quoted-printable", Params: map[string]string{"charset": "utf-8"}},
		}},
		{MIMEType: "application", MIMESubType: "pdf", Disposition: "attachment", DispositionParams: map[string]string{"filename": "Rechnung.pdf"}},
	}}

	names, text := walk(bs)
	if len(names) != 1 || names[0] != "Rechnung.pdf" {
		t.Fatalf("names: %v", names)
	}
	if text == nil || text.html || text.encoding != "quoted-printable" || len(text.path) != 2 || text.path[1] != 2 {
		t.Fatalf("text part: %+v", text)
	}
}

// decode undoes transfer encoding and strips HTML to plain words.
func TestDecodeHTML(t *testing.T) {
	got := decode(strings.NewReader("<p>Betrag:=20<b>12,50&nbsp;=E2=82=AC</b></p>"),
		&textPart{html: true, encoding: "quoted-printable", charst: "utf-8"})
	if got != "Betrag: 12,50 €" {
		t.Fatalf("decoded %q", got)
	}
}
