package spaces_test

import (
	"testing"

	"dashboard/internal/enums"
	"dashboard/internal/services/spaces"
	"dashboard/internal/testkit"
)

// Nav links keep web and site-relative addresses only; titles default to
// the address.
func TestPageNavKeepsSafeLinks(t *testing.T) {
	form := map[string]string{
		spaces.PageTitle: " Start ",
		spaces.PageNav:   "Docs | /docs\njavascript:alert(1)\nEvil | //evil.example\nhttps://git.example",
	}
	page := spaces.PageOf(spaces.ParsePage(func(k string) string { return form[k] }))

	if page.Title != "Start" || len(page.Nav) != 2 {
		t.Fatalf("page: %+v", page)
	}
	if page.Nav[0] != (spaces.NavLink{Title: "Docs", URL: "/docs"}) || page.Nav[1].Title != "https://git.example" {
		t.Fatalf("nav: %+v", page.Nav)
	}
}

// Nobody edits another user's personal space.
func TestUpdateOwnSpaceOnly(t *testing.T) {
	d := testkit.DB(t)
	owner, space := testkit.User(t, d, "a@b.c", enums.RoleUser)
	other, _ := testkit.User(t, d, "x@y.z", enums.RoleAdmin)

	if err := spaces.Update(d, other, space, map[string]any{spaces.PageTitle: "Mine"}, ""); err == nil {
		t.Fatal("foreign personal space changed")
	}
	if err := spaces.Update(d, owner, space, map[string]any{spaces.PageTitle: "Mine"}, ""); err != nil {
		t.Fatal(err)
	}
	settings, err := spaces.Settings(d, owner, space)
	if err != nil || settings[spaces.PageTitle] != "Mine" {
		t.Fatalf("settings: %v %v", settings, err)
	}
}
