package hints

// Hint workflow beyond ack/snooze: history, notes, assignment, flapping.
//
//	rule run ──► opened / resolved / reopened ─┐
//	user     ──► acked / snoozed / reset       ├─► hint_events (history)
//	         ──► assigned / work / note       ─┘
//	reopened ≥ flapLimit times in flapWindow ──► View.Flapping (no repeat pushes)

import (
	"database/sql"
	"strings"
	"time"
	"unicode/utf8"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/data"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
)

const (
	flapWindow   = 7 * 24 * time.Hour
	flapLimit    = 3
	maxNoteRunes = 500
	historySize  = 20
)

// markEvents maps a mark to its history entry.
var markEvents = map[enums.HintState]enums.HintEvent{
	enums.HintAcknowledged: enums.EventAcked,
	enums.HintSnoozed:      enums.EventSnoozed,
	enums.HintOpen:         enums.EventReset,
}

// workStates are the states SetWork accepts.
var workStates = map[enums.WorkState]bool{enums.WorkOpen: true, enums.WorkProgress: true, enums.WorkDone: true}

func logEvent(q db.Queryer, hintID int64, kind enums.HintEvent, userID *int64, note string) error {
	return data.AddHintEvent(q, &model.HintEvent{HintID: hintID, Kind: kind, UserID: userID, Note: clip(note), At: time.Now().UTC()})
}

// clip trims a note to maxNoteRunes.
func clip(note string) string {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) <= maxNoteRunes {
		return note
	}
	return string([]rune(note)[:maxNoteRunes])
}

// reachable loads a hint who may act on.
func reachable(q db.Queryer, who *access.Principal, hintID int64) (*model.Hint, error) {
	hint, err := data.HintByID(q, hintID)
	if err != nil {
		return nil, err
	}
	if hint == nil {
		return nil, ErrNotFound
	}
	if _, ok := who.Spaces[hint.SpaceID]; !ok {
		return nil, ErrDenied
	}
	if hint.UserID != nil && *hint.UserID != who.UserID {
		return nil, ErrDenied
	}
	return hint, nil
}

// One returns one hint as who sees it.
func One(d *sql.DB, who *access.Principal, hintID int64) (View, error) {
	var out View
	err := db.WithTx(d, func(tx *sql.Tx) error {
		hint, err := reachable(tx, who, hintID)
		if err != nil {
			return err
		}
		out = viewOf(hint, who)
		return nil
	})
	return out, err
}

// ── Notes and history ──

// AddNote adds a free note to a hint's history.
func AddNote(d *sql.DB, who *access.Principal, hintID int64, note string) error {
	if clip(note) == "" {
		return nil
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		if _, err := reachable(tx, who, hintID); err != nil {
			return err
		}
		return logEvent(tx, hintID, enums.EventNote, &who.UserID, note)
	})
}

// EventView is one history entry as shown; Actor is "" for the system.
type EventView struct {
	Kind  enums.HintEvent
	Actor string
	Note  string
	At    time.Time
}

// History returns a hint's latest history entries, newest first.
func History(d *sql.DB, who *access.Principal, hintID int64) ([]EventView, error) {
	var out []EventView
	err := db.WithTx(d, func(tx *sql.Tx) error {
		if _, err := reachable(tx, who, hintID); err != nil {
			return err
		}
		events, err := data.HintEvents(tx, hintID, historySize)
		if err != nil {
			return err
		}
		names := nameCache{q: tx, names: map[int64]string{}}
		for _, e := range events {
			out = append(out, EventView{Kind: e.Kind, Actor: names.of(e.UserID), Note: e.Note, At: e.At})
		}
		return nil
	})
	return out, err
}

// nameCache resolves user ids to display names once per request.
type nameCache struct {
	q     db.Queryer
	names map[int64]string
}

func (c nameCache) of(id *int64) string {
	if id == nil {
		return ""
	}
	if name, ok := c.names[*id]; ok {
		return name
	}
	u, err := users.Get(c.q, *id)
	name := ""
	if err == nil && u != nil {
		name = u.Name
	}
	c.names[*id] = name
	return name
}

