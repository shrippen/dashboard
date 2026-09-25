package icons_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dashboard/internal/services/icons"
)

func TestURLDownloadsOnceAndRemembersMisses(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/missing.png" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(`<svg><a href="https://evil">x</a><use href="#ok"/></svg>`))
	}))
	defer srv.Close()
	icons.Init(t.TempDir())

	spec := srv.URL + "/logo.svg"
	if got := icons.URL(spec, ""); got != "" {
		t.Fatalf("first call must not block, got %q", got)
	}
	icons.Wait()
	got := icons.URL(spec, "")
	if !strings.HasPrefix(got, "/icons/") {
		t.Fatalf("expected cached icon URL, got %q", got)
	}
	body, media, ok := icons.Read(strings.TrimPrefix(got, "/icons/"))
	if !ok || media != "image/svg+xml" || strings.Contains(string(body), "evil") || !strings.Contains(string(body), `href="#ok"`) {
		t.Fatalf("unexpected icon: ok=%v media=%q body=%s", ok, media, body)
	}

	missing := srv.URL + "/missing.png"
	icons.URL(missing, "")
	icons.Wait()
	before := hits
	if icons.URL(missing, "") != "" {
		t.Fatal("missing icon should give a monogram")
	}
	icons.Wait()
	if hits != before {
		t.Fatal("a remembered miss must not be fetched again")
	}
	if err := icons.ForgetMisses(); err != nil {
		t.Fatal(err)
	}
	icons.URL(missing, "")
	icons.Wait()
	if hits == before {
		t.Fatal("after ForgetMisses the icon should be retried")
	}

	if _, err := icons.Upload([]byte("x"), "text/html"); err != icons.ErrInvalid {
		t.Fatalf("expected ErrInvalid for non-image upload, got %v", err)
	}
}

func TestEmojiAndGlyph(t *testing.T) {
	for spec, want := range map[string]string{"🚀": "🚀", "U+1F680": "🚀", "1f680": "🚀", "👨‍💻": "👨‍💻", "hl-kimai": "", "0041": "", "abc": ""} {
		if got := icons.Emoji(spec); got != want {
			t.Errorf("Emoji(%q) = %q, want %q", spec, got, want)
		}
	}
	for spec, want := range map[string]bool{"mdi-server": true, "si-github": true, "fab fa-github": true, "hl-kimai": false, "sh-immich": false} {
		if icons.Glyph(spec) != want {
			t.Errorf("Glyph(%q) != %v", spec, want)
		}
	}
}
