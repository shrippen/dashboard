package settings_test

import (
	"testing"

	"andon/internal/settings"
)

// Environment values override defaults; broken numbers and flags keep
// the default instead of zeroing it.
func TestLoadReadsEnvironment(t *testing.T) {
	t.Setenv("BASE_URL", "https://dash.example")
	t.Setenv("DATA_DIR", "/srv/dash")
	t.Setenv("ANDON_DEV", "yes please")
	t.Setenv("ANALYSIS_MINUTES", "ten")

	s := settings.Load()
	if s.BaseURL != "https://dash.example" || !s.SecureCookies() {
		t.Fatalf("base url: %+v", s)
	}
	if s.DBPath() != "/srv/dash/andon.db" || s.IconsDir() != "/srv/dash/icons" {
		t.Fatalf("paths: %q %q", s.DBPath(), s.IconsDir())
	}
	if s.Dev || s.AnalysisMinutes <= 0 {
		t.Fatalf("bad values must keep defaults: dev=%v minutes=%d", s.Dev, s.AnalysisMinutes)
	}
}
