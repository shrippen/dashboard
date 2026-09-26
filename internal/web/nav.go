package web

import (
	"andon/internal/enums"
	"andon/internal/services/access"
	"andon/internal/services/boards"
	"andon/internal/services/hints"
)

// addNav fills what the app header shows on every page: the viewer's
// boards and the open hint count with its highest severity. It only
// decorates the page, so a failed lookup leaves the entry out.
func (d Deps) addNav(data map[string]any, who *access.Principal) {
	if _, ok := data["CurBoard"]; !ok {
		data["CurBoard"] = int64(0)
	}
	if _, ok := data["NavBoards"]; !ok {
		if list, err := boards.Visible(d.DB, who); err == nil {
			data["NavBoards"] = list
		}
	}

	counts, err := hints.Summary(d.DB, who)
	if err != nil {
		return
	}
	total, level := 0, enums.Severity(0)
	for sev, n := range counts {
		total += n
		if n > 0 && sev > level {
			level = sev
		}
	}
	data["HintOpen"], data["HintLevel"] = total, int(level)
}

// sectionRun is one section on the main grid, or a run of neighbouring
// flowing sections set in newspaper columns (see boards.SpanFlow).
type sectionRun struct {
	Flow     bool
	Sections []boards.SectionView
}

// mainRuns groups the main-area sections for the board template:
//
//	[full] [flow flow flow] [third] [flow]  →  4 runs, the flows grouped
func mainRuns(sections []boards.SectionView) []sectionRun {
	var out []sectionRun
	for _, s := range sections {
		if s.Area == areaSide {
			continue
		}

		flow := s.Span == boards.SpanFlow
		if flow && len(out) > 0 && out[len(out)-1].Flow {
			last := &out[len(out)-1]
			last.Sections = append(last.Sections, s)
			continue
		}
		out = append(out, sectionRun{Flow: flow, Sections: []boards.SectionView{s}})
	}
	return out
}

const areaSide = "side"
