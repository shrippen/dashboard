package widgets

import (
	"testing"

	"andon/internal/sources"
)

func TestMonitorsViewSumsUp(t *testing.T) {
	var mons []sources.KumaMonitor
	for range 10 {
		mons = append(mons, sources.KumaMonitor{Name: "ok", Status: sources.KumaUp, MS: 100, CertDays: -1})
	}
	mons = append(mons,
		sources.KumaMonitor{Name: "NAS", Status: sources.KumaDown, CertDays: -1},
		sources.KumaMonitor{Name: "Shop", Status: sources.KumaUp, MS: 300, CertDays: 12},
		sources.KumaMonitor{Name: "A", Status: sources.KumaPending, CertDays: -1},
		sources.KumaMonitor{Name: "B", Status: sources.KumaDown, CertDays: -1},
		sources.KumaMonitor{Name: "C", Status: sources.KumaDown, CertDays: -1},
		sources.KumaMonitor{Name: "D", Status: sources.KumaDown, CertDays: -1},
	)
	v := monitorsView(nil, map[string]any{"data": &sources.KumaDataset{Monitors: mons}}, ViewCtx{})
	cells := v["Cells"].([]StripCell)
	problems := v["Problems"].([]MonitorRow)
	if v["Up"] != 11 || v["Total"] != 16 || cells[0].State != "bad" || len(problems) != 4 || v["More"] != 1 ||
		v["CertName"] != "Shop" || v["CertDays"] != 12 || v["AvgMS"] != 1300.0/11 {
		t.Fatalf("view: %+v", v)
	}
}
