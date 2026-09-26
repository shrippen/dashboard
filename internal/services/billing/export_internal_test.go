package billing

import (
	"strings"
	"testing"

	"andon/internal/metrics"
	"andon/internal/sources"
)

// TestTripRows: one round trip per client day, priced at the km rate.
func TestTripRows(t *testing.T) {
	lat, lon := 52.52, 13.405
	geo := &sources.DawarichDataset{
		Areas: []sources.DawarichArea{{ID: 1, Name: "Zuhause", Lat: 52.52, Lon: 13.405}, {ID: 2, Name: "Acme", Lat: 52.39, Lon: 13.06}},
		Visits: []sources.DawarichVisit{
			{ID: 1, Start: "2026-03-02T09:00:00Z", End: "2026-03-02T15:00:00Z", AreaID: 2},
			{ID: 2, Start: "2025-12-30T09:00:00Z", End: "2025-12-30T12:00:00Z", AreaID: 2, Lat: &lat, Lon: &lon},
		},
	}
	mapping := map[string]metrics.AreaMapping{"Zuhause": {Home: true}, "Acme": {CustomerID: 7}}
	kimai := &sources.KimaiDataset{Customers: []sources.KimaiCustomer{{ID: 7, Name: "Acme GmbH"}}}

	rows := tripRows(geo, mapping, kimai, 2026, 0.30)
	if len(rows) != 2 || rows[1][0] != "2026-03-02" || rows[1][1] != "Acme GmbH" || !strings.Contains(rows[1][3], ",") {
		t.Fatalf("rows: %v", rows)
	}
}
