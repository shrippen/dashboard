// Package hints stores rule findings, reconciles them per run, and lets
// users act on them.
//
//	rule run (space, user?, connection)
//	    findings ──► upsert by fingerprint ──► missing ones: resolved
//	                                            reappearing: reopened, marks dropped
//	reader
//	    open hints of reachable spaces − acknowledged − snoozed(until > now)
package hints

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/i18n"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/data"
	"dashboard/internal/rules"
	"dashboard/internal/services/access"
)

const (
	resolvedRetention = 90 * 24 * time.Hour
	ackModeKey        = "hint_ack"
)

// ErrNotFound means the hint does not exist.
var ErrNotFound = errors.New("hints: not found")

// ErrDenied means the principal may not act on this hint.
var ErrDenied = errors.New("hints: access denied")

// Sync applies one rule run: upserts findings by fingerprint, reopens
// previously resolved hints that reappeared (dropping their old marks),
// and resolves hints of these rules that didn't fire this time. Returns
// the number of hints that are new or reopened.
func Sync(q db.Queryer, spaceID int64, userID *int64, connID *int64, ruleIDs []string, findings []rules.Finding) (int, error) {
	now := time.Now().UTC()
	seen := map[string]bool{}
	fresh := 0

	for _, item := range findings {
		// Two connections of one service in a space must not share fingerprints.
		fingerprint := item.Fingerprint
		if connID != nil {
			fingerprint = fmt.Sprintf("%d:%s", *connID, item.Fingerprint)
		}
		seen[fingerprint] = true

		hint, err := data.HintByPrint(q, spaceID, userID, fingerprint)
		if err != nil {
			return 0, err
		}
		if hint == nil {
			hint = &model.Hint{SpaceID: spaceID, UserID: userID, Fingerprint: fingerprint, FirstSeen: now}
			fill(hint, item, connID, now)
			if err := data.AddHint(q, hint); err != nil {
				return 0, err
			}
			if err := logEvent(q, hint.ID, enums.EventOpened, nil, ""); err != nil {
				return 0, err
			}
			fresh++
			continue
		}

		if hint.ResolvedAt != nil {
			hint.ResolvedAt = nil
			hint.FirstSeen = now
			if err := data.DropMarks(q, hint.ID); err != nil {
				return 0, err
			}
			if err := logEvent(q, hint.ID, enums.EventReopened, nil, ""); err != nil {
				return 0, err
			}
			fresh++
		}
		fill(hint, item, connID, now)
		if err := data.UpdateHint(q, hint); err != nil {
			return 0, err
		}
	}

	stillFiring, err := data.HintsOfScope(q, spaceID, userID, ruleIDs, connID)
	if err != nil {
		return 0, err
	}
	for _, h := range stillFiring {
		if !seen[h.Fingerprint] && h.ResolvedAt == nil {
			h.ResolvedAt = &now
			if err := data.UpdateHint(q, h); err != nil {
				return 0, err
			}
			if err := logEvent(q, h.ID, enums.EventResolved, nil, ""); err != nil {
				return 0, err
			}
		}
	}

	return fresh, nil
}

func fill(hint *model.Hint, item rules.Finding, connID *int64, now time.Time) {
	hint.Rule = item.Rule
	hint.Severity = item.Severity
	hint.Message = item.Message
	hint.Params = item.Params
	hint.ActionURL = item.ActionURL
	hint.ActionLabel = item.ActionLabel
	hint.Due = item.Due
	hint.Sources = item.Sources
	hint.ConnectionID = connID
	hint.LastSeen = now
}

// ── Read ──

// View is a hint rendered for one reader: translated title/why, formatted
// params, in the reader's own language.
type View struct {
	ID           int64
	Rule         string
	Severity     enums.Severity
	Title        string
	Why          string
	ActionURL    string
	ActionLabel  string
	Due          string
	Sources      []string
	SpaceName    string
	FirstSeen    time.Time
	ConnectionID *int64
	Assignee     string // "" = nobody
	AssigneeID   *int64
	Work         enums.WorkState
	Flapping     bool    // reopened often lately; pushed only once
	Value        float64 // largest money amount the hint names, 0 if none
	Currency     string
}

