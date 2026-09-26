package sources_test

import (
	"testing"
	"time"

	"andon/internal/sources"
)

func TestContractTerms(t *testing.T) {
	today := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	day := func(s string) time.Time { d, _ := time.Parse(time.DateOnly, s); return d }

	// Fields win over text.
	c := sources.ContractTerms(map[string]string{"end": "2026-12-31", "notice": "3 Monate"}, "Laufzeit bis 31.12.2030", today)
	if !c.Deadline.Equal(day("2026-09-30")) {
		t.Fatalf("fields: %+v", c)
	}

	// Text only, notice in weeks.
	c = sources.ContractTerms(nil, "Der Vertrag endet am 15.01.2027. Kündigungsfrist: 6 Wochen zum Vertragsende.", today)
	if !c.Deadline.Equal(day("2026-12-04")) {
		t.Fatalf("text: %+v", c)
	}

	// Passed deadline of an auto-renewing contract rolls into the next term.
	c = sources.ContractTerms(nil, "Laufzeit bis 31.10.2026, Kündigungsfrist 3 Monate, danach verlängert sich der Vertrag um 12 Monate.", today)
	if !c.End.Equal(day("2027-10-31")) || !c.Deadline.Equal(day("2027-07-31")) || !c.RenewsAutomatic {
		t.Fatalf("renewal: %+v", c)
	}

	if c := sources.ContractTerms(nil, "Kein Datum", today); !c.Deadline.IsZero() {
		t.Fatalf("no end: %+v", c)
	}
}
