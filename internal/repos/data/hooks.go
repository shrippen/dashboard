package data

import (
	"time"

	"dashboard/internal/db"
	"dashboard/internal/model"
)

// AddHookEvent stores one pushed event.
func AddHookEvent(q db.Queryer, e *model.HookEvent) error {
	res, err := q.Exec("INSERT INTO hook_events (connection_id, event, subject, at) VALUES (?,?,?,?)",
		e.ConnectionID, e.Event, e.Subject, db.TimeStr(e.At))
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

// HookEvents returns a connection's events since a time, oldest first.
func HookEvents(q db.Queryer, connID int64, since time.Time) ([]*model.HookEvent, error) {
	rows, err := q.Query("SELECT id, connection_id, event, subject, at FROM hook_events WHERE connection_id = ? AND at >= ? ORDER BY at, id",
		connID, db.TimeStr(since))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.HookEvent
	for rows.Next() {
		var e model.HookEvent
		var at string
		if err := rows.Scan(&e.ID, &e.ConnectionID, &e.Event, &e.Subject, &at); err != nil {
			return nil, err
		}
		if e.At, err = db.ParseTime(at); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// PruneHookEvents deletes events older than a time.
func PruneHookEvents(q db.Queryer, before time.Time) error {
	_, err := q.Exec("DELETE FROM hook_events WHERE at < ?", db.TimeStr(before))
	return err
}
