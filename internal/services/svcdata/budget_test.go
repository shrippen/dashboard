package svcdata_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/repos/content"
	data "andon/internal/repos/data"
	"andon/internal/repos/users"
	"andon/internal/services/svcdata"
	"andon/internal/sources"
)

type budgetSource struct{ calls *int }

func (budgetSource) Key() string                { return "test.budget" }
func (budgetSource) TTL() time.Duration         { return time.Nanosecond }
func (budgetSource) Service() enums.ServiceType { return "" }

func (s budgetSource) Fetch(context.Context, sources.Ctx) (any, error) {
	*s.calls++
	return map[string]any{"n": *s.calls}, nil
}

// TestBudgetStopsFetching: once a connection used its daily budget, reads
// answer with the last result instead of reaching the service; every
// fetch counts towards the health record.
func TestBudgetStopsFetching(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	u := &model.User{Email: "a@b.c", Name: "a", Role: enums.RoleUser, IsActive: true,
		Locale: enums.LocaleDE, ColorMode: enums.ColorAuto, CreatedAt: time.Now().UTC()}
	if err := users.Add(d, u); err != nil {
		t.Fatal(err)
	}
	space := &model.Space{Kind: enums.SpacePersonal, Name: "a", OwnerUserID: &u.ID, Version: 1}
	if err := content.AddSpace(d, space); err != nil {
		t.Fatal(err)
	}
	conn := &model.Connection{SpaceID: space.ID, Key: "x", Name: "x", Service: "jsonapi", URL: "http://x",
		CredentialMode: enums.CredentialShared, DailyBudget: 2, CreatedAt: time.Now().UTC()}
	if err := content.AddConnection(d, conn); err != nil {
		t.Fatal(err)
	}

	calls := 0
	sources.Register(budgetSource{&calls})
	var last svcdata.Result
	for range 4 {
		if last, err = svcdata.Get(context.Background(), d, "test.budget", nil, conn, nil, svcdata.Force); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 2 || !last.Ok() {
		t.Fatalf("calls=%d last=%+v", calls, last)
	}
	if n, _ := data.Fetches(d, conn.ID, time.Now().UTC().Format(time.DateOnly)); n != 2 {
		t.Fatalf("recorded %d fetches", n)
	}
}
