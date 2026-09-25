package billing

// Year package for the tax advisor: one ZIP with CSV files (semicolon,
// decimal comma, UTF-8 with BOM – opens directly in German Excel).
//
//	rechnungen.csv  zahlungen.csv  ausgaben.csv  stunden.csv  ust.csv

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/services/access"
	auditsvc "dashboard/internal/services/audit"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/sources"
)

const (
	csvComma       = ';'
	utf8BOM        = "\ufeff"
	monthsInYear   = 12
	minutesPerHour = 60
)

// ErrNoData means the space has neither Kimai nor Invoice Ninja data.
var ErrNoData = errors.New("billing.no_data")

// money: 1234.5 → "1234,50".
func money(v float64) string {
	return strings.Replace(strconv.FormatFloat(v, 'f', 2, 64), ".", ",", 1)
}

func inYear(isoDay string, year int) bool {
	return strings.HasPrefix(isoDay, strconv.Itoa(year)+"-")
}

// Export builds the year package of one space.
func Export(ctx context.Context, d *sql.DB, who *access.Principal, spaceID int64, year int, ip string) (string, []byte, error) {
	ref, ok := who.Spaces[spaceID]
	if !ok || access.SpaceRight(who, &ref) < enums.RightEdit {
		return "", nil, access.ErrDenied
	}
	var conns []*model.Connection
	var settings map[string]any
	err := db.WithTx(d, func(tx *sql.Tx) error {
		var err error
		if conns, err = content.Connections(tx, []int64{spaceID}); err != nil {
			return err
		}
		sp, err := content.Space(tx, spaceID)
		if sp != nil {
			settings = sp.Settings
		}
		return err
	})
	if err != nil {
		return "", nil, err
	}

	var kimai *sources.KimaiDataset
	var ninja *sources.NinjaDataset
	uid := who.UserID
	for _, c := range conns {
		switch enums.ServiceType(c.Service) {
		case enums.ServiceKimai, enums.ServiceInvoiceNinja:
		default:
			continue
		}
		res, err := svcdata.Get(ctx, d, sources.DataKey(enums.ServiceType(c.Service)), nil, c, &uid, svcdata.Force)
		if err != nil {
			continue
		}
		switch data := res.Data.(type) {
		case *sources.KimaiDataset:
			kimai = data
		case *sources.NinjaDataset:
			ninja = data
		}
	}
	if kimai == nil && ninja == nil {
		return "", nil, ErrNoData
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string][][]string{}
	if ninja != nil {
		files["rechnungen.csv"], files["zahlungen.csv"], files["ausgaben.csv"] = invoiceRows(ninja, year), paymentRows(ninja, year), expenseRows(ninja, year)
		files["ust.csv"] = vatRows(ninja, year, metrics.TaxVATMethod(settings))
	}
	if kimai != nil {
		files["stunden.csv"] = hourRows(kimai, year)
	}
	for _, name := range []string{"rechnungen.csv", "zahlungen.csv", "ausgaben.csv", "ust.csv", "stunden.csv"} {
		rows, ok := files[name]
		if !ok {
			continue
		}
		if err := writeCSV(zw, name, rows); err != nil {
			return "", nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return "", nil, err
	}
	filename := fmt.Sprintf("steuer-%d.zip", year)
	return filename, buf.Bytes(), auditsvc.Log(d, &who.UserID, "billing.export", filename, ip, nil)
}

func writeCSV(zw *zip.Writer, name string, rows [][]string) error {
	f, err := zw.Create(name)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(utf8BOM)); err != nil {
		return err
	}
	w := csv.NewWriter(f)
	w.Comma = csvComma
	if err := w.WriteAll(rows); err != nil {
		return err
	}
	return w.Error()
}

func clientNames(ninja *sources.NinjaDataset) map[int64]string {
	out := map[int64]string{}
	for _, c := range ninja.Clients {
		out[c.ID] = c.Name
	}
	return out
}

func invoiceRows(ninja *sources.NinjaDataset, year int) [][]string {
	names := clientNames(ninja)
	rows := [][]string{{"Nummer", "Datum", "Kunde", "Netto", "USt", "Brutto", "Offen", "Status"}}
	for _, i := range metrics.NinjaCounted(ninja) {
		if inYear(i.Date, year) {
			rows = append(rows, []string{i.Number, i.Date, names[i.ClientID], money(i.Net), money(i.Taxes), money(i.Amount), money(i.Balance), i.Status})
		}
	}
	return rows
}

func paymentRows(ninja *sources.NinjaDataset, year int) [][]string {
	names := clientNames(ninja)
	rows := [][]string{{"Datum", "Kunde", "Betrag"}}
	for _, p := range ninja.Payments {
		if inYear(p.Date, year) {
			rows = append(rows, []string{p.Date, names[p.ClientID], money(p.Amount)})
		}
	}
	return rows
}

func expenseRows(ninja *sources.NinjaDataset, year int) [][]string {
	vendors := map[string]string{}
	for _, v := range ninja.Vendors {
		vendors[v.Key] = v.Name
	}
	rows := [][]string{{"Datum", "Lieferant", "Beschreibung", "Brutto", "Vorsteuer"}}
	for _, e := range ninja.Expenses {
		if inYear(e.Date, year) {
			rows = append(rows, []string{e.Date, vendors[e.VendorKey], e.Notes, money(e.Amount), money(e.Tax)})
		}
	}
	return rows
}

func vatRows(ninja *sources.NinjaDataset, year int, method string) [][]string {
	rows := [][]string{{"Monat", "Umsatzsteuer", "Vorsteuer", "Zahllast"}}
	for m := 1; m <= monthsInYear; m++ {
		start := time.Date(year, time.Month(m), 1, 0, 0, 0, 0, time.UTC)
		end := start.AddDate(0, 1, -1)
		out, in := metrics.NinjaOutputVAT(ninja, start, end, method), metrics.NinjaInputVAT(ninja, start, end)
		rows = append(rows, []string{start.Format("2006-01"), money(out), money(in), money(out - in)})
	}
	return rows
}

func hourRows(kimai *sources.KimaiDataset, year int) [][]string {
	customers := metrics.KimaiCustomerNames(kimai)
	projects := map[int64]string{}
	for _, p := range kimai.Projects {
		projects[p.ID] = p.Name
	}
	rows := [][]string{{"Datum", "Kunde", "Projekt", "Tätigkeit", "Stunden", "Betrag", "Abrechenbar", "Exportiert"}}
	yesNo := map[bool]string{true: "ja", false: "nein"}
	for _, s := range kimai.Timesheets {
		day, ok := metrics.ParseDay(s.Begin)
		if !ok || day.Year() != year {
			continue
		}
		rows = append(rows, []string{day.Format("2006-01-02"), customers[s.CustomerID], projects[s.ProjectID], s.Activity,
			money(float64(s.Minutes) / minutesPerHour), money(s.Rate), yesNo[s.Billable], yesNo[s.Exported]})
	}
	return rows
}
