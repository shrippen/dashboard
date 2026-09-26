package auth_test

import (
	"testing"
	"time"

	"andon/internal/model"
	"andon/internal/repos/auth"
)

func TestPasskeyLifecycle(t *testing.T) {
	q := openTestDB(t)
	uid := userID(t, q)

	p := &model.Passkey{UserID: uid, CredID: "abc", Name: "Laptop", Data: "{}", CreatedAt: time.Now().UTC()}
	if err := auth.AddPasskey(q, p); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := auth.TouchPasskey(q, p.ID, `{"n":1}`, time.Now().UTC()); err != nil {
		t.Fatalf("touch: %v", err)
	}

	list, err := auth.PasskeysOf(q, uid)
	if err != nil || len(list) != 1 || list[0].Data != `{"n":1}` || list[0].LastUsedAt == nil {
		t.Fatalf("list: %+v err=%v", list, err)
	}

	// Credential ids are unique across accounts.
	dup := &model.Passkey{UserID: uid, CredID: "abc", Name: "x", Data: "{}", CreatedAt: time.Now().UTC()}
	if err := auth.AddPasskey(q, dup); err == nil {
		t.Fatal("duplicate cred_id accepted")
	}

	if err := auth.RemovePasskey(q, p.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got, _ := auth.Passkey(q, p.ID); got != nil {
		t.Fatalf("still there: %+v", got)
	}
}
