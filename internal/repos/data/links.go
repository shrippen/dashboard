package data

import (
	"time"

	"andon/internal/db"
)

// ── Clicks ──

// CountClick adds one click of a user on a link tile.
func CountClick(q db.Queryer, userID, widgetID int64) error {
	_, err := q.Exec(`INSERT INTO link_clicks (user_id, widget_id, count, last_at) VALUES (?,?,1,?)
		ON CONFLICT (user_id, widget_id) DO UPDATE SET count = count + 1, last_at = excluded.last_at`,
		userID, widgetID, db.TimeStr(time.Now().UTC()))
	return err
}

// Clicks returns a user's click counts per widget.
func Clicks(q db.Queryer, userID int64) (map[int64]int, error) {
	rows, err := q.Query("SELECT widget_id, count FROM link_clicks WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
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

// LastClicks returns when each link tile was last clicked by anyone.
func LastClicks(q db.Queryer) (map[int64]time.Time, error) {
	rows, err := q.Query("SELECT widget_id, MAX(last_at) FROM link_clicks GROUP BY widget_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var id int64
		var at string
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		if t, err := db.ParseTime(at); err == nil {
			out[id] = t
		}
	}
	return out, rows.Err()
}

// ── Status history ──

// DayStatus is one tile's checks of one day.
type DayStatus struct {
	Day      string
	OK, Fail int
	MsSum    int
}

// Check is one status check's outcome.
type Check struct {
	Up bool
	MS int
}

// RecordStatus adds one check result to today's row.
func RecordStatus(q db.Queryer, widgetID int64, day string, check Check) error {
	ok, fail, ms := 0, 1, check.MS
	if check.Up {
		ok, fail = 1, 0
	}
	_, err := q.Exec(`INSERT INTO link_status (widget_id, day, ok, fail, ms_sum) VALUES (?,?,?,?,?)
		ON CONFLICT (widget_id, day) DO UPDATE SET ok = ok + excluded.ok, fail = fail + excluded.fail, ms_sum = ms_sum + excluded.ms_sum`,
		widgetID, day, ok, fail, ms)
	return err
}

// StatusSince returns a tile's days from a day on, oldest first.
func StatusSince(q db.Queryer, widgetID int64, since string) ([]DayStatus, error) {
	rows, err := q.Query("SELECT day, ok, fail, ms_sum FROM link_status WHERE widget_id = ? AND day >= ? ORDER BY day", widgetID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DayStatus
	for rows.Next() {
		var s DayStatus
		if err := rows.Scan(&s.Day, &s.OK, &s.Fail, &s.MsSum); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// PruneStatus drops rows before a day.
func PruneStatus(q db.Queryer, before string) error {
	_, err := q.Exec("DELETE FROM link_status WHERE day < ?", before)
	return err
}
