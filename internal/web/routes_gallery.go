package web

// The gallery ("Kachel hinzufügen") replaces the type list and the library
// picker: one page for placing an existing tile again or setting up a new
// one, each card with a lazy preview.
//
//	/widgets/new ──► gallery ──┬─ set up: /widgets/new?type=… (form, preview among neighbours)
//	                           └─ existing: dialog ─┬─ show here too: POST …/place
//	                                                └─ as a copy: POST /widgets/{id}/copy
//
// Previews: /widget-sample/{type} renders a type with the caller's
// connection (live) or demo data; /widgets/{id}/preview a library widget.

import (
	"net/http"
	"slices"
	"strconv"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"dashboard/internal/enums"
	"dashboard/internal/i18n"
	"dashboard/internal/services/access"
	"dashboard/internal/services/boards"
	"dashboard/internal/services/connections"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/services/widgetlib"
	"dashboard/internal/widgets"
)

// galleryCard is one type to set up. ConnID is the caller's connection of
// its service (live preview), 0 means demo data.
type galleryCard struct {
	Kind       widgets.WidgetType
	Name, Desc string
	ConnID     int64
}

// galleryTopic is one topic with its types, A–Z by displayed name.
type galleryTopic struct {
	Topic widgets.Topic
	Cards []galleryCard
}

// galleryTile is a set-up widget that can be placed again.
type galleryTile struct {
	widgetlib.Ref
	Name string
}

// galleryTarget names the section a tile goes to ("Start › Daten").
type galleryTarget struct {
	Board, Section string
	Size           enums.TileSize
}

func (d Deps) handleGallery(w http.ResponseWriter, ctx Ctx, target widgetTarget, spaces []access.SpaceRef) {
	conns, err := connections.Listing(d.DB, ctx.Who, enums.RightUse)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	connOf := map[enums.ServiceType]int64{}
	for _, c := range conns {
		if _, seen := connOf[c.Service]; !seen {
			connOf[c.Service] = c.ID
		}
	}

	sorter := collate.New(language.Make(string(ctx.Locale)), collate.IgnoreCase)
	tr := func(key string) string { return i18n.T(key, ctx.Locale, nil) }
	name := func(key string) string { return tr("wtype." + key + ".name") }
	byTopic := map[widgets.Topic][]galleryCard{}
	for _, kind := range widgets.AllTypes() {
		card := galleryCard{Kind: kind, Name: name(kind.Key), Desc: tr("wtype." + kind.Key + ".desc"),
			ConnID: connOf[kind.Service]}
		topic := widgets.TopicOf(kind.Key)
		byTopic[topic] = append(byTopic[topic], card)
	}
	var topics []galleryTopic
	for _, topic := range widgets.Topics {
		cards := byTopic[topic]
		slices.SortFunc(cards, func(a, b galleryCard) int { return sorter.CompareString(a.Name, b.Name) })
		if len(cards) > 0 {
			topics = append(topics, galleryTopic{Topic: topic, Cards: cards})
		}
	}

	// Set-up tiles only make sense when placing into a section.
	var tiles, links []galleryTile
	var dest galleryTarget
	if target.Place {
		lib, err := widgetlib.Library(d.DB, ctx.Who)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, ref := range lib {
			tile := galleryTile{Ref: ref, Name: ref.Title}
			if tile.Name == "" {
				tile.Name = name(ref.Type)
			}
			// Links outnumber everything else; they get their own group.
			if ref.Type == linkType {
				links = append(links, tile)
				continue
			}
			tiles = append(tiles, tile)
		}
		byName := func(a, b galleryTile) int { return sorter.CompareString(a.Name, b.Name) }
		slices.SortFunc(tiles, byName)
		slices.SortFunc(links, byName)
		dest = d.targetNames(ctx, target)
	}

	_ = d.Page(w, ctx, "widget_gallery", http.StatusOK, map[string]any{
		"Topics": topics, "Tiles": tiles, "Links": links, "Reuse": len(tiles)+len(links) > 0, "Target": target, "Dest": dest, "Spaces": spaces,
	})
}

// targetNames looks up the board and section a new tile goes to; empty
// names if the board can't be read (the page still works without them).
func (d Deps) targetNames(ctx Ctx, target widgetTarget) galleryTarget {
	view, err := boards.View(d.DB, ctx.Who, target.BoardID)
	if err != nil {
		return galleryTarget{}
	}
	out := galleryTarget{Board: view.Name}
	for _, s := range view.Sections {
		if s.ID == target.SectionID {
			out.Section, out.Size = s.Title, s.Size
		}
	}
	return out
}

// handleSample renders a type with default settings: live with the given
// connection, else with demo data.
func (d Deps) handleSample(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	kind, ok := widgets.Get(r.PathValue("type"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	space, _ := strconv.ParseInt(r.URL.Query().Get("space"), 10, 64)

	var frag *widgetlib.Fragment
	if conn, _ := strconv.ParseInt(r.URL.Query().Get("conn"), 10, 64); conn > 0 {
		frag, err = widgetlib.Preview(r.Context(), d.DB, ctx.Who, space, kind.Key, "", nil, &conn)
	} else {
		frag, err = widgetlib.Demo(r.Context(), d.DB, ctx.Who, space, kind.Key, "", nil)
	}
	d.renderPreview(w, ctx, kind, frag, err)
}

// handleWidgetShow renders a library widget as it looks on a board.
func (d Deps) handleWidgetShow(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	widget, _, err := widgetlib.Detail(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	kind, ok := widgets.Get(widget.Type)
	if !ok {
		http.NotFound(w, r)
		return
	}
	frag, err := widgetlib.Load(r.Context(), d.DB, ctx.Who, widget, svcdata.Cached)
	d.renderPreview(w, ctx, kind, frag, err)
}

// renderPreview writes a fragment into a preview card, or the error.
func (d Deps) renderPreview(w http.ResponseWriter, ctx Ctx, kind widgets.WidgetType, frag *widgetlib.Fragment, err error) {
	if err != nil {
		_ = d.Page(w, ctx, "widget_preview_error", http.StatusOK, map[string]any{"Error": errKey(err), "ThemeURL": ""})
		return
	}
	name := kind.Template
	if kind.Key == linkType {
		name = "widget_preview"
	}
	_ = d.Page(w, ctx, name, http.StatusOK, map[string]any{"Frag": frag, "Kind": kind, "ThemeURL": ""})
}