func hidden(marks []*model.HintMark, now time.Time) bool {
	for _, m := range marks {
		if m.State == enums.HintAcknowledged {
			return true
		}
		if m.State == enums.HintSnoozed && m.Until != nil && m.Until.After(now) {
			return true
		}
	}
	return false
}

func visible(q db.Queryer, who *access.Principal) ([]*model.Hint, error) {
	spaceIDs := make([]int64, 0, len(who.Spaces))
	for id := range who.Spaces {
		spaceIDs = append(spaceIDs, id)
	}
	found, err := data.HintsIn(q, spaceIDs, who.UserID)
	if err != nil {
		return nil, err
	}

	hintIDs := make([]int64, len(found))
	for i, h := range found {
		hintIDs[i] = h.ID
	}
	marks, err := data.Marks(q, hintIDs, who.UserID)
	if err != nil {
		return nil, err
	}
	byHint := map[int64][]*model.HintMark{}
	for _, m := range marks {
		byHint[m.HintID] = append(byHint[m.HintID], m)
	}

	now := time.Now().UTC()
	out := found[:0]
	for _, h := range found {
		if !hidden(byHint[h.ID], now) {
			out = append(out, h)
		}
	}
	return out, nil
}

// Active returns the hints visible to who, filtered to min severity and
// (if given) at least one matching source, most severe / soonest due /
// oldest first, capped at limit if positive.
func Active(d *sql.DB, who *access.Principal, minSeverity enums.Severity, sourcesFilter []string, limit int) ([]View, error) {
	return Filtered(d, who, Filter{MinSeverity: minSeverity, Sources: sourcesFilter}, limit)
}

// Filter narrows the visible hints; empty lists match everything.
type Filter struct {
	MinSeverity enums.Severity
	Sources     []string
	Rules       []string
}

// Filtered is Active with a rule filter too (topic widgets).
func Filtered(d *sql.DB, who *access.Principal, f Filter, limit int) ([]View, error) {
	var views []View
	err := db.WithTx(d, func(tx *sql.Tx) error {
		found, err := visible(tx, who)
		if err != nil {
			return err
		}

		rows := found[:0]
		for _, h := range found {
			if h.Severity < f.MinSeverity {
				continue
			}
			if len(f.Sources) > 0 && !anyMatch(h.Sources, f.Sources) {
				continue
			}
			if len(f.Rules) > 0 && !anyMatch([]string{h.Rule}, f.Rules) {
				continue
			}
			rows = append(rows, h)
		}
		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].Severity != rows[j].Severity {
				return rows[i].Severity > rows[j].Severity
			}
			di, dj := rows[i].Due, rows[j].Due
			if di == "" {
				di = "9999"
			}
			if dj == "" {
				dj = "9999"
			}
			if di != dj {
				return di < dj
			}
			return rows[i].FirstSeen.Before(rows[j].FirstSeen)
		})
		if limit > 0 && len(rows) > limit {
			rows = rows[:limit]
		}

		views = make([]View, len(rows))
		for i, h := range rows {
			views[i] = viewOf(h, who)
		}
		return enrich(tx, views)
	})
	return views, err
}

func anyMatch(have, want []string) bool {
	set := map[string]bool{}
	for _, w := range want {
		set[w] = true
	}
	for _, h := range have {
		if set[h] {
			return true
		}
	}
	return false
}

func viewOf(h *model.Hint, who *access.Principal) View {
	locale := who.Locale
	params := i18n.Typed(h.Params, locale)
	spaceName := ""
	if sp, ok := who.Spaces[h.SpaceID]; ok {
		spaceName = sp.Name
	}
	actionLabel := ""
	if h.ActionLabel != "" {
		actionLabel = i18n.T("action."+h.ActionLabel, locale, nil)
	}
	value, currency := moneyValue(h.Params)
	return View{
		ID: h.ID, Rule: h.Rule, Severity: h.Severity, Value: value, Currency: currency,
		Title:     i18n.T("hint."+h.Message+".title", locale, params),
		Why:       i18n.T("hint."+h.Message+".why", locale, params),
		ActionURL: h.ActionURL, ActionLabel: actionLabel, Due: h.Due, Sources: h.Sources,
		SpaceName: spaceName, FirstSeen: h.FirstSeen, ConnectionID: h.ConnectionID,
	}
}

