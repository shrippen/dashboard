// Package onboarding guides new users: a checklist that ticks itself from
// real data, and a short intro on each main page until it was seen.
//
//	User.Prefs["onboarding"] = {shown, dismissed, seen: {page: true}, visited: {page: true}}
//
//	shown      the welcome page opened once by itself
//	dismissed  the checklist is hidden ("I can manage")
//	seen       page intros closed
//	visited    pages opened (a checklist step may be "look at it once")
package onboarding

import (
	"database/sql"
	"reflect"

	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/repos/auth"
	"andon/internal/repos/content"
	data "andon/internal/repos/data"
	"andon/internal/repos/users"
	"andon/internal/services/access"
	"andon/internal/services/accounts"
)

const prefKey = "onboarding"

// Step is one checklist item, e.g. "add a connection" with where to do it.
type Step struct {
	Key    string // catalog suffix, welcome.step_<key>
	Done   bool
	Target string // page that does it
}

// State is what a page needs from onboarding.
type State struct {
	Steps     []Step
	Done      int
	Shown     bool
	Dismissed bool
	Seen      map[string]bool // page intros closed
}

// Open reports whether the checklist still has something to do.
func (s State) Open() bool { return !s.Dismissed && s.Done < len(s.Steps) }

// record is the stored part of the state.
type record struct {
	Shown     bool
	Dismissed bool
	Seen      map[string]bool
	Visited   map[string]bool
}

func (r record) prefs() map[string]any {
	return map[string]any{"shown": r.Shown, "dismissed": r.Dismissed, "seen": toAny(r.Seen), "visited": toAny(r.Visited)}
}

func toAny(m map[string]bool) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func fromAny(v any) map[string]bool {
	out := map[string]bool{}
	m, _ := v.(map[string]any)
	for k, x := range m {
		if b, _ := x.(bool); b {
			out[k] = true
		}
	}
	return out
}

func readRecord(prefs map[string]any) record {
	raw, _ := prefs[prefKey].(map[string]any)
	shown, _ := raw["shown"].(bool)
	dismissed, _ := raw["dismissed"].(bool)
	return record{Shown: shown, Dismissed: dismissed, Seen: fromAny(raw["seen"]), Visited: fromAny(raw["visited"])}
}

// Load returns the checklist with its progress and the intro state.
func Load(d *sql.DB, who *access.Principal) (State, error) {
	var out State
	err := db.WithRead(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, who.UserID)
		if err != nil || u == nil {
			return orNoRows(err)
		}
		rec := readRecord(u.Prefs)
		out.Shown, out.Dismissed, out.Seen = rec.Shown, rec.Dismissed, rec.Seen
		out.Steps, err = steps(tx, who, u.TOTPEnabled, rec.Visited)
		for _, s := range out.Steps {
			if s.Done {
				out.Done++
			}
		}
		return err
	})
	return out, err
}

func orNoRows(err error) error {
	if err != nil {
		return err
	}
	return sql.ErrNoRows
}

// steps works out the checklist from what exists, not from clicks.
func steps(tx *sql.Tx, who *access.Principal, totp bool, visited map[string]bool) ([]Step, error) {
	spaceIDs := make([]int64, 0, len(who.Spaces))
	for id := range who.Spaces {
		spaceIDs = append(spaceIDs, id)
	}

	conns, err := content.Connections(tx, spaceIDs)
	if err != nil {
		return nil, err
	}
	personal, missing := false, false
	for _, c := range conns {
		if c.CredentialMode != enums.CredentialPersonal {
			continue
		}
		personal = true
		cred, err := content.Credential(tx, c.ID, who.UserID)
		if err != nil {
			return nil, err
		}
		missing = missing || cred == nil
	}
	placed, err := content.PlacedIn(tx, spaceIDs)
	if err != nil {
		return nil, err
	}
	channels, err := data.Channels(tx, who.UserID)
	if err != nil {
		return nil, err
	}
	keys, err := auth.PasskeysOf(tx, who.UserID)
	if err != nil {
		return nil, err
	}

	out := []Step{{Key: "connection", Done: len(conns) > 0, Target: "/connections/new"}}
	if personal {
		out = append(out, Step{Key: "credentials", Done: !missing, Target: "/me/credentials"})
	}
	out = append(out,
		Step{Key: "tile", Done: placed > 0, Target: "/?edit"},
		Step{Key: "hints", Done: visited["hints"], Target: "/hints"},
		Step{Key: "notify", Done: len(channels) > 0, Target: "/me/notify"},
		Step{Key: "security", Done: totp || len(keys) > 0, Target: "/me/security"},
	)
	if who.IsAdmin() {
		n, err := users.Count(tx)
		if err != nil {
			return nil, err
		}
		invites, err := auth.OpenInvites(tx)
		if err != nil {
			return nil, err
		}
		out = append(out, Step{Key: "invite", Done: n > 1 || len(invites) > 0, Target: "/admin/users"})
	}
	return out, nil
}

// update changes the stored record of who.
func update(d *sql.DB, who *access.Principal, change func(*record)) error {
	profile, err := accounts.GetProfile(d, who)
	if err != nil {
		return err
	}
	rec := readRecord(profile.Prefs)
	before := rec.prefs()
	change(&rec)
	if reflect.DeepEqual(before, rec.prefs()) {
		return nil // e.g. a page visited again: nothing to store
	}
	return accounts.SetPref(d, who, prefKey, rec.prefs())
}

// MarkShown notes that the welcome page opened by itself once.
func MarkShown(d *sql.DB, who *access.Principal) error {
	return update(d, who, func(r *record) { r.Shown = true })
}

// SeeIntro closes the intro of one page.
func SeeIntro(d *sql.DB, who *access.Principal, page string) error {
	return update(d, who, func(r *record) { r.Seen[page] = true })
}

// Visit notes that a page was opened (e.g. hints for the checklist).
func Visit(d *sql.DB, who *access.Principal, page string) error {
	return update(d, who, func(r *record) { r.Visited[page] = true })
}

// Dismiss hides the checklist ("I can manage").
func Dismiss(d *sql.DB, who *access.Principal) error {
	return update(d, who, func(r *record) { r.Dismissed = true })
}

// Resume shows the checklist again.
func Resume(d *sql.DB, who *access.Principal) error {
	return update(d, who, func(r *record) { r.Dismissed = false })
}

// ShowIntros brings all page intros back.
func ShowIntros(d *sql.DB, who *access.Principal) error {
	return update(d, who, func(r *record) { r.Seen = map[string]bool{} })
}
