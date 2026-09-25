package sources_test

import (
	"context"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/server"

	"dashboard/internal/sources"
)

func TestInvoiceAmount(t *testing.T) {
	cases := map[string]float64{
		"Rechnungsbetrag: 1.234,56 € zzgl. Versand 4,90 €": 1234.56,
		"Total due: EUR 1,234.56":                          1234.56,
		"Artikel 12,00 € und 30,00 €":                      30,
		"keine Beträge":                                    0,
	}
	for text, want := range cases {
		if got := sources.InvoiceAmount(text); got != want {
			t.Errorf("%q = %v, want %v", text, got, want)
		}
	}
}

const invoiceMail = "From: Hetzner Online GmbH <billing@hetzner.com>\r\n" +
	"To: username@example.org\r\n" +
	"Subject: Ihre Rechnung R0012345\r\n" +
	"Date: %DATE%\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=XX\r\n" +
	"\r\n" +
	"--XX\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"Content-Transfer-Encoding: quoted-printable\r\n" +
	"\r\n" +
	"Guten Tag,=0D=0Ader Rechnungsbetrag von 1.234,56 =E2=82=AC wird abgebucht.\r\n" +
	"--XX\r\n" +
	"Content-Type: application/pdf; name=\"Rechnung_R0012345.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"Rechnung_R0012345.pdf\"\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"JVBERi0xLjQK\r\n" +
	"--XX--\r\n"

func TestMailFindsInvoiceOverIMAP(t *testing.T) {
	be := memory.New()
	user, _ := be.Login(nil, "username", "password")
	box, _ := user.GetMailbox("INBOX")
	body := strings.ReplaceAll(invoiceMail, "%DATE%", time.Now().UTC().Format(time.RFC1123Z))
	if err := box.CreateMessage(nil, time.Now(), strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}

	srv := server.New(be)
	srv.AllowInsecureAuth = true
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)

	out, err := sources.MailData{}.Fetch(context.Background(), sources.Ctx{URL: "imap://127.0.0.1:" + port + "/INBOX", Secret: "username:password"})
	if err != nil {
		t.Fatal(err)
	}
	d := out.(*sources.MailDataset)
	if d.Scanned != 2 || len(d.Invoices) != 1 {
		t.Fatalf("data: %+v", d)
	}
	inv := d.Invoices[0]
	if inv.Amount != 1234.56 || inv.Domain != "hetzner.com" || inv.Sender != "Hetzner Online GmbH" || len(inv.Attachments) != 1 {
		t.Fatalf("invoice: %+v", inv)
	}
}

func TestMailFilesDownloadsPDF(t *testing.T) {
	be := memory.New()
	user, _ := be.Login(nil, "username", "password")
	box, _ := user.GetMailbox("INBOX")
	body := strings.ReplaceAll(invoiceMail, "%DATE%", time.Now().UTC().Format(time.RFC1123Z))
	if err := box.CreateMessage(nil, time.Now(), strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	srv := server.New(be)
	srv.AllowInsecureAuth = true
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	sctx := sources.Ctx{URL: "imap://" + l.Addr().String() + "/INBOX", Secret: "username:password"}

	data, err := sources.MailData{}.Fetch(context.Background(), sctx)
	if err != nil {
		t.Fatal(err)
	}
	uid := data.(*sources.MailDataset).Invoices[0].UID
	files, err := sources.MailFiles(context.Background(), sctx, uid)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "Rechnung_R0012345.pdf" || string(files[0].Content) != "%PDF-1.4\n" {
		t.Fatalf("files: %+v", files)
	}
}
