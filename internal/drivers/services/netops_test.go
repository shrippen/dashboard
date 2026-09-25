package services

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDNSBLName(t *testing.T) {
	if name, ok := dnsblName(net.ParseIP("93.184.216.34"), "zen.spamhaus.org"); !ok || name != "34.216.184.93.zen.spamhaus.org" {
		t.Fatalf("name: %q", name)
	}
	if _, ok := dnsblName(net.ParseIP("::1"), "zen.spamhaus.org"); ok {
		t.Fatal("IPv6 accepted")
	}
}

func TestPaperlessUploadMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, head, err := r.FormFile("document")
		if err != nil || r.Header.Get("Authorization") != "Token tok" || r.URL.Path != "/api/documents/post_document/" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(file)
		if head.Filename != "a.pdf" || string(body) != "%PDF" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(`"task-1"`))
	}))
	defer srv.Close()
	task, err := PaperlessApi{URL: srv.URL, Token: "tok", Verify: true}.Upload(context.Background(), "a.pdf", "", []byte("%PDF"))
	if err != nil || task != "task-1" {
		t.Fatalf("upload: %q %v", task, err)
	}
}
