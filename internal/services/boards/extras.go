package boards

// Board conveniences:
//
//	Undo        restore the revision before the last change
//	QuickLink   paste a URL → link tile with page title and favicon
//	Click       count a click on a link tile (frequently used row)
//	Palette     boards, link tiles and pages for the command palette

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"
	"strings"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/data"
	"dashboard/internal/services/access"
	"dashboard/internal/services/widgetlib"
	"dashboard/internal/sources"
	"dashboard/internal/widgets"
)

const (
	frequentMin   = 3 // clicks before a tile counts as frequent
	frequentShown = 6
	linkType      = "link"
	faviconIcon   = "favicon"
)

// ErrNothingToUndo means the board has no earlier revision.
var ErrNothingToUndo = errors.New("board.nothing_to_undo")

// ErrBadURL means a pasted link is not an http(s) URL.
var ErrBadURL = errors.New("board.bad_url")

// Undo restores the revision before the latest one.
func Undo(d *sql.DB, who *access.Principal, boardID int64) error {
	revs, err := History(d, who, boardID)
	if err != nil {
		return err
	}
	if len(revs) < 2 {
		return ErrNothingToUndo
	}
	return Restore(d, who, boardID, revs[1].ID)
}

// QuickLink creates a link tile from a URL in a section and returns the
// new placement id.
func QuickLink(ctx context.Context, d *sql.DB, who *access.Principal, sectionID int64, version int, rawURL string) (int64, error) {
	rawURL = strings.TrimSpace(rawURL)
	if !strings.HasPrefix(rawURL, "https://") && !strings.HasPrefix(rawURL, "http://") {
		rawURL = "https://" + rawURL
	}
	if strings.ContainsAny(rawURL, " \t\n") {
		return 0, ErrBadURL
	}
	var spaceID int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		section, err := content.Section(tx, sectionID)
		if err != nil || section == nil {
			return orNotFound(err)
		}
		board, err := load(tx, who, section.BoardID, enums.RightEdit)
		if err != nil {
			return err
		}
		spaceID = board.SpaceID
		return nil
	})
	if err != nil {
		return 0, err
	}

	title := sources.PageTitle(ctx, rawURL)
	config := map[string]any{"url": rawURL, "icon": faviconIcon, "status": string(widgets.StatusHTTP), "target": string(enums.LinkNewTab)}
	widgetID, err := widgetlib.Create(d, who, spaceID, linkType, title, config, nil, nil)
	if err != nil {
		return 0, err
	}
	return Place(d, who, sectionID, widgetID, version)
}

// Click counts one click of who on a placed link tile.
func Click(d *sql.DB, who *access.Principal, placementID int64) error {
	w, err := PlacedWidget(d, who, placementID)
	if err != nil {
		return err
	}
	if w.Type != linkType {
		return nil
	}
	return data.CountClick(d, who.UserID, w.ID)
}

// frequent picks the viewer's most clicked link tiles of a board.
func frequent(q db.Queryer, who *access.Principal, sections []SectionView) ([]Tile, error) {
	clicks, err := data.Clicks(q, who.UserID)
	if err != nil || len(clicks) == 0 {
		return nil, err
	}
	var out []Tile
	for _, s := range sections {
		for _, t := range s.Tiles {
			if t.Type == linkType && !t.Hidden && clicks[t.WidgetID] >= frequentMin {
				out = append(out, t)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return clicks[out[i].WidgetID] > clicks[out[j].WidgetID] })
	if len(out) > frequentShown {
		out = out[:frequentShown]
	}
	return out, nil
}

// PaletteKind groups palette entries.
type PaletteKind string

const (
	PaletteBoard PaletteKind = "board"
	PaletteLink  PaletteKind = "link"
	PalettePage  PaletteKind = "page"
)

// PaletteItem is one entry of the command palette.
type PaletteItem struct {
	Kind   PaletteKind `json:"kind"`
	Title  string      `json:"title"`
	URL    string      `json:"url"`
	Detail string      `json:"detail"`
}

// Palette lists the boards and link tiles who can see.
func Palette(d *sql.DB, who *access.Principal) ([]PaletteItem, error) {
	refs, err := Visible(d, who)
	if err != nil {
		return nil, err
	}
	var out []PaletteItem
	seen := map[string]bool{}
	for _, ref := range refs {
		out = append(out, PaletteItem{Kind: PaletteBoard, Title: ref.Name, URL: "/boards/" + strconv.FormatInt(ref.ID, 10), Detail: ref.Space.Name})
		view, err := View(d, who, ref.ID)
		if err != nil {
			continue
		}
		for _, s := range view.Sections {
			for _, t := range s.Tiles {
				link, ok := t.Config.(widgets.LinkConfig)
				if !ok || t.Hidden || seen[link.URL] {
					continue
				}
				seen[link.URL] = true
				out = append(out, PaletteItem{Kind: PaletteLink, Title: t.Title, URL: link.URL, Detail: ref.Name})
			}
		}
	}
	return out, nil
}
