package util_test

import (
	"testing"

	"andon/internal/services/util"
)

func TestSlugBasic(t *testing.T) {
	if got := util.Slug("Mein Board!", "item"); got != "mein-board" {
		t.Fatalf("expected mein-board, got %q", got)
	}
}

func TestSlugUmlauts(t *testing.T) {
	if got := util.Slug("Büro Köln", "item"); got != "buro-koln" {
		t.Fatalf("expected buro-koln, got %q", got)
	}
}

func TestSlugEmptyFallsBack(t *testing.T) {
	if got := util.Slug("!!!", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
}

func TestUniqueAppendsSuffix(t *testing.T) {
	taken := map[string]bool{"board": true, "board-2": true}
	if got := util.Unique("board", taken); got != "board-3" {
		t.Fatalf("expected board-3, got %q", got)
	}
	if got := util.Unique("fresh", taken); got != "fresh" {
		t.Fatalf("expected unchanged fresh, got %q", got)
	}
}
