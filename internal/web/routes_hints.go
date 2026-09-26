package web

import (
	"cmp"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"andon/internal/enums"
	"andon/internal/services/assist"
	"andon/internal/services/hints"
	historysvc "andon/internal/services/history"
	"andon/internal/services/onboarding"
)

// RegisterHintRoutes wires the hints overview page, snooze/ack/reopen
// actions and the workflow (history, notes, assignment, work state).
func (d Deps) RegisterHintRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /hints", d.handleHintsPage)
	mux.HandleFunc("POST /hints/{id}/ack", d.handleHintAct(hints.ActionAck))
	mux.HandleFunc("POST /hints/{id}/snooze", d.handleHintAct(hints.ActionSnooze))
	mux.HandleFunc("POST /hints/{id}/reopen", d.handleHintAct(hints.ActionReopen))
	mux.HandleFunc("GET /hints/{id}/detail", d.handleHintDetail)
	mux.HandleFunc("GET /connections/{id}/hints", d.handleConnHints)
	mux.HandleFunc("POST /hints/{id}/advice", d.handleHintAdvice)
	mux.HandleFunc("POST /hints/{id}/note", d.handleHintWorkflow(hintNote))
	mux.HandleFunc("POST /hints/{id}/assign", d.handleHintWorkflow(hintAssign))
	mux.HandleFunc("POST /hints/{id}/work", d.handleHintWorkflow(hintWork))
}

const defaultSnoozeDays = 7

// hintStep is one workflow form.
type hintStep string

const (
	hintNote   hintStep = "note"
	hintAssign hintStep = "assign"
	hintWork   hintStep = "work"
)

var workStates = []enums.WorkState{enums.WorkOpen, enums.WorkProgress, enums.WorkDone}

// sortByValue orders the hints page by the money a hint names.
const sortByValue = "value"

func (d Deps) handleHintsPage(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	_ = onboarding.Visit(d.DB, ctx.Who, "hints") // a checklist step: seen the hints once
	found, err := hints.Active(d.DB, ctx.Who, enums.SeverityInfo, nil, 0)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	byValue := r.URL.Query().Get("sort") == sortByValue
	if byValue {
		hints.ByValue(found)
	}
	noisy, err := hints.NoisyRules(d.DB, ctx.Who, time.Now().UTC())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	filter := hintFilter{Level: r.URL.Query().Get("level"), Source: r.URL.Query().Get("source"), ByValue: byValue}
	_ = d.Page(w, ctx, "hints", http.StatusOK, map[string]any{
		"Groups": groupHints(filter.apply(found)), "Levels": levelCounts(found, filter), "Sources": sourceCounts(found, filter),
		"Filter": filter, "Total": len(found), "ByValue": byValue, "Noisy": noisy,
	})
}

// hintsShown is how many hints of one rule stay open; the rest fold away.
const hintsShown = 5

// hintLevels are the severity filters, highest first.
var hintLevels = []struct {
	Key string
	Min enums.Severity
}{{"critical", enums.SeverityCritical}, {"warn", enums.SeverityWarn}, {"info", enums.SeverityInfo}}

// hintFilter narrows the hints page: ?level=critical&source=kimai.
type hintFilter struct {
	Level, Source string
	ByValue       bool
}

// levelOf names a severity band: "critical", "warn" or "info".
func levelOf(s enums.Severity) string {
	for _, l := range hintLevels {
		if s >= l.Min {
			return l.Key
		}
	}
	return hintLevels[len(hintLevels)-1].Key
}

// hintAxis names the filter a count leaves out, so a chip shows what
// picking it would give.
type hintAxis int

const (
	axisNone hintAxis = iota
	axisLevel
	axisSource
)

func (f hintFilter) keep(v hints.View, skip hintAxis) bool {
	if skip != axisLevel && f.Level != "" && levelOf(v.Severity) != f.Level {
		return false
	}
	return skip == axisSource || f.Source == "" || slices.Contains(v.Sources, f.Source)
}

func (f hintFilter) apply(all []hints.View) []hints.View {
	var out []hints.View
	for _, v := range all {
		if f.keep(v, axisNone) {
			out = append(out, v)
		}
	}
	return out
}

// Link is the page URL with one filter changed ("" clears it).
func (f hintFilter) Link(key, value string) string {
	q := url.Values{}
	level, source := f.Level, f.Source
	if key == "level" {
		level = value
	} else {
		source = value
	}
	if level != "" {
		q.Set("level", level)
	}
	if source != "" {
		q.Set("source", source)
	}
	if f.ByValue {
		q.Set("sort", sortByValue)
	}
	if len(q) == 0 {
		return "/hints"
	}
	return "/hints?" + q.Encode()
}

// hintCount is one filter chip: its key and how many hints it would show.
type hintCount struct {
	Key string
	N   int
}

// levelCounts counts per severity under the current source filter.
func levelCounts(all []hints.View, f hintFilter) []hintCount {
	n := map[string]int{}
	for _, v := range all {
		if f.keep(v, axisLevel) {
			n[levelOf(v.Severity)]++
		}
	}
	var out []hintCount
	for _, l := range hintLevels {
		if n[l.Key] > 0 {
			out = append(out, hintCount{l.Key, n[l.Key]})
		}
	}
	return out
}

