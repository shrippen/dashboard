package notify_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/db/dbtest"
	"dashboard/internal/enums"
	"dashboard/internal/rules"
	"dashboard/internal/services/access"
	"dashboard/internal/services/accounts"
	"dashboard/internal/services/hints"
	"dashboard/internal/services/notify"
	"dashboard/internal/settings"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	crypto.Init("test-master-key")
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func addUser(t *testing.T, d *sql.DB) int64 {
	t.Helper()
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		u, err := accounts.Create(tx, "a@x.de", "A", nil, enums.RoleUser, enums.LocaleDE, "")
		if err != nil {
			return err
		}
		id = u.ID
		return nil
	})
	if err != nil {
		t.Fatalf("add user: %v", err)
	}
	return id
}

func addUser2(t *testing.T, d *sql.DB) int64 {
	t.Helper()
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		u, err := accounts.Create(tx, "b@x.de", "B", nil, enums.RoleUser, enums.LocaleDE, "")
		if err != nil {
			return err
		}
		id = u.ID
		return nil
	})
	if err != nil {
		t.Fatalf("add second user: %v", err)
	}
	return id
}

func principalFor(t *testing.T, d *sql.DB, userID int64) *access.Principal {
	t.Helper()
	who, err := access.Load(d, userID)
	if err != nil {
		t.Fatalf("load principal: %v", err)
	}
	return who
}

func TestAddChannelRejectsPlainText(t *testing.T) {
	d := openTestDB(t)
	who := principalFor(t, d, addUser(t, d))
	if err := notify.AddChannel(d, who, "x", "not a url", enums.SeverityInfo, nil); err != notify.ErrInvalidURL {
		t.Fatalf("expected ErrInvalidURL, got %v", err)
	}
}

