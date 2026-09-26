// Package reports builds printable evidence from recorded history.
//
//	ISP  one month of speed measurements and WAN outages per space, as a
//	     table and as CSV for a complaint to the provider (§ 57 TKG)
package reports

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	"andon/internal/services/access"
	"andon/internal/services/connections"
	"andon/internal/services/history"
	"andon/internal/services/svcdata"
	"andon/internal/sources"
)

const (
	// ISPDays is the report window.
	ISPDays = 30
	// ISPShare: a day counts as too slow below this share of the booked speed.
	ISPShare = 0.9
	csvComma = ';'
	utf8BOM  = "\ufeff"
)

// ISP is one space's report.
type ISP struct {
	Space                string
	ExpectDown, ExpectUp float64
	metrics.SpeedReport
}

// ISPReports lists a report per usable Speedtest Tracker connection.
func ISPReports(ctx context.Context, d *sql.DB, who *access.Principal) ([]ISP, error) {
	views, err := connections.Listing(d, who, enums.RightUse)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	var out []ISP
	for _, v := range views {
		if v.Service != enums.ServiceSpeedtest {
			continue
		}
		conn, err := connections.ByID(d, v.ID)
		if err != nil || conn == nil {
			continue
		}
		uid := who.UserID
		res, err := svcdata.Get(ctx, d, sources.DataKey(enums.ServiceSpeedtest), nil, conn, &uid, svcdata.Stored)
		if err != nil {
			continue
		}
		st, ok := res.Data.(*sources.SpeedtestDataset)
		if !ok {
			continue
		}
		h, err := history.Load(d, conn.SpaceID, 0, now)
		if err != nil {
			return nil, err
		}
		space := ""
		if ref, ok := who.Spaces[conn.SpaceID]; ok {
			space = ref.Name
		}
		out = append(out, ISP{Space: space, ExpectDown: st.ExpectDown, ExpectUp: st.ExpectUp,
			SpeedReport: metrics.SpeedDays(h, st.ExpectDown, ISPShare, now, ISPDays)})
	}
	return out, nil
}

// num: 243.5 → "243,5".
func num(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1)
}

// ISPCSV renders the reports as one CSV (semicolon, decimal comma, BOM).
func ISPCSV(reports []ISP) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(utf8BOM)
	w := csv.NewWriter(&buf)
	w.Comma = csvComma
	rows := [][]string{{"Bereich", "Datum", "Download Mbit/s", "Upload Mbit/s", "Gebucht Download", "Unter " + fmt.Sprintf("%.0f %%", ISPShare*100)}}
	for _, r := range reports {
		for _, day := range r.Days {
			below := ""
			if day.Below {
				below = "ja"
			}
			rows = append(rows, []string{r.Space, day.Day.Format(time.DateOnly), num(day.Down), num(day.Up), num(r.ExpectDown), below})
		}
		for _, o := range r.Outages {
			end := ""
			if !o.End.IsZero() {
				end = o.End.Format(time.DateTime)
			}
			rows = append(rows, []string{r.Space, o.Start.Format(time.DateTime), "WAN-Ausfall " + o.Gateway, "bis " + end, "", ""})
		}
	}
	if err := w.WriteAll(rows); err != nil {
		return nil, err
	}
	return buf.Bytes(), w.Error()
}
