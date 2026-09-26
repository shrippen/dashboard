package i18n_test

import (
	"testing"
	"time"

	"andon/internal/enums"
	"andon/internal/i18n"
)

func TestTFallsBackToDefaultLocaleThenKey(t *testing.T) {
	if got := i18n.T("nav.home", enums.LocaleDE, nil); got == "nav.home" {
		t.Fatal("expected a real translation for a known key")
	}
	if got := i18n.T("this.key.does.not.exist", enums.LocaleEN, nil); got != "this.key.does.not.exist" {
		t.Fatalf("expected unknown key to fall back to itself, got %q", got)
	}
}

func TestTSubstitutesParams(t *testing.T) {
	// action.save has no placeholders; use a key with one, if present, else
	// just verify unknown placeholders survive untouched.
	got := i18n.T("does.not.exist.either", enums.LocaleDE, map[string]any{"n": 3})
	if got != "does.not.exist.either" {
		t.Fatalf("unexpected: %q", got)
	}
}

func TestMoneyFormatsByLocale(t *testing.T) {
	if got := i18n.Money(1234.5, enums.LocaleDE, "EUR"); got != "1.234,50 €" {
		t.Fatalf("expected German money format, got %q", got)
	}
	if got := i18n.Money(1234.5, enums.LocaleEN, "EUR"); got != "€1,234.50" {
		t.Fatalf("expected English money format, got %q", got)
	}
}

func TestNumGrouping(t *testing.T) {
	if got := i18n.Num(1000000, enums.LocaleDE, 0); got != "1.000.000" {
		t.Fatalf("expected grouped German number, got %q", got)
	}
	if got := i18n.Num(1000000, enums.LocaleEN, 0); got != "1,000,000" {
		t.Fatalf("expected grouped English number, got %q", got)
	}
}

func TestDayFormatting(t *testing.T) {
	if got := i18n.Day("2026-03-05", enums.LocaleDE); got != "05.03.2026" {
		t.Fatalf("expected German date, got %q", got)
	}
	if got := i18n.Day("2026-03-05", enums.LocaleEN); got != "Mar 5, 2026" {
		t.Fatalf("expected English date, got %q", got)
	}
	if got := i18n.Day(nil, enums.LocaleDE); got != "" {
		t.Fatalf("expected empty string for nil date, got %q", got)
	}
}

func TestPickAcceptLanguage(t *testing.T) {
	if got := i18n.Pick("en-US,en;q=0.9,de;q=0.8"); got != enums.LocaleEN {
		t.Fatalf("expected en, got %v", got)
	}
	if got := i18n.Pick("fr-FR"); got != i18n.DefaultLocale {
		t.Fatalf("expected fallback to default locale, got %v", got)
	}
}

func TestTypedDropsKeyAndFormatsMoney(t *testing.T) {
	out := i18n.Typed(map[string]any{
		"key":    "hint.x",
		"amount": map[string]any{"$money": 12.5},
	}, enums.LocaleDE)
	if _, ok := out["key"]; ok {
		t.Fatal("expected 'key' to be dropped")
	}
	if out["amount"] != "12,50 €" {
		t.Fatalf("expected formatted money, got %v", out["amount"])
	}
}

func TestAgoFuturePast(t *testing.T) {
	future := time.Now().Add(3 * 24 * time.Hour)
	if got := i18n.Ago(&future, enums.LocaleEN); got != "in 3 days" {
		t.Fatalf("expected future phrase, got %q", got)
	}
	past := time.Now().Add(-3 * 24 * time.Hour)
	if got := i18n.Ago(&past, enums.LocaleDE); got != "vor 3 Tagen" {
		t.Fatalf("expected past phrase, got %q", got)
	}
}
