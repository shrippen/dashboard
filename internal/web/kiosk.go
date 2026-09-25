package web

// Wall display (?kiosk): no app chrome, boards rotate, dimmed at night.
//
//	/boards/3?kiosk&every=60&dim=22-7
//	  → after 60 s /boards/<next visible board>?kiosk&every=60&dim=22-7
//	  → 22:00–07:00 the page is dimmed

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"dashboard/internal/services/boards"
)

const (
	kioskMinEvery = 10   // seconds; faster rotation is unreadable
	kioskMaxEvery = 3600 // seconds
)

// dimPattern is "from-to" in full hours, e.g. "22-7".
var dimPattern = regexp.MustCompile(`^([01]?\d|2[0-3])-([01]?\d|2[0-3])$`)

// kioskView is what the board template needs for the wall display.
type kioskView struct {
	On    bool
	Every int    // seconds until the next board, 0 = stay
	Next  string // next board URL, "" = stay
	Dim   string // "22-7" or ""
}

// kioskOf reads the wall display settings and picks the next board.
func kioskOf(r *http.Request, visible []boards.BoardRef, current int64) kioskView {
	q := r.URL.Query()
	if !q.Has("kiosk") {
		return kioskView{}
	}

	k := kioskView{On: true}
	if dimPattern.MatchString(q.Get("dim")) {
		k.Dim = q.Get("dim")
	}
	every, err := strconv.Atoi(q.Get("every"))
	if err != nil || every <= 0 || len(visible) < 2 {
		return k
	}
	k.Every = min(max(every, kioskMinEvery), kioskMaxEvery)

	next := visible[0].ID
	for i, b := range visible {
		if b.ID == current && i+1 < len(visible) {
			next = visible[i+1].ID
		}
	}
	keep := url.Values{"every": {strconv.Itoa(k.Every)}}
	if k.Dim != "" {
		keep.Set("dim", k.Dim)
	}
	k.Next = boardPath(next) + "?kiosk&" + keep.Encode()
	return k
}
