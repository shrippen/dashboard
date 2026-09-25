package web

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/services/assist"
	"dashboard/internal/services/hints"
	historysvc "dashboard/internal/services/history"
)

// RegisterHintRoutes wires the hints overview page, snooze/ack/reopen
// actions and the workflow (history, notes, assignment, work state).
func (d Deps) RegisterHintRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /hints", d.handleHintsPage)
	mux.HandleFunc("POST /hints/{id}/ack", d.handleHintAct(hints.ActionAck))
	mux.HandleFunc("POST /hints/{id}/snooze", d.handleHintAct(hints.ActionSnooze))
	mux.HandleFunc("POST /hints/{id}/reopen", d.handleHintAct(hints.ActionReopen))
	mux.HandleFunc("GET /hints/{id}/detail", d.handleHintDetail)
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
	_ = d.Page(w, ctx, "hints", http.StatusOK, map[string]any{"Hints": found, "ByValue": byValue, "Noisy": noisy})
}

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
		http.Redirect(w, r, "/hints", http.StatusSeeOther)
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
