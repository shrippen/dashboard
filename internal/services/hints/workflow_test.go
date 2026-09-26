package hints_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/enums"
	"andon/internal/rules"
	"andon/internal/services/access"
	"andon/internal/services/accounts"
	"andon/internal/services/hints"
)

func person(t *testing.T, d *sql.DB, email string) *access.Principal {
	t.Helper()
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		u, err := accounts.Create(tx, email, email[:1], nil, enums.RoleUser, enums.LocaleDE, "")
		if err == nil {
			id = u.ID
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	who, err := access.Load(d, id)
	if err != nil {
		t.Fatal(err)
	}
	return who
}

func ownSpace(who *access.Principal) int64 {
	for id := range who.Spaces {
		return id
	}
	return 0
}

// History records the rule's lifecycle and the user's steps; flapping
// shows after repeated reopenings; outsiders cannot be assigned.
func TestHintWorkflow(t *testing.T) {
	crypto.Init("test-master-key")
	d, err := db.Open(filepath.Join(t.TempDir(), "w.db"), dbtest.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	alex, kim := person(t, d, "a@x.de"), person(t, d, "k@x.de")
	sid := ownSpace(alex)
	rule := []string{"kimai.missing_day"}
	f := []rules.Finding{finding("x")}

	// Open, then resolve and reopen three times.
	for i := 0; i < 4; i++ {
		if _, err := hints.Sync(d, sid, nil, nil, rule, f); err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			if _, err := hints.Sync(d, sid, nil, nil, rule, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	views, err := hints.Active(d, alex, enums.SeverityInfo, nil, 0)
	if err != nil || len(views) != 1 || !views[0].Flapping || views[0].Work != enums.WorkOpen {
		t.Fatalf("views: %+v %v", views, err)
	}
	id := views[0].ID

	if err := hints.Assign(d, alex, id, kim.UserID, ""); err != hints.ErrDenied {
		t.Fatalf("outsider assigned: %v", err)
	}
	if err := hints.Assign(d, alex, id, alex.UserID, "mache ich"); err != nil {
		t.Fatal(err)
	}
	if err := hints.SetWork(d, alex, id, enums.WorkProgress, ""); err != nil {
		t.Fatal(err)
	}
	if err := hints.AddNote(d, kim, id, "fremd"); err != hints.ErrDenied {
		t.Fatalf("outsider note: %v", err)
	}

	views, _ = hints.Active(d, alex, enums.SeverityInfo, nil, 0)
	if views[0].Assignee != "a" || views[0].Work != enums.WorkProgress {
		t.Fatalf("assignment: %+v", views[0])
	}

	if err := hints.Act(d, alex, id, hints.ActionAck, 0, "Kunde informiert"); err != nil {
		t.Fatal(err)
	}
	history, err := hints.History(d, alex, id)
	if err != nil {
		t.Fatal(err)
	}
	if history[0].Kind != enums.EventAcked || history[0].Note != "Kunde informiert" || history[0].Actor != "a" {
		t.Fatalf("latest: %+v", history[0])
	}
	if last := history[len(history)-1]; last.Kind != enums.EventOpened || last.Actor != "" {
		t.Fatalf("first: %+v", last)
	}
}
