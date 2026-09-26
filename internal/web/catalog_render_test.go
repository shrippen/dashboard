package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/services/widgetlib"
	"dashboard/internal/sources"
	"dashboard/internal/widgets"
)

// Every catalog tile renders from its service's demo dataset.
func TestCatalogTilesRender(t *testing.T) {
	now := time.Now()
	today := now.Format(time.DateOnly)
	speed := &metrics.History{Series: map[string][]metrics.Point{}}
	for i := range 7 {
		speed.Series[metrics.SampleKey("speedtest", "down")] = append(speed.Series[metrics.SampleKey("speedtest", "down")],
			metrics.Point{Day: now.AddDate(0, 0, i-6), Value: float64(150 + i*15)})
	}

	cases := map[string]struct {
		data any
		want string
	}{
		"kimai_week":       {sources.DemoKimai(now), "weekcol"},
		"kimai_split":      {sources.DemoKimai(now), "weekcol"},
		"unbilled_age":     {sources.DemoKimai(now), "hbar"},
		"disks":            {sources.DemoScrutiny(now), "legend-list"},
		"komodo_stacks":    {sources.DemoKomodo(now), "strip"},
		"truenas_pools":    {sources.DemoTrueNAS(), "hbar"},
		"pihole":           {sources.DemoPihole(now), "progress-bar"},
		"adguard":          {sources.DemoAdGuard(), "progress-bar"},
		"vpn":              {sources.DemoGluetun(), "pill"},
		"gateway":          {sources.DemoGateway(), "tile-value"},
		"expiry":           {sources.DemoCerts(now), "hbar"},
		"speed_history":    {&sources.SpeedtestDataset{Down: 240, Up: 40, ExpectDown: 250, At: now}, "bars-target"},
		"sabnzbd":          {sources.DemoSabnzbd(now), "MB/s"},
		"paperless_inbox":  {sources.DemoPaperless(now), "Posteingang"},
		"mail_invoices":    {sources.DemoMail(now), "tile-value"},
		"freshrss_feeds":   {sources.DemoFreshRSS(now), "hbar"},
		"linkwarden":       {sources.DemoLinkwarden(), "hbar"},
		"gitea_reviews":    {sources.DemoGitea(now), "Reviews offen"},
		"dawarich_day":     {sources.DemoDawarich(now), "kl-day"},
		"authentik_logins": {sources.DemoAuthentik(now), "Anmeldungen"},
		"vaultwarden_2fa":  {sources.DemoVaultwarden(now), "progress-bar"},
	}
	for key, c := range cases {
		kind, ok := widgets.Get(key)
		if !ok {
			t.Fatalf("%s not registered", key)
		}
		cfg, _ := widgets.Decode(key, map[string]any{})
		results := map[string]any{"data": c.data}
		if key == "speed_history" {
			results[widgets.HistorySlot] = speed
		}
		view := kind.View(cfg, results, widgets.ViewCtx{Today: today})
		frag := &widgetlib.Fragment{Type: key, View: view, Slots: map[string]widgetlib.Slot{"data": {Data: c.data}}}

		rec := httptest.NewRecorder()
		if err := (Deps{}).Page(rec, Ctx{Locale: enums.LocaleDE}, kind.Template, http.StatusOK, map[string]any{"ThemeURL": "", "Frag": frag}); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if body := rec.Body.String(); !strings.Contains(body, c.want) {
			t.Errorf("%s: missing %q\n%s", key, c.want, body)
		}
	}
}
