package sources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"andon/internal/drivers/httpclient"
)

func TestAllowlistBlocksPrivateUnlessListed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	t.Cleanup(func() { httpclient.SetGuard(nil) })

	call := func() error {
		resp, err := httpclient.Request(context.Background(), http.MethodGet, srv.URL, httpclient.Options{})
		if err == nil {
			resp.Body.Close()
		}
		return err
	}

	if err := ApplyNetwork(NetworkPolicy{Mode: NetAllowlist, Public: true}); err != nil {
		t.Fatal(err)
	}
	if _, denied := call().(httpclient.EgressDenied); !denied {
		t.Fatal("expected loopback denied under allowlist")
	}

	if err := ApplyNetwork(NetworkPolicy{Mode: NetAllowlist, Networks: []string{"127.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	if err := call(); err != nil {
		t.Fatalf("expected listed network allowed: %v", err)
	}

	if err := ApplyNetwork(NetworkPolicy{Mode: NetOpen}); err != nil {
		t.Fatal(err)
	}
	if err := call(); err != nil {
		t.Fatalf("expected open mode: %v", err)
	}
}