// CountFor returns (count, highest severity) of open hints on one connection.
func CountFor(d *sql.DB, who *access.Principal, connID int64) (int, enums.Severity, error) {
	var count int
	var top enums.Severity
	err := db.WithTx(d, func(tx *sql.Tx) error {
		found, err := visible(tx, who)
		if err != nil {
			return err
		}
		for _, h := range found {
			if h.ConnectionID != nil && *h.ConnectionID == connID {
				count++
				if h.Severity > top {
					top = h.Severity
				}
			}
		}
		return nil
	})
	return count, top, err
}

// Summary counts open hints per severity level.
func Summary(d *sql.DB, who *access.Principal) (map[enums.Severity]int, error) {
	counts := map[enums.Severity]int{enums.SeverityInfo: 0, enums.SeverityWarn: 0, enums.SeverityCritical: 0}
	err := db.WithTx(d, func(tx *sql.Tx) error {
		found, err := visible(tx, who)
		if err != nil {
			return err
		}
		for _, h := range found {
			counts[h.Severity]++
		}
		return nil
	})
	return counts, err
}

// ── Act ──

func ackMode(q db.Queryer, spaceID int64) (enums.HintAckMode, error) {
	space, err := content.Space(q, spaceID)
	if err != nil {
		return "", err
	}
	if space == nil || space.Kind != enums.SpaceTeam {
		return enums.AckPerUser, nil
	}
	if raw, ok := space.Settings[ackModeKey].(string); ok && raw != "" {
		return enums.HintAckMode(raw), nil
	}
	return enums.AckPerUser, nil
}

// Mark sets (or clears, for HintOpen) one hint's acknowledgement state for
// who, honoring the space's ack mode (per-user vs. team-wide).
func Mark(d *sql.DB, who *access.Principal, hintID int64, state enums.HintState, until *time.Time, note string) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		hint, err := reachable(tx, who, hintID)
		if err != nil {
			return err
		}
		if err := logEvent(tx, hintID, markEvents[state], &who.UserID, note); err != nil {
			return err
		}

		mode, err := ackMode(tx, hint.SpaceID)
		if err != nil {
			return err
		}
		teamWide := mode == enums.AckTeam
		if teamWide && state == enums.HintAcknowledged {
			space, err := access.SpaceOf(tx, who, hint.SpaceID)
			if err != nil {
				return err
			}
			if access.SpaceRight(who, space) < enums.RightEdit {
				teamWide = false
			}
		}

		var owner *int64
		if !teamWide {
			owner = &who.UserID
		}
		existing, err := data.Mark(tx, hintID, owner)
		if err != nil {
			return err
		}
		if state == enums.HintOpen {
			if existing != nil {
				return data.RemoveMark(tx, existing.ID)
			}
			return nil
		}

		now := time.Now().UTC()
		if existing == nil {
			return data.SetMark(tx, &model.HintMark{HintID: hintID, UserID: owner, State: state, Until: until, At: now})
		}
		existing.State = state
		existing.Until = until
		existing.At = now
		return data.SetMark(tx, existing)
	})
}

// Action is a user-facing shortcut for Mark.
type Action string

const (
	ActionAck    Action = "ack"
	ActionSnooze Action = "snooze"
	ActionReopen Action = "reopen"
)

const defaultSnoozeDays = 7

// Act applies a named action to one hint; note explains it in the history.
func Act(d *sql.DB, who *access.Principal, hintID int64, action Action, days int, note string) error {
	switch action {
	case ActionAck:
		return Mark(d, who, hintID, enums.HintAcknowledged, nil, note)
	case ActionSnooze:
		if days < 1 {
			days = defaultSnoozeDays
		}
		until := time.Now().UTC().AddDate(0, 0, days)
		return Mark(d, who, hintID, enums.HintSnoozed, &until, note)
	default:
		return Mark(d, who, hintID, enums.HintOpen, nil, note)
	}
}

// Prune deletes hints resolved more than resolvedRetention ago.
func Prune(d *sql.DB) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		return data.PruneResolved(tx, time.Now().UTC().Add(-resolvedRetention))
	})
}
