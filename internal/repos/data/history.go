package data

// Key figure history, version register and event timeline of a space.
// owner 0 is the shared data; a user id the data of personal credentials.

import (
	"strings"
	"time"

	"dashboard/internal/db"
)

// SamplePoint is one stored daily value.
type SamplePoint struct {
	Day   string
	Value float64
}

// PutSamples stores today's values; a later run on the same day wins.
func PutSamples(q db.Queryer, spaceID, owner int64, day string, values map[string]float64) error {
	for key, v := range values {
		if _, err := q.Exec(`INSERT INTO samples (space_id, owner, key, day, value) VALUES (?,?,?,?,?)
			ON CONFLICT (space_id, owner, key, day) DO UPDATE SET value = excluded.value`, spaceID, owner, key, day, v); err != nil {
			return err
		}
	}
	return nil
}

// SamplesSince returns every series of a space and owner from a day on,
// oldest first.
func SamplesSince(q db.Queryer, spaceID, owner int64, since string) (map[string][]SamplePoint, error) {
	rows, err := q.Query("SELECT key, day, value FROM samples WHERE space_id = ? AND owner = ? AND day >= ? ORDER BY day",
		spaceID, owner, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]SamplePoint{}
	for rows.Next() {
		var key string
		var p SamplePoint
		if err := rows.Scan(&key, &p.Day, &p.Value); err != nil {
			return nil, err
		}
		out[key] = append(out[key], p)
	}
	return out, rows.Err()
}

// Versions returns the last seen version per subject.
func Versions(q db.Queryer, spaceID, owner int64) (map[string]string, error) {
	rows, err := q.Query("SELECT subject, version FROM versions WHERE space_id = ? AND owner = ?", spaceID, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var s, v string
		if err := rows.Scan(&s, &v); err != nil {
			return nil, err
		}
		out[s] = v
	}
	return out, rows.Err()
}

// SetVersion stores the current version of a subject.
func SetVersion(q db.Queryer, spaceID, owner int64, subject, version string, at time.Time) error {
	_, err := q.Exec(`INSERT INTO versions (space_id, owner, subject, version, seen_at) VALUES (?,?,?,?,?)
		ON CONFLICT (space_id, owner, subject) DO UPDATE SET version = excluded.version, seen_at = excluded.seen_at`,
		spaceID, owner, subject, version, db.TimeStr(at))
	return err
}

// Event is one timeline entry.
type Event struct {
	At                    time.Time
	Kind, Subject, Detail string
	Owner                 int64
}

// AddEvent appends a timeline entry.
func AddEvent(q db.Queryer, spaceID int64, e Event) error {
	_, err := q.Exec("INSERT INTO events (space_id, owner, at, kind, subject, detail) VALUES (?,?,?,?,?,?)",
		spaceID, e.Owner, db.TimeStr(e.At), e.Kind, e.Subject, e.Detail)
	return err
}

// EventsSince returns the events of the given spaces for the shared data
// and the given owner, newest first.
func EventsSince(q db.Queryer, spaceIDs []int64, owner int64, since time.Time, limit int) ([]Event, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	query, args := eventQuery(spaceIDs, owner, since, limit)
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var at string
		if err := rows.Scan(&at, &e.Kind, &e.Subject, &e.Detail, &e.Owner); err != nil {
			return nil, err
		}
		if e.At, err = db.ParseTime(at); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func eventQuery(spaceIDs []int64, owner int64, since time.Time, limit int) (string, []any) {
	marks := strings.TrimSuffix(strings.Repeat("?,", len(spaceIDs)), ",")
	args := make([]any, 0, len(spaceIDs)+3)
	for _, id := range spaceIDs {
		args = append(args, id)
	}
	args = append(args, owner, db.TimeStr(since), limit)
	return "SELECT at, kind, subject, detail, owner FROM events WHERE space_id IN (" + marks +
		") AND owner IN (0, ?) AND at >= ? ORDER BY at DESC LIMIT ?", args
}

// PruneHistory drops samples and events older than the cutoffs.
func PruneHistory(q db.Queryer, samplesBefore string, eventsBefore time.Time) error {
	if _, err := q.Exec("DELETE FROM samples WHERE day < ?", samplesBefore); err != nil {
		return err
	}
	_, err := q.Exec("DELETE FROM events WHERE at < ?", db.TimeStr(eventsBefore))
	return err
}
