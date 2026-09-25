package web

import (
	"encoding/json"
	"net/http"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/services/access"
	"dashboard/internal/services/boards"
	"dashboard/internal/services/calendar"
	"dashboard/internal/services/hints"
)

const embedHintLimit = 10

// RegisterAPIRoutes wires the token API (read scope), the iCal feed and
// the embeds (embed or read scope), e.g. an iframe in another dashboard:
//
//	GET /api/summary     hint counts + visible boards (JSON)
//	GET /api/hints       open hints (JSON)
//	GET /calendar.ics    deadlines + due hints (iCal)
//	GET /embed/hints     hint list without app chrome
//	GET /embed/b/{id}    board without app chrome
func (d Deps) RegisterAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/summary", d.handleAPISummary)
	mux.HandleFunc("GET /api/hints", d.handleAPIHints)
	mux.HandleFunc("GET /calendar.ics", d.handleCalendar)
	mux.HandleFunc("GET /embed/hints", d.handleEmbedHints)
	mux.HandleFunc("GET /embed/b/{id}", d.handleEmbedBoard)
}

// apiPrincipal resolves a token of one of scopes; nil means unauthorized.
func (d Deps) apiPrincipal(r *http.Request, scopes ...enums.TokenScope) *access.Principal {
	for _, scope := range scopes {
		who, err := d.tokenPrincipal(r, scope)
		if err == nil && who != nil {
			return who
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func (d Deps) handleAPISummary(w http.ResponseWriter, r *http.Request) {
	who := d.apiPrincipal(r, enums.TokenRead)
	if who == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	counts, err := hints.Summary(d.DB, who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	visible, err := boards.Visible(d.DB, who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	levels := map[string]int{}
	for level, n := range counts {
		levels[level.Key()] = n
	}
	type boardRef struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	refs := make([]boardRef, 0, len(visible))
	for _, b := range visible {
		refs = append(refs, boardRef{ID: b.ID, Name: b.Name})
	}
	writeJSON(w, map[string]any{"hints": levels, "boards": refs})
}

type apiHint struct {
	ID       int64  `json:"id"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Why      string `json:"why"`
	Due      string `json:"due,omitempty"`
	URL      string `json:"url,omitempty"`
}

func (d Deps) handleAPIHints(w http.ResponseWriter, r *http.Request) {
	who := d.apiPrincipal(r, enums.TokenRead)
	if who == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	open, err := hints.Active(d.DB, who, enums.SeverityInfo, nil, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]apiHint, 0, len(open))
	for _, h := range open {
		out = append(out, apiHint{ID: h.ID, Rule: h.Rule, Severity: h.Severity.Key(), Title: h.Title, Why: h.Why, Due: h.Due, URL: h.ActionURL})
	}
	writeJSON(w, out)
}

func (d Deps) handleCalendar(w http.ResponseWriter, r *http.Request) {
	who := d.apiPrincipal(r, enums.TokenRead)
	if who == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	feed, err := calendar.Feed(d.DB, who, time.Now())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	_, _ = w.Write([]byte(feed))
}

func (d Deps) handleEmbedHints(w http.ResponseWriter, r *http.Request) {
	who := d.apiPrincipal(r, enums.TokenEmbed, enums.TokenRead)
	if who == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	found, err := hints.Active(d.DB, who, enums.SeverityInfo, nil, embedHintLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = d.Page(w, Ctx{Who: who, Locale: who.Locale}, "hints", http.StatusOK, map[string]any{"Hints": found, "Embed": true})
}

func (d Deps) handleEmbedBoard(w http.ResponseWriter, r *http.Request) {
	who := d.apiPrincipal(r, enums.TokenEmbed, enums.TokenRead)
	if who == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	d.renderBoard(w, r, Ctx{Who: who, Locale: who.Locale}, r.URL.Query().Get("token"))
}