func TestAddChannelListAndDelete(t *testing.T) {
	d := openTestDB(t)
	who := principalFor(t, d, addUser(t, d))
	if err := notify.AddChannel(d, who, "My phone", "ntfy://ntfy.example/topic", enums.SeverityWarn, nil); err != nil {
		t.Fatalf("add channel: %v", err)
	}
	chans, err := notify.Channels(d, who)
	if err != nil {
		t.Fatalf("channels: %v", err)
	}
	if len(chans) != 1 || chans[0].Hint != "ntfy://…/topic" {
		t.Fatalf("expected 1 masked channel, got %+v", chans)
	}
	if err := notify.DeleteChannel(d, who, chans[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	chans, _ = notify.Channels(d, who)
	if len(chans) != 0 {
		t.Fatalf("expected channel gone, got %+v", chans)
	}
}

func TestDeleteChannelDeniesOtherUsers(t *testing.T) {
	d := openTestDB(t)
	owner := principalFor(t, d, addUser(t, d))
	if err := notify.AddChannel(d, owner, "x", "ntfy://a/b", enums.SeverityInfo, nil); err != nil {
		t.Fatalf("add: %v", err)
	}
	chans, _ := notify.Channels(d, owner)
	stranger := principalFor(t, d, addUser2(t, d))
	if err := notify.DeleteChannel(d, stranger, chans[0].ID); err != notify.ErrDenied {
		t.Fatalf("expected ErrDenied, got %v", err)
	}
}

func TestSavePrefsRejectsBadTime(t *testing.T) {
	d := openTestDB(t)
	who := principalFor(t, d, addUser(t, d))
	err := notify.SavePrefs(d, who, notify.Prefs{QuietFrom: "not-a-time"})
	if err != notify.ErrBadTime {
		t.Fatalf("expected ErrBadTime, got %v", err)
	}
}

func TestSavePrefsRoundTrips(t *testing.T) {
	d := openTestDB(t)
	who := principalFor(t, d, addUser(t, d))
	if err := notify.SavePrefs(d, who, notify.Prefs{QuietFrom: "22:00", QuietTo: "07:00"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := notify.GetPrefs(d, who)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.QuietFrom != "22:00" || got.QuietTo != "07:00" {
		t.Fatalf("unexpected prefs: %+v", got)
	}
}

func TestDispatchSendsFreshHintAndSkipsOnRerun(t *testing.T) {
	d := openTestDB(t)
	userID := addUser(t, d)
	who := principalFor(t, d, userID)
	if err := notify.AddChannel(d, who, "phone", "ntfy://ntfy.example/topic", enums.SeverityInfo, nil); err != nil {
		t.Fatalf("add channel: %v", err)
	}

	var calls int
	var lastPayload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewDecoder(r.Body).Decode(&lastPayload)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sid := onlySpace(t, who)
	if _, err := hints.Sync(d, sid, nil, nil, []string{"kimai.missing_day"}, []rules.Finding{{
		Fingerprint: "missing:1", Rule: "kimai.missing_day", Severity: enums.SeverityInfo,
		Message: "kimai.missing_day", Params: map[string]any{"day": map[string]any{"$day": "2026-03-01"}},
	}}); err != nil {
		t.Fatalf("sync hint: %v", err)
	}

	cfg := settings.Settings{AppriseAPIURL: srv.URL}
	n, err := notify.Dispatch(context.Background(), d, cfg)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if n != 1 || calls != 1 {
		t.Fatalf("expected 1 push, got n=%d calls=%d", n, calls)
	}
	if lastPayload["title"] == "" {
		t.Fatalf("expected a title in the payload, got %+v", lastPayload)
	}

	n, err = notify.Dispatch(context.Background(), d, cfg)
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if n != 0 || calls != 1 {
		t.Fatalf("expected no duplicate push, got n=%d calls=%d", n, calls)
	}
}

func TestDispatchSkipsQuietHours(t *testing.T) {
	d := openTestDB(t)
	userID := addUser(t, d)
	who := principalFor(t, d, userID)
	if err := notify.AddChannel(d, who, "phone", "ntfy://ntfy.example/topic", enums.SeverityInfo, nil); err != nil {
		t.Fatalf("add channel: %v", err)
	}
	// A 24h window covers "now" no matter when the test runs.
	if err := notify.SavePrefs(d, who, notify.Prefs{QuietFrom: "00:00", QuietTo: "23:59"}); err != nil {
		t.Fatalf("save prefs: %v", err)
	}

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sid := onlySpace(t, who)
	if _, err := hints.Sync(d, sid, nil, nil, []string{"kimai.missing_day"}, []rules.Finding{{
		Fingerprint: "missing:1", Rule: "kimai.missing_day", Severity: enums.SeverityInfo,
		Message: "kimai.missing_day", Params: map[string]any{"day": map[string]any{"$day": "2026-03-01"}},
	}}); err != nil {
		t.Fatalf("sync hint: %v", err)
	}

	n, err := notify.Dispatch(context.Background(), d, settings.Settings{AppriseAPIURL: srv.URL})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if n != 0 || calls != 0 {
		t.Fatalf("expected quiet hours to suppress the push, got n=%d calls=%d", n, calls)
	}
}

func onlySpace(t *testing.T, who *access.Principal) int64 {
	t.Helper()
	for id := range who.Spaces {
		return id
	}
	t.Fatal("expected the user to have a personal space")
	return 0
}

// Quiet hours let critical hints through unless muted; channels take only
// their subscribed sources.
func TestDispatchQuietCriticalAndSubscriptions(t *testing.T) {
	d := openTestDB(t)
	who := principalFor(t, d, addUser(t, d))
	if err := notify.AddChannel(d, who, "kimai", "ntfy://ntfy.example/kimai", enums.SeverityInfo, []string{"kimai"}); err != nil {
		t.Fatal(err)
	}
	if err := notify.AddChannel(d, who, "gitea", "ntfy://ntfy.example/gitea", enums.SeverityInfo, []string{"gitea"}); err != nil {
		t.Fatal(err)
	}
	if err := notify.SavePrefs(d, who, notify.Prefs{QuietFrom: "00:00", QuietTo: "23:59"}); err != nil {
		t.Fatal(err)
	}

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	cfg := settings.Settings{AppriseAPIURL: srv.URL}

	sid := onlySpace(t, who)
	if _, err := hints.Sync(d, sid, nil, nil, []string{"kimai.missing_day"}, []rules.Finding{
		{Fingerprint: "a", Rule: "kimai.missing_day", Severity: enums.SeverityCritical, Message: "kimai.missing_day", Sources: []string{"kimai"}},
		{Fingerprint: "b", Rule: "kimai.missing_day", Severity: enums.SeverityInfo, Message: "kimai.missing_day", Sources: []string{"kimai"}},
	}); err != nil {
		t.Fatal(err)
	}

	// Only the kimai channel, and only the critical hint.
	if n, err := notify.Dispatch(context.Background(), d, cfg); err != nil || n != 1 || calls != 1 {
		t.Fatalf("critical in quiet hours: n=%d calls=%d err=%v", n, calls, err)
	}

	if err := notify.SavePrefs(d, who, notify.Prefs{QuietFrom: "00:00", QuietTo: "23:59", QuietMuted: true}); err != nil {
		t.Fatal(err)
	}
	if n, _ := notify.Dispatch(context.Background(), d, cfg); n != 0 {
		t.Fatalf("muted quiet hours sent %d", n)
	}
}
