package data

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
)

// ── Hint history ──

// AddHintEvent appends one entry to a hint's history.
func AddHintEvent(q db.Queryer, e *model.HintEvent) error {
	res, err := q.Exec("INSERT INTO hint_events (hint_id, kind, user_id, note, at) VALUES (?,?,?,?,?)",
		e.HintID, e.Kind, e.UserID, e.Note, db.TimeStr(e.At))
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

// HintEvents returns a hint's history, newest first.
func HintEvents(q db.Queryer, hintID int64, limit int) ([]*model.HintEvent, error) {
	rows, err := q.Query("SELECT id, hint_id, kind, user_id, note, at FROM hint_events WHERE hint_id = ? ORDER BY at DESC, id DESC LIMIT ?",
		hintID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.HintEvent
	for rows.Next() {
		var e model.HintEvent
		var userID sql.NullInt64
		var at string
		if err := rows.Scan(&e.ID, &e.HintID, &e.Kind, &userID, &e.Note, &at); err != nil {
			return nil, err
		}
		if userID.Valid {
			e.UserID = &userID.Int64
		}
		if e.At, err = db.ParseTime(at); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// EventCounts counts events of one kind per hint since a time.
func EventCounts(q db.Queryer, hintIDs []int64, kind enums.HintEvent, since time.Time) (map[int64]int, error) {
	out := map[int64]int{}
	if len(hintIDs) == 0 {
		return out, nil
	}
	args := []any{kind, db.TimeStr(since)}
	for _, id := range hintIDs {
		args = append(args, id)
	}
	rows, err := q.Query("SELECT hint_id, COUNT(*) FROM hint_events WHERE kind = ? AND at >= ? AND hint_id IN ("+
		placeholders(len(hintIDs))+") GROUP BY hint_id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ── Assignment ──

// HintWorks returns the assignments of the given hints.
func HintWorks(q db.Queryer, hintIDs []int64) (map[int64]*model.HintWork, error) {
	out := map[int64]*model.HintWork{}
	if len(hintIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(hintIDs))
	for i, id := range hintIDs {
		args[i] = id
	}
	rows, err := q.Query("SELECT hint_id, assignee_id, state, at FROM hint_work WHERE hint_id IN ("+placeholders(len(hintIDs))+")", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var w model.HintWork
		var assignee sql.NullInt64
		var at string
		if err := rows.Scan(&w.HintID, &assignee, &w.State, &at); err != nil {
			return nil, err
		}
		if assignee.Valid {
			w.AssigneeID = &assignee.Int64
		}
		if w.At, err = db.ParseTime(at); err != nil {
			return nil, err
		}
		out[w.HintID] = &w
	}
	return out, rows.Err()
}

// SetHintWork creates or replaces a hint's assignment.
func SetHintWork(q db.Queryer, w *model.HintWork) error {
	_, err := q.Exec(`INSERT INTO hint_work (hint_id, assignee_id, state, at) VALUES (?,?,?,?)
		ON CONFLICT (hint_id) DO UPDATE SET assignee_id = excluded.assignee_id, state = excluded.state, at = excluded.at`,
		w.HintID, w.AssigneeID, w.State, db.TimeStr(w.At))
	return err
}

// ── Repeated pushes ──

// LastSent returns when a hint was last pushed to a user; zero if never.
func LastSent(q db.Queryer, userID, hintID int64) (time.Time, error) {
	var at string
	err := q.QueryRow("SELECT sent_at FROM notify_log WHERE user_id = ? AND hint_id = ?", userID, hintID).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	return db.ParseTime(at)
}

// TouchSent records a push, keeping one row per user and hint.
func TouchSent(q db.Queryer, userID, hintID int64) error {
	_, err := q.Exec(`INSERT INTO notify_log (user_id, hint_id, sent_at) VALUES (?,?,?)
		ON CONFLICT (user_id, hint_id) DO UPDATE SET sent_at = excluded.sent_at`,
		userID, hintID, db.TimeStr(time.Now().UTC()))
	return err
}
