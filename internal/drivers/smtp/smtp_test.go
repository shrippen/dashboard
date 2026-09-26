package smtp_test

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"testing"

	"andon/internal/drivers/smtp"
)

func TestParse(t *testing.T) {
	cases := map[string]smtp.Target{
		"smtp://mailer@mail.example:587?starttls=true": {Host: "mail.example", Port: 587, User: "mailer", StartTLS: true},
		"smtps://mail.example":                         {Host: "mail.example", Port: 465, UseSSL: true},
		"smtp://":                                      {Host: "localhost", Port: 25},
	}
	for raw, want := range cases {
		got, err := smtp.Parse(raw)
		if err != nil || got != want {
			t.Errorf("%s: %+v %v", raw, got, err)
		}
	}
	if _, err := smtp.Parse("smtp://host:port"); err == nil {
		t.Error("bad port accepted")
	}
}

// Send speaks plain SMTP to a server that records the message.
func TestSendDelivers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go fakeServer(ln, got)

	port := ln.Addr().(*net.TCPAddr).Port
	target := smtp.Target{Host: "127.0.0.1", Port: port}
	msg := "Subject: Test\r\n\r\nHallo\r\n"
	if err := smtp.Send(target, "", "dash@example", []string{"a@b.c"}, []byte(msg)); err != nil {
		t.Fatal(err)
	}
	if body := <-got; !strings.Contains(body, "RCPT TO:<a@b.c>") || !strings.Contains(body, "Hallo") {
		t.Fatalf("server saw:\n%s", body)
	}
}

// fakeServer answers one SMTP session with 250s and reports what it read.
func fakeServer(ln net.Listener, got chan<- string) {
	conn, err := ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	reply := func(code int, text string) { conn.Write([]byte(strconv.Itoa(code) + " " + text + "\r\n")) }
	var seen strings.Builder
	reply(220, "fake")
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}
		seen.WriteString(line)
		switch {
		case inData && line == ".\r\n":
			inData = false
			reply(250, "queued")
		case inData:
		case strings.HasPrefix(line, "DATA"):
			inData = true
			reply(354, "go")
		case strings.HasPrefix(line, "QUIT"):
			reply(221, "bye")
			got <- seen.String()
			return
		default:
			reply(250, "ok")
		}
	}
	got <- seen.String()
}
