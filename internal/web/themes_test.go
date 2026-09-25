package web_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func postFile(t *testing.T, client *http.Client, target string, fields map[string]string, name string, data []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	part, _ := w.CreateFormFile("file", name)
	_, _ = part.Write(data)
	_ = w.Close()
	resp, err := client.Post(target, w.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	return resp
}

// TestThemeDuplicateEditFontExportImport: duplicate shrippen, change a
// token, upload a font, and round-trip it through the ZIP export.
func TestThemeDuplicateEditFontExportImport(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)

	list := mustGet(t, srv, client, "/themes")
	builtin := regexp.MustCompile(`name="theme_id" value="(\d+)"`).FindSubmatch(list)
	space := regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(list)
	if builtin == nil || space == nil {
		t.Fatalf("theme list incomplete:\n%s", list)
	}

	resp := postForm(t, client, srv.URL+"/themes/duplicate", url.Values{
		"csrf": {csrf}, "theme_id": {string(builtin[1])}, "space_id": {string(space[1])}, "name": {"Mine"},
	})
	edit := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || edit == "" {
		t.Fatalf("duplicate: %d", resp.StatusCode)
	}
	id := strings.TrimPrefix(edit, "/themes/")

	resp = postForm(t, client, srv.URL+edit, url.Values{"csrf": {csrf}, "name": {"Mine"}, "dark--accent": {"#123456"}})
	if resp.StatusCode != http.StatusSeeOther && resp.StatusCode != http.StatusOK {
		t.Fatalf("save: %d", resp.StatusCode)
	}

	resp = postFile(t, client, srv.URL+edit+"/fonts", map[string]string{"csrf": csrf}, "../Evil.woff2", []byte("x"))
	resp.Body.Close()
	resp = postFile(t, client, srv.URL+edit+"/fonts", map[string]string{"csrf": csrf}, "Inter-600.woff2", []byte("wOF2font"))
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("font upload: %d", resp.StatusCode)
	}

	css := string(mustGet(t, srv, client, "/theme/"+id+".css"))
	if !strings.Contains(css, "--accent:#123456") || !strings.Contains(css, `font-family:"Inter";font-weight:600`) {
		t.Fatalf("css missing token or font-face:\n%s", css)
	}
	if font := mustGet(t, srv, freshClient(t), "/theme-fonts/"+id+"/Inter-600.woff2"); string(font) != "wOF2font" {
		t.Fatalf("font not served: %q", font)
	}

	zipData := mustGet(t, srv, client, edit+"/export")
	resp = postFile(t, client, srv.URL+"/themes/import", map[string]string{"csrf": csrf, "space_id": string(space[1])}, "mine.zip", zipData)
	resp.Body.Close()
	imported := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusSeeOther || imported == edit {
		t.Fatalf("import: %d %s", resp.StatusCode, imported)
	}
	css = string(mustGet(t, srv, client, "/theme/"+strings.TrimPrefix(imported, "/themes/")+".css"))
	if !strings.Contains(css, "--accent:#123456") || !strings.Contains(css, "Inter-600.woff2") {
		t.Fatalf("import lost token or font:\n%s", css)
	}

	if !strings.Contains(string(mustGet(t, srv, client, "/styleguide")), `class="sample"`) {
		t.Fatal("styleguide missing sample")
	}
}
