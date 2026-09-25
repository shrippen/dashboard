// Package data provides database access for the source cache, metric
// points, hints and notifications.
package data

import (
	"database/sql"
	"errors"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/model"
)

// ── Cache ──

// Cache returns one cache entry by key, or nil.
func Cache(q db.Queryer, key string) (*model.CacheEntry, error) {
	var c model.CacheEntry
	var fetchedAt string
	var okAt, data, errText sql.NullString

	err := q.QueryRow(
		"SELECT key, source, fetched_at, ok_at, data, error FROM cache WHERE key = ?", key,
	).Scan(&c.Key, &c.Source, &fetchedAt, &okAt, &data, &errText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.FetchedAt, err = db.ParseTime(fetchedAt); err != nil {
		return nil, err
	}
	if okAt.Valid {
		t, err := db.ParseTime(okAt.String)
		if err != nil {
			return nil, err
		}
		c.OkAt = &t
	}
	c.Error = errText.String
	if data.Valid {
		c.Data = map[string]any{}
		if err := db.FromJSON(data.String, &c.Data); err != nil {
			return nil, err
		}
	}
	return &c, nil
}

// PutCache inserts or replaces one cache entry.
func PutCache(q db.Queryer, c *model.CacheEntry) error {
	var dataText any
	if c.Data != nil {
		text, err := db.ToJSON(c.Data)
		if err != nil {
			return err
		}
		dataText = text
	}
	_, err := q.Exec(`INSERT INTO cache (key, source, fetched_at, ok_at, data, error)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET
			source=excluded.source, fetched_at=excluded.fetched_at, ok_at=excluded.ok_at,
			data=excluded.data, error=excluded.error`,
		c.Key, c.Source, db.TimeStr(c.FetchedAt), db.NullTimeStr(c.OkAt), dataText, nullStr(c.Error),
	)
	return err
}

// PruneCache deletes cache entries fetched before olderThan.
func PruneCache(q db.Queryer, olderThan time.Time) error {
	_, err := q.Exec("DELETE FROM cache WHERE fetched_at < ?", db.TimeStr(olderThan))
	return err
}

// ── Metrics ──

// PutPoint inserts or updates one daily metric value.
func PutPoint(q db.Queryer, scope, metric, day string, value float64) error {
	_, err := q.Exec(`INSERT INTO metric_points (scope, metric, day, value) VALUES (?,?,?,?)
		ON CONFLICT(scope, metric, day) DO UPDATE SET value = excluded.value`,
		scope, metric, day, value,
	)
	return err
}

// Points returns the metric points of one scope/metric on or after since,
// ordered by day.
func Points(q db.Queryer, scope, metric, since string) ([]*model.MetricPoint, error) {
	rows, err := q.Query(
		"SELECT id, scope, metric, day, value FROM metric_points WHERE scope = ? AND metric = ? AND day >= ? ORDER BY day",
		scope, metric, since,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.MetricPoint
	for rows.Next() {
		var p model.MetricPoint
		if err := rows.Scan(&p.ID, &p.Scope, &p.Metric, &p.Day, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// PrunePoints deletes metric points before beforeDay.
func PrunePoints(q db.Queryer, beforeDay string) error {
	_, err := q.Exec("DELETE FROM metric_points WHERE day < ?", beforeDay)
	return err
}

// ── Hints ──

const hintCols = `id, space_id, user_id, fingerprint, rule, severity, message, params,
	action_url, action_label, due, sources, connection_id, first_seen, last_seen, resolved_at`

func scanHint(row interface{ Scan(...any) error }) (*model.Hint, error) {
	var h model.Hint
	var userID, connID sql.NullInt64
	var actionURL, actionLabel, due sql.NullString
	var params, sources, firstSeen, lastSeen string
	var resolvedAt sql.NullString

	err := row.Scan(&h.ID, &h.SpaceID, &userID, &h.Fingerprint, &h.Rule, &h.Severity, &h.Message,
		&params, &actionURL, &actionLabel, &due, &sources, &connID, &firstSeen, &lastSeen, &resolvedAt)
	if err != nil {
		return nil, err
	}
	if userID.Valid {
		h.UserID = &userID.Int64
	}
	if connID.Valid {
		h.ConnectionID = &connID.Int64
	}
	h.ActionURL, h.ActionLabel, h.Due = actionURL.String, actionLabel.String, due.String
	h.Params = map[string]any{}
	if err := db.FromJSON(params, &h.Params); err != nil {
		return nil, err
	}
	if err := db.FromJSON(sources, &h.Sources); err != nil {
		return nil, err
	}
	if h.FirstSeen, err = db.ParseTime(firstSeen); err != nil {
		return nil, err
	}
	if h.LastSeen, err = db.ParseTime(lastSeen); err != nil {
		return nil, err
	}
	if resolvedAt.Valid {
		t, err := db.ParseTime(resolvedAt.String)
		if err != nil {
			return nil, err
		}
		h.ResolvedAt = &t
	}
	return &h, nil
}

// HintsIn returns the active hints of the given spaces visible to userID:
// shared ones (user_id NULL) plus the user's own.
func HintsIn(q db.Queryer, spaceIDs []int64, userID int64) ([]*model.Hint, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	placeholders, args := "", []any{}
	for i, id := range spaceIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, id)
	}
	args = append(args, userID)
	rows, err := q.Query(
		"SELECT "+hintCols+" FROM hints WHERE space_id IN ("+placeholders+
			") AND resolved_at IS NULL AND (user_id IS NULL OR user_id = ?)",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHints(rows)
}

func scanHints(rows *sql.Rows) ([]*model.Hint, error) {
	var out []*model.Hint
	for rows.Next() {
		h, err := scanHint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// HintByID returns a hint by id, or nil.
func HintByID(q db.Queryer, hintID int64) (*model.Hint, error) {
	row := q.QueryRow("SELECT "+hintCols+" FROM hints WHERE id = ?", hintID)
	h, err := scanHint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return h, err
}

// HintsOfScope returns the active hints of given rules in one space/owner
// scope, optionally narrowed to one connection.
func HintsOfScope(q db.Queryer, spaceID int64, userID *int64, rules []string, connID *int64) ([]*model.Hint, error) {
	if len(rules) == 0 {
		return nil, nil
	}
	placeholders, args := "", []any{spaceID}
	for i, r := range rules {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, r)
	}
	query := "SELECT " + hintCols + " FROM hints WHERE space_id = ? AND rule IN (" + placeholders + ") AND resolved_at IS NULL"
	if userID == nil {
		query += " AND user_id IS NULL"
	} else {
		query += " AND user_id = ?"
		args = append(args, *userID)
	}
	if connID != nil {
		query += " AND connection_id = ?"
		args = append(args, *connID)
	}

	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHints(rows)
}

// HintByPrint looks up a hint by its fingerprint within a space/owner scope.
func HintByPrint(q db.Queryer, spaceID int64, userID *int64, fingerprint string) (*model.Hint, error) {
	query := "SELECT " + hintCols + " FROM hints WHERE space_id = ? AND fingerprint = ?"
	args := []any{spaceID, fingerprint}
	if userID == nil {
		query += " AND user_id IS NULL"
	} else {
		query += " AND user_id = ?"
		args = append(args, *userID)
	}
	row := q.QueryRow(query, args...)
	h, err := scanHint(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return h, err
}

// AddHint inserts a new hint.
func AddHint(q db.Queryer, h *model.Hint) error {
	params, err := db.ToJSON(orEmpty(h.Params))
	if err != nil {
		return err
	}
	sources, err := db.ToJSON(orEmptySlice(h.Sources))
	if err != nil {
		return err
	}
	res, err := q.Exec(`INSERT INTO hints
		(space_id, user_id, fingerprint, rule, severity, message, params, action_url,
		 action_label, due, sources, connection_id, first_seen, last_seen, resolved_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		h.SpaceID, h.UserID, h.Fingerprint, h.Rule, h.Severity, h.Message, params,
		nullStr(h.ActionURL), nullStr(h.ActionLabel), nullStr(h.Due), sources, h.ConnectionID,
		db.TimeStr(h.FirstSeen), db.TimeStr(h.LastSeen), db.NullTimeStr(h.ResolvedAt),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	h.ID = id
	return nil
}

// UpdateHint writes back every mutable field (re-fired hints refresh
// last_seen and params; resolved hints set resolved_at).
func UpdateHint(q db.Queryer, h *model.Hint) error {
	params, err := db.ToJSON(orEmpty(h.Params))
	if err != nil {
		return err
	}
	sources, err := db.ToJSON(orEmptySlice(h.Sources))
	if err != nil {
		return err
	}
	_, err = q.Exec(`UPDATE hints SET
		severity=?, message=?, params=?, action_url=?, action_label=?, due=?, sources=?,
		last_seen=?, resolved_at=? WHERE id=?`,
		h.Severity, h.Message, params, nullStr(h.ActionURL), nullStr(h.ActionLabel),
		nullStr(h.Due), sources, db.TimeStr(h.LastSeen), db.NullTimeStr(h.ResolvedAt), h.ID,
	)
	return err
}

// RemoveHint deletes a hint.
func RemoveHint(q db.Queryer, hintID int64) error {
	_, err := q.Exec("DELETE FROM hints WHERE id = ?", hintID)
	return err
}

// Marks returns the hint marks visible to userID (team-wide plus their own)
// for the given hints.
func Marks(q db.Queryer, hintIDs []int64, userID int64) ([]*model.HintMark, error) {
	if len(hintIDs) == 0 {
		return nil, nil
	}
	placeholders, args := "", []any{}
	for i, id := range hintIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, id)
	}
	args = append(args, userID)
	rows, err := q.Query(
		"SELECT id, hint_id, user_id, state, until, at FROM hint_marks WHERE hint_id IN ("+
			placeholders+") AND (user_id IS NULL OR user_id = ?)",
		args...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMarks(rows)
}

func scanMarks(rows *sql.Rows) ([]*model.HintMark, error) {
	var out []*model.HintMark
	for rows.Next() {
		var m model.HintMark
		var userID sql.NullInt64
		var until sql.NullString
		var at string
		if err := rows.Scan(&m.ID, &m.HintID, &userID, &m.State, &until, &at); err != nil {
			return nil, err
		}
		if userID.Valid {
			m.UserID = &userID.Int64
		}
		if until.Valid {
			t, err := db.ParseTime(until.String)
			if err != nil {
				return nil, err
			}
			m.Until = &t
		}
		var err error
		if m.At, err = db.ParseTime(at); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// Mark returns the mark of one hint for userID (nil = team-wide), or nil.
func Mark(q db.Queryer, hintID int64, userID *int64) (*model.HintMark, error) {
	query := "SELECT id, hint_id, user_id, state, until, at FROM hint_marks WHERE hint_id = ?"
	args := []any{hintID}
	if userID == nil {
		query += " AND user_id IS NULL"
	} else {
		query += " AND user_id = ?"
		args = append(args, *userID)
	}
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	marks, err := scanMarks(rows)
	if err != nil {
		return nil, err
	}
	if len(marks) == 0 {
		return nil, nil
	}
	return marks[0], nil
}

// SetMark inserts or replaces a hint's mark for userID (nil = team-wide).
func SetMark(q db.Queryer, m *model.HintMark) error {
	existing, err := Mark(q, m.HintID, m.UserID)
	if err != nil {
		return err
	}
	if existing == nil {
		_, err := q.Exec(
			"INSERT INTO hint_marks (hint_id, user_id, state, until, at) VALUES (?,?,?,?,?)",
			m.HintID, m.UserID, m.State, db.NullTimeStr(m.Until), db.TimeStr(m.At),
		)
		return err
	}
	_, err = q.Exec(
		"UPDATE hint_marks SET state=?, until=?, at=? WHERE id=?",
		m.State, db.NullTimeStr(m.Until), db.TimeStr(m.At), existing.ID,
	)
	return err
}

// RemoveMark deletes one mark by id.
func RemoveMark(q db.Queryer, markID int64) error {
	_, err := q.Exec("DELETE FROM hint_marks WHERE id = ?", markID)
	return err
}

// DropMarks deletes every mark of one hint (it is about to be re-evaluated).
func DropMarks(q db.Queryer, hintID int64) error {
	_, err := q.Exec("DELETE FROM hint_marks WHERE hint_id = ?", hintID)
	return err
}

// PruneResolved deletes hints resolved before olderThan.
func PruneResolved(q db.Queryer, olderThan time.Time) error {
	_, err := q.Exec("DELETE FROM hints WHERE resolved_at < ?", db.TimeStr(olderThan))
	return err
}

// ── Notifications ──

// Channels returns one user's notification channels.
func Channels(q db.Queryer, userID int64) ([]*model.NotifyChannel, error) {
	rows, err := q.Query(
		"SELECT id, user_id, name, url_enc, min_severity, enabled FROM notify_channels WHERE user_id = ?",
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.NotifyChannel
	for rows.Next() {
		var c model.NotifyChannel
		if err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.URLEnc, &c.MinSeverity, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// Channel returns one notification channel by id, or nil.
func Channel(q db.Queryer, channelID int64) (*model.NotifyChannel, error) {
	var c model.NotifyChannel
	err := q.QueryRow(
		"SELECT id, user_id, name, url_enc, min_severity, enabled FROM notify_channels WHERE id = ?",
		channelID,
	).Scan(&c.ID, &c.UserID, &c.Name, &c.URLEnc, &c.MinSeverity, &c.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}

// AddChannel inserts a new notification channel.
func AddChannel(q db.Queryer, c *model.NotifyChannel) error {
	res, err := q.Exec(
		"INSERT INTO notify_channels (user_id, name, url_enc, min_severity, enabled) VALUES (?,?,?,?,?)",
		c.UserID, c.Name, c.URLEnc, c.MinSeverity, c.Enabled,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	c.ID = id
	return nil
}

// UpdateChannel writes back a channel's mutable fields.
func UpdateChannel(q db.Queryer, c *model.NotifyChannel) error {
	_, err := q.Exec(
		"UPDATE notify_channels SET name=?, min_severity=?, enabled=? WHERE id=?",
		c.Name, c.MinSeverity, c.Enabled, c.ID,
	)
	return err
}

// UpdateChannelSecret rewrites a channel's encrypted URL (key rotation
// only; AddChannel/UpdateChannel cover the normal CRUD paths).
func UpdateChannelSecret(q db.Queryer, channelID int64, urlEnc []byte) error {
	_, err := q.Exec("UPDATE notify_channels SET url_enc=? WHERE id=?", urlEnc, channelID)
	return err
}

// RemoveChannel deletes a notification channel.
func RemoveChannel(q db.Queryer, channelID int64) error {
	_, err := q.Exec("DELETE FROM notify_channels WHERE id = ?", channelID)
	return err
}

// WasSent reports whether a hint was already sent to a user (no duplicate
// pushes across restarts).
func WasSent(q db.Queryer, userID, hintID int64) (bool, error) {
	var id int64
	err := q.QueryRow(
		"SELECT id FROM notify_log WHERE user_id = ? AND hint_id = ?", userID, hintID,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// LogSent records that a hint was sent to a user.
func LogSent(q db.Queryer, userID, hintID int64) error {
	_, err := q.Exec(
		"INSERT INTO notify_log (user_id, hint_id, sent_at) VALUES (?,?,?)",
		userID, hintID, db.TimeStr(time.Now().UTC()),
	)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func orEmptySlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
