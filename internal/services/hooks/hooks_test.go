package hooks_test

import (
	"errors"
	"path"
	"strconv"
	"testing"

	"dashboard/internal/enums"
	"dashboard/internal/services/hooks"
	"dashboard/internal/testkit"
)

// Events land only with the right signature and on push services.
func TestReceiveChecksSignatureAndService(t *testing.T) {
	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleUser)
	pg := testkit.Conn(t, d, who, space, enums.ServicePGBackWeb, "https://pg.example")
	kimai := testkit.Conn(t, d, who, space, enums.ServiceKimai, "https://kimai.example")

	url, err := hooks.URL("https://dash.example/", pg)
	if err != nil {
		t.Fatal(err)
	}
	if want := "https://dash.example/hooks/" + strconv.FormatInt(pg, 10) + "/"; url[:len(want)] != want {
		t.Fatalf("url %q", url)
	}
	sig := path.Base(url)

	if err := hooks.Receive(d, pg, sig, "backup.failed", "db"); err != nil {
		t.Fatalf("receive: %v", err)
	}
	if err := hooks.Receive(d, pg, sig+"x", "backup.failed", "db"); !errors.Is(err, hooks.ErrRejected) {
		t.Fatalf("bad signature: %v", err)
	}
	kimaiURL, _ := hooks.URL("https://dash.example", kimai)
	if err := hooks.Receive(d, kimai, path.Base(kimaiURL), "x", "y"); !errors.Is(err, hooks.ErrRejected) {
		t.Fatalf("non-push service: %v", err)
	}
}
