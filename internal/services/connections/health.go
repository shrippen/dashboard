package connections

// Connection health and token hygiene:
//
//	health   last success, failure rate, mean response time (7 days)
//	hygiene  token expiry date and a daily fetch budget, set by managers

import (
	"database/sql"
	"errors"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/repos/content"
	data "dashboard/internal/repos/data"
	"dashboard/internal/services/access"
	"dashboard/internal/services/audit"
	"dashboard/internal/services/svcdata"
)

// healthDays is the window of the health view.
const healthDays = 7

// ErrBadDate means the expiry is no "2026-12-31" date.
var ErrBadDate = errors.New("connection.bad_date")

// Health is one connection's recent fetch record.
type Health struct {
	Fetches   int
	FailPct   int
	AvgMS     int
	LastOK    time.Time // zero = none in the window
	LastError string
	Today     int // fetches today, for the budget
}

func healthOf(q db.Queryer, connID int64, now time.Time) (Health, error) {
	since := now.AddDate(0, 0, -healthDays+1).Format(time.DateOnly)
	raw, err := data.HealthSince(q, connID, since)
	if err != nil {
		return Health{}, err
	}
	today, err := data.Fetches(q, connID, now.Format(time.DateOnly))
	if err != nil {
		return Health{}, err
	}

	h := Health{Fetches: raw.OK + raw.Fail, LastError: raw.LastError, Today: today}
	if h.Fetches > 0 {
		h.FailPct = raw.Fail * 100 / h.Fetches
		h.AvgMS = int(raw.MsSum / int64(h.Fetches))
	}
	if raw.LastOKAt != "" {
		h.LastOK, _ = db.ParseTime(raw.LastOKAt)
	}
	return h, nil
}

// SetHygiene stores a token's expiry ("" = unknown) and the daily fetch
// budget (0 = unlimited). Requires MANAGE.
func SetHygiene(d *sql.DB, who *access.Principal, connID int64, expires string, budget int) error {
	if expires != "" {
		if _, err := time.Parse(time.DateOnly, expires); err != nil {
			return ErrBadDate
		}
	}
	defer svcdata.Forget(connID) // the budget applies from the next fetch

	return db.WithTx(d, func(tx *sql.Tx) error {
		conn, err := content.Connection(tx, connID)
		if err != nil {
			return err
		}
		if conn == nil {
			return ErrNotFound
		}
		granted, err := rightOf(tx, who, conn)
		if err != nil {
			return err
		}
		if err := access.Need(granted, enums.RightManage); err != nil {
			return err
		}
		conn.SecretExpires, conn.DailyBudget = expires, max(budget, 0)
		if err := content.UpdateConnection(tx, conn); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "connection.hygiene", conn.Name, "", nil)
	})
}

// DayState is one day of a connection's health strip.
type DayState struct {
	Day      string
	OK, Fail int
}

// Strip is one connection's recent days, oldest first, with one entry
// per calendar day (days without fetches have zero counts).
type Strip struct {
	Name, Service string
	FailPct       int
	Days          []DayState
}

// Strips returns the day-by-day health of every connection who can see,
// over the last days (the connection health tile).
func Strips(d *sql.DB, who *access.Principal, days int, now time.Time) ([]Strip, error) {
	list, err := Listing(d, who, enums.RightView)
	if err != nil {
		return nil, err
	}
	first := now.AddDate(0, 0, -days+1)
	since := first.Format(time.DateOnly)

	var out []Strip
	err = db.WithRead(d, func(tx *sql.Tx) error {
		for _, c := range list {
			raw, err := data.DaysSince(tx, c.ID, since)
			if err != nil {
				return err
			}
			byDay := map[string]data.ConnDay{}
			ok, fail := 0, 0
			for _, r := range raw {
				byDay[r.Day] = r
				ok, fail = ok+r.OK, fail+r.Fail
			}

			strip := Strip{Name: c.Name, Service: string(c.Service)}
			if ok+fail > 0 {
				strip.FailPct = fail * 100 / (ok + fail)
			}
			for i := range days {
				day := first.AddDate(0, 0, i).Format(time.DateOnly)
				r := byDay[day]
				strip.Days = append(strip.Days, DayState{Day: day, OK: r.OK, Fail: r.Fail})
			}
			out = append(out, strip)
		}
		return nil
	})
	return out, err
}