// sourceCounts counts per service under the current level filter, most first.
func sourceCounts(all []hints.View, f hintFilter) []hintCount {
	n := map[string]int{}
	for _, v := range all {
		if !f.keep(v, axisSource) {
			continue
		}
		for _, s := range v.Sources {
			n[s]++
		}
	}
	out := make([]hintCount, 0, len(n))
	for k, c := range n {
		out = append(out, hintCount{k, c})
	}
	slices.SortFunc(out, func(a, b hintCount) int { return cmp.Or(b.N-a.N, strings.Compare(a.Key, b.Key)) })
	return out
}

// hintGroup is one rule's hints; Rest folds away below the first few.
type hintGroup struct {
	Rule     string
	Severity enums.Severity
	Shown    []hints.View
	Rest     []hints.View
}

// groupHints keeps the given order and gathers each rule's hints where the
// rule first appears: 40 "invoice missing" hints become one group.
func groupHints(views []hints.View) []hintGroup {
	var out []hintGroup
	at := map[string]int{}
	for _, v := range views {
		i, ok := at[v.Rule]
		if !ok {
			i = len(out)
			at[v.Rule] = i
			out = append(out, hintGroup{Rule: v.Rule, Severity: v.Severity})
		}
		g := &out[i]
		if len(g.Shown) < hintsShown {
			g.Shown = append(g.Shown, v)
			continue
		}
		g.Rest = append(g.Rest, v)
	}
	return out
}

// Count is the group's size.
func (g hintGroup) Count() int { return len(g.Shown) + len(g.Rest) }

// hintRequest parses the path id and checks CSRF for posts.
func (d Deps) hintRequest(w http.ResponseWriter, r *http.Request) (Ctx, int64, bool) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return ctx, 0, false
	}
	if r.Method == http.MethodPost {
		if err := d.checkCSRF(r, ctx.CSRF); err != nil {
			d.handleAuthError(w, r, err)
			return ctx, 0, false
		}
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return ctx, 0, false
	}
	return ctx, id, true
}

func (d Deps) handleHintAct(action hints.Action) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, id, ok := d.hintRequest(w, r)
		if !ok {
			return
		}
		days, _ := strconv.Atoi(r.FormValue("days"))
		if days <= 0 {
			days = defaultSnoozeDays
		}
		if err := hints.Act(d.DB, ctx.Who, id, action, days, r.FormValue("note")); err != nil {
			d.handleBoardError(w, r, err)
			return
		}
		http.Redirect(w, r, backTo(r, "/hints"), http.StatusSeeOther)
	}
}

// handleHintDetail renders history, assignment and note forms of one
// hint (loaded when the reader opens them).
func (d Deps) handleHintDetail(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := d.hintRequest(w, r)
	if !ok {
		return
	}
	history, err := hints.History(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	people, err := hints.Assignees(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	before, err := historysvc.Before(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	_ = d.Page(w, ctx, "hint_detail", http.StatusOK, map[string]any{"ID": id, "History": history, "People": people, "States": workStates,
		"Assist": assist.Enabled(), "Before": before})
}

// handleHintAdvice answers "Was tun?" for one hint (htmx fragment).
func (d Deps) handleHintAdvice(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := d.hintRequest(w, r)
	if !ok {
		return
	}
	text, err := assist.Advise(r.Context(), d.DB, ctx.Who, id)
	if errors.Is(err, hints.ErrNotFound) || errors.Is(err, hints.ErrDenied) {
		d.handleBoardError(w, r, err)
		return
	}
	values := map[string]any{"Text": text}
	if err != nil {
		values["Error"] = errKey(err)
	}
	_ = d.Page(w, ctx, "hint_advice", http.StatusOK, values)
}

func (d Deps) handleHintWorkflow(step hintStep) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, id, ok := d.hintRequest(w, r)
		if !ok {
			return
		}
		note := r.FormValue("note")
		var err error
		switch step {
		case hintAssign:
			assignee, _ := strconv.ParseInt(r.FormValue("assignee"), 10, 64)
			err = hints.Assign(d.DB, ctx.Who, id, assignee, note)
		case hintWork:
			err = hints.SetWork(d.DB, ctx.Who, id, enums.WorkState(r.FormValue("state")), note)
		default:
			err = hints.AddNote(d.DB, ctx.Who, id, note)
		}
		if err != nil {
			d.handleBoardError(w, r, err)
			return
		}
		http.Redirect(w, r, "/hints#hint-"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	}
}

// handleConnHints renders the open hints of one connection as a small
// fragment (the popover behind a link tile's hint badge).
func (d Deps) handleConnHints(w http.ResponseWriter, r *http.Request) {
	ctx, id, ok := d.hintRequest(w, r)
	if !ok {
		return
	}
	found, err := hints.ForConnection(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	_ = d.Page(w, ctx, "hint_pop", http.StatusOK, map[string]any{"Hints": found})
}

// backTo returns the page a form was sent from when the form asks for it
// (back=referer) and the referer is this site; fallback otherwise. Only
// the path is kept, so the redirect can never leave the site.
func backTo(r *http.Request, fallback string) string {
	if r.FormValue("back") != "referer" {
		return fallback
	}
	ref, err := url.Parse(r.Referer())
	if err != nil || ref.Host != r.Host || !strings.HasPrefix(ref.Path, "/") || strings.HasPrefix(ref.Path, "//") {
		return fallback
	}
	if ref.RawQuery != "" {
		return ref.Path + "?" + ref.RawQuery
	}
	return ref.Path
}