// ── Assignment ──

// Person is a possible assignee.
type Person struct {
	ID   int64
	Name string
}

// Assignees lists who may take over a hint: users who see its space.
func Assignees(d *sql.DB, who *access.Principal, hintID int64) ([]Person, error) {
	var out []Person
	err := db.WithTx(d, func(tx *sql.Tx) error {
		hint, err := reachable(tx, who, hintID)
		if err != nil {
			return err
		}
		all, err := users.All(tx)
		if err != nil {
			return err
		}
		for _, u := range all {
			if !u.IsActive || !sees(tx, u.ID, hint) {
				continue
			}
			out = append(out, Person{ID: u.ID, Name: u.Name})
		}
		return nil
	})
	return out, err
}

// sees reports whether a user reaches a hint.
func sees(q db.Queryer, userID int64, hint *model.Hint) bool {
	if hint.UserID != nil {
		return *hint.UserID == userID
	}
	p, err := access.Load(q, userID)
	if err != nil || p == nil {
		return false
	}
	_, ok := p.Spaces[hint.SpaceID]
	return ok
}

// Assign hands a hint to a user (0 = nobody); the assignee must see it.
func Assign(d *sql.DB, who *access.Principal, hintID, assigneeID int64, note string) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		hint, err := reachable(tx, who, hintID)
		if err != nil {
			return err
		}
		var assignee *int64
		if assigneeID != 0 {
			if !sees(tx, assigneeID, hint) {
				return ErrDenied
			}
			assignee = &assigneeID
		}

		work, err := currentWork(tx, hintID)
		if err != nil {
			return err
		}
		work.AssigneeID = assignee
		work.At = time.Now().UTC()
		if err := data.SetHintWork(tx, work); err != nil {
			return err
		}
		names := nameCache{q: tx, names: map[int64]string{}}
		return logEvent(tx, hintID, enums.EventAssigned, &who.UserID, joinNote(names.of(assignee), note))
	})
}

// SetWork records how far work on a hint got.
func SetWork(d *sql.DB, who *access.Principal, hintID int64, state enums.WorkState, note string) error {
	if !workStates[state] {
		return ErrDenied
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		if _, err := reachable(tx, who, hintID); err != nil {
			return err
		}
		work, err := currentWork(tx, hintID)
		if err != nil {
			return err
		}
		work.State = state
		work.At = time.Now().UTC()
		if err := data.SetHintWork(tx, work); err != nil {
			return err
		}
		return logEvent(tx, hintID, enums.EventWork, &who.UserID, joinNote(string(state), note))
	})
}

func currentWork(q db.Queryer, hintID int64) (*model.HintWork, error) {
	works, err := data.HintWorks(q, []int64{hintID})
	if err != nil {
		return nil, err
	}
	if w, ok := works[hintID]; ok {
		return w, nil
	}
	return &model.HintWork{HintID: hintID, State: enums.WorkOpen}, nil
}

// joinNote: "alex" + "übernehme ich" → "alex – übernehme ich".
func joinNote(subject, note string) string {
	note = strings.TrimSpace(note)
	if subject == "" || note == "" {
		return subject + note
	}
	return subject + " – " + note
}

// ── View enrichment ──

// enrich adds assignment and flapping to views.
func enrich(q db.Queryer, views []View) error {
	ids := make([]int64, len(views))
	for i, v := range views {
		ids[i] = v.ID
	}
	works, err := data.HintWorks(q, ids)
	if err != nil {
		return err
	}
	reopened, err := data.EventCounts(q, ids, enums.EventReopened, time.Now().UTC().Add(-flapWindow))
	if err != nil {
		return err
	}
	names := nameCache{q: q, names: map[int64]string{}}
	for i := range views {
		views[i].Work = enums.WorkOpen
		views[i].Flapping = reopened[views[i].ID] >= flapLimit
		w, ok := works[views[i].ID]
		if !ok {
			continue
		}
		views[i].Work = w.State
		views[i].AssigneeID = w.AssigneeID
		views[i].Assignee = names.of(w.AssigneeID)
	}
	return nil
}
