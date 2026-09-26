package onboarding_test

import (
	"testing"

	"andon/internal/enums"
	"andon/internal/services/connections"
	"andon/internal/services/onboarding"
	"andon/internal/testkit"
)

func done(s onboarding.State) map[string]bool {
	out := map[string]bool{}
	for _, step := range s.Steps {
		out[step.Key] = step.Done
	}
	return out
}

// TestChecklistTicksFromData: a fresh admin has everything open; a
// connection, a placed tile and a visit to the hints tick their steps; a
// personal connection without an own token adds the credentials step.
func TestChecklistTicksFromData(t *testing.T) {
	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleAdmin)

	s, err := onboarding.Load(d, who)
	if err != nil {
		t.Fatal(err)
	}
	if s.Done != 0 || !s.Open() || len(s.Steps) != 6 {
		t.Fatalf("fresh admin: %+v", s)
	}

	testkit.Place(t, d, who, space, "clock", nil, nil)
	if _, err := connections.Create(d, who, space, enums.ServiceKimai, "K", "https://k.test", enums.CredentialPersonal, "", connections.TLSVerify, nil); err != nil {
		t.Fatal(err)
	}
	if err := onboarding.Visit(d, who, "hints"); err != nil {
		t.Fatal(err)
	}
	s, _ = onboarding.Load(d, who)
	got := done(s)
	if !got["connection"] || !got["tile"] || !got["hints"] || got["credentials"] || got["notify"] {
		t.Fatalf("after setup: %v", got)
	}
	if _, has := got["invite"]; !has {
		t.Fatal("admins get the invite step")
	}
}

// TestIntrosAndDismiss: an intro closes once and comes back on request;
// dismissing hides the checklist.
func TestIntrosAndDismiss(t *testing.T) {
	d := testkit.DB(t)
	who, _ := testkit.User(t, d, "u@b.c", enums.RoleUser)

	if err := onboarding.SeeIntro(d, who, "connections"); err != nil {
		t.Fatal(err)
	}
	s, _ := onboarding.Load(d, who)
	if !s.Seen["connections"] || s.Seen["hints"] {
		t.Fatalf("seen: %v", s.Seen)
	}
	if _, has := done(s)["invite"]; has {
		t.Fatal("non-admins have no invite step")
	}
	_ = onboarding.ShowIntros(d, who)
	_ = onboarding.Dismiss(d, who)
	s, _ = onboarding.Load(d, who)
	if len(s.Seen) != 0 || s.Open() {
		t.Fatalf("after reset and dismiss: %+v", s)
	}
}
