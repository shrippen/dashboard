// Package widgetlib is the widget library: create, change, delete widgets.
// Loading a widget's live data (running its queries against a source) is a
// separate, not-yet-ported concern (app/services/widgets.py's other half);
// this package only manages the widget rows themselves.
//
//	placement ──► widget ──► type.Queries(config) ──► (not yet: svcdata.Get)
//	    │            │
//	 board right   widget right (view)
package widgetlib

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	data "dashboard/internal/repos/data"
	"dashboard/internal/rules"
	"dashboard/internal/services/access"
	"dashboard/internal/services/connections"
	"dashboard/internal/services/hints"
	"dashboard/internal/services/history"
	"dashboard/internal/services/linkstatus"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/services/util"
	"dashboard/internal/services/weekly"
	"dashboard/internal/sources"
	"dashboard/internal/widgets"
)

// genericSources are query sources that resolve through the widget's own
// connection service ("data" -> "kimai.data", "invoiceninja.data", ...).
var genericSources = map[string]bool{dataSource: true, "test": true}

const dataSource = "data"

var (
	ErrNotFound         = util.ErrNotFound
	ErrConflict         = util.ErrConflict
	ErrDenied           = access.ErrDenied
	ErrUnknownType      = errors.New("widgetlib: unknown widget type")
	ErrConnRequired     = errors.New("widgetlib: connection required")
	ErrConnMissing      = errors.New("widgetlib: connection not found")
	ErrConnWrongService = errors.New("widgetlib: connection is for a different service")
)

// Ref is a widget library entry.
type Ref struct {
	ID           int64
	Key          string
	Type         string
	Title        string
	Space        access.SpaceRef
	ConnectionID *int64
	Uses         int
	CanEdit      bool
}

func widgetRight(q db.Queryer, who *access.Principal, w *model.Widget) (enums.Right, error) {
	space, err := access.SpaceOf(q, who, w.SpaceID)
	if err != nil {
		return enums.RightNone, err
	}
	return access.Right(who, enums.ResourceWidget, w.ID, space, w.MinTeamRole), nil
}

// Library lists the widgets who may at least USE: their own spaces plus
// explicitly shared ones.
func Library(d *sql.DB, who *access.Principal) ([]Ref, error) {
	var out []Ref
	err := db.WithTx(d, func(tx *sql.Tx) error {
		spaceIDs := make([]int64, 0, len(who.Spaces))
		for id := range who.Spaces {
			spaceIDs = append(spaceIDs, id)
		}
		found, err := content.Widgets(tx, spaceIDs)
		if err != nil {
			return err
		}
		for _, id := range access.GrantedResourceIDs(who, enums.ResourceWidget) {
			w, err := content.Widget(tx, id)
			if err != nil {
				return err
			}
			if w != nil {
				if _, already := who.Spaces[w.SpaceID]; !already {
					found = append(found, w)
				}
			}
		}

		for _, w := range found {
			granted, err := widgetRight(tx, who, w)
			if err != nil {
				return err
			}
			if granted < enums.RightUse {
				continue
			}
			space, err := access.SpaceOf(tx, who, w.SpaceID)
			if err != nil {
				return err
			}
			uses, err := content.WidgetUses(tx, w.ID)
			if err != nil {
				return err
			}
			ref := Ref{ID: w.ID, Key: w.Key, Type: w.Type, Title: w.Title, ConnectionID: w.ConnectionID,
				Uses: uses, CanEdit: granted >= enums.RightEdit}
			if space != nil {
				ref.Space = *space
			}
			out = append(out, ref)
		}
		return nil
	})
	return out, err
}

func checkConnection(q db.Queryer, who *access.Principal, connID *int64, typeKey string) error {
	kind, ok := widgets.Get(typeKey)
	if !ok {
		return ErrUnknownType
	}
	if connID == nil {
		if kind.Service != "" {
			return ErrConnRequired
		}
		return nil
	}
	conn, err := content.Connection(q, *connID)
	if err != nil {
		return err
	}
	if conn == nil {
		return ErrConnMissing
	}
	space, err := access.SpaceOf(q, who, conn.SpaceID)
	if err != nil {
		return err
	}
	if err := access.Need(access.Right(who, enums.ResourceConnection, conn.ID, space, nil), enums.RightUse); err != nil {
		return err
	}
	if kind.Service != "" && enums.ServiceType(conn.Service) != kind.Service {
		return ErrConnWrongService
	}
	return nil
}

// Create adds a new widget to a space. Requires EDIT on the space.
func Create(d *sql.DB, who *access.Principal, spaceID int64, typeKey, title string, config map[string]any,
	connID *int64, minRole *enums.TeamRole) (int64, error) {
	if _, ok := widgets.Get(typeKey); !ok {
		return 0, ErrUnknownType
	}
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		space, err := access.SpaceOf(tx, who, spaceID)
		if err != nil {
			return err
		}
		if err := access.Need(access.SpaceRight(who, space), enums.RightEdit); err != nil {
			return err
		}
		if err := checkConnection(tx, who, connID, typeKey); err != nil {
			return err
		}

		existing, err := content.Widgets(tx, []int64{spaceID})
		if err != nil {
			return err
		}
		taken := map[string]bool{}
		for _, w := range existing {
			taken[w.Key] = true
		}
		label := strings.TrimSpace(title)
		config, err := util.SealSecrets(config, nil)
		if err != nil {
			return err
		}

		widget := &model.Widget{
			SpaceID: spaceID, Key: util.Unique(util.Slug(firstNonEmpty(label, typeKey), typeKey), taken),
			Type: typeKey, Title: label, Config: config, ConnectionID: connID, MinTeamRole: minRole,
			Version: 1, UpdatedAt: time.Now().UTC(),
		}
		if err := content.AddWidget(tx, widget); err != nil {
			return err
		}
		id = widget.ID
		return snapshot(tx, who, widget)
	})
	return id, err
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Detail returns a widget and the caller's right on it. Requires VIEW.
func Detail(d *sql.DB, who *access.Principal, widgetID int64) (*model.Widget, enums.Right, error) {
	var w *model.Widget
	var granted enums.Right
	err := db.WithTx(d, func(tx *sql.Tx) error {
		item, err := content.Widget(tx, widgetID)
		if err != nil {
			return err
		}
		if item == nil {
			return ErrNotFound
		}
		g, err := widgetRight(tx, who, item)
		if err != nil {
			return err
		}
		if err := access.Need(g, enums.RightView); err != nil {
			return err
		}
		w, granted = item, g
		return nil
	})
	return w, granted, err
}

// Update changes a widget's title/config/connection/min-role. Requires EDIT.
func Update(d *sql.DB, who *access.Principal, widgetID int64, version int, title string, config map[string]any,
	connID *int64, minRole *enums.TeamRole) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		widget, err := content.Widget(tx, widgetID)
		if err != nil {
			return err
		}
		if widget == nil {
			return ErrNotFound
		}
		g, err := widgetRight(tx, who, widget)
		if err != nil {
			return err
		}
		if err := access.Need(g, enums.RightEdit); err != nil {
			return err
		}
		if widget.Version != version {
			return ErrConflict
		}
		if err := checkConnection(tx, who, connID, widget.Type); err != nil {
			return err
		}

		sealed, err := util.SealSecrets(config, widget.Config)
		if err != nil {
			return err
		}
		widget.Title = strings.TrimSpace(title)
		widget.Config = sealed
		widget.ConnectionID = connID
		widget.MinTeamRole = minRole
		widget.Version++
		widget.UpdatedAt = time.Now().UTC()
		if err := content.UpdateWidget(tx, widget); err != nil {
			return err
		}
		return snapshot(tx, who, widget)
	})
}

// Copy creates an independent copy of a widget in spaceID ("keep as copy").
func Copy(d *sql.DB, who *access.Principal, widgetID, spaceID int64) (int64, error) {
	w, _, err := Detail(d, who, widgetID)
	if err != nil {
		return 0, err
	}
	return Create(d, who, spaceID, w.Type, w.Title, w.Config, w.ConnectionID, nil)
}

// Delete removes a widget. Requires MANAGE.
func Delete(d *sql.DB, who *access.Principal, widgetID int64) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		widget, err := content.Widget(tx, widgetID)
		if err != nil || widget == nil {
			return err
		}
		g, err := widgetRight(tx, who, widget)
		if err != nil {
			return err
		}
		if err := access.Need(g, enums.RightManage); err != nil {
			return err
		}
		return content.RemoveWidget(tx, widget.ID)
	})
}

// ── Display ──

// Slot is one query's result, ready for a widget template.
type Slot struct {
	Data              any
	Error             string
	OkAt              time.Time
	MissingCredential string // connection name, "" if credentials are fine
	Pending           bool   // not fetched by a background run yet
}

// Fragment is a widget's live view: its queries' results shaped by its
// type's View function, plus its connection's hint badge. Ports the
// display half of Python's app/services/widgets.py ("load").
type Fragment struct {
	WidgetID  int64
	Type      string
	Title     string
	Config    any
	Slots     map[string]Slot
	HintCount int
	HintLevel enums.Severity
	HintConn  int64 // connection the hint count belongs to, 0 = none
	View      map[string]any
}

func sourceFor(q widgets.Query, target *model.Connection) string {
	if target == nil || !genericSources[q.Source] {
		return q.Source
	}
	if q.Source == dataSource {
		return sources.DataKey(enums.ServiceType(target.Service))
	}
	return target.Service + "." + q.Source
}

// infoKeyOf returns the connection key a link tile's info line names, or "".
func infoKeyOf(cfg any) string {
	link, ok := cfg.(widgets.LinkConfig)
	if !ok {
		return ""
	}
	return link.InfoConn
}

// infoConnection finds a connection by key: first in the widget's space,
// then in any space the viewer reaches.
func infoConnection(q db.Queryer, who *access.Principal, widget *model.Widget, key string) (*model.Connection, error) {
	found, err := content.ConnectionByKey(q, widget.SpaceID, key)
	if err != nil || found != nil {
		return found, err
	}
	for spaceID := range who.Spaces {
		found, err := content.ConnectionByKey(q, spaceID, key)
		if err != nil || found != nil {
			return found, err
		}
	}
	return nil, nil
}

// linkHost returns the lower-case host of a link tile's URL, or "".
func linkHost(cfg any) string {
	link, ok := cfg.(widgets.LinkConfig)
	if !ok {
		return ""
	}
	u, err := url.Parse(link.URL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// hostConnection finds the connection serving host (pl.example.org →
// the Paperless connection), in the widget's space first. A link tile
// without an info connection counts that connection's hints.
func hostConnection(q db.Queryer, who *access.Principal, widget *model.Widget, host string) (*model.Connection, error) {
	spaceIDs := []int64{widget.SpaceID}
	for spaceID := range who.Spaces {
		if spaceID != widget.SpaceID {
			spaceIDs = append(spaceIDs, spaceID)
		}
	}
	slices.Sort(spaceIDs[1:])

	for _, spaceID := range spaceIDs {
		list, err := content.Connections(q, []int64{spaceID})
		if err != nil {
			return nil, err
		}
		for _, c := range list {
			u, err := url.Parse(c.URL)
			if err == nil && strings.EqualFold(u.Hostname(), host) {
				return c, nil
			}
		}
	}
	return nil, nil
}

// peerConnection finds a connection of a service for ConnPeer queries:
// first in the widget's space, then in any space the viewer reaches.
func peerConnection(q db.Queryer, who *access.Principal, widget *model.Widget, service enums.ServiceType) (*model.Connection, error) {
	spaceIDs := []int64{widget.SpaceID}
	for spaceID := range who.Spaces {
		if spaceID != widget.SpaceID {
			spaceIDs = append(spaceIDs, spaceID)
		}
	}
	slices.Sort(spaceIDs[1:])

	for _, spaceID := range spaceIDs {
		list, err := content.Connections(q, []int64{spaceID})
		if err != nil {
			return nil, err
		}
		for _, c := range list {
			if c.Service == string(service) {
				return c, nil
			}
		}
	}
	return nil, nil
}

// Load runs a widget's queries against its connection (svcdata.Get, so
// caching and credential resolution apply) and shapes the results via its
// type's View function.
func Load(ctx context.Context, d *sql.DB, who *access.Principal, widget *model.Widget, fresh svcdata.Freshness) (*Fragment, error) {
	return load(ctx, d, who, widget, fresh, originStored)
}

// origin says where a fragment's connection data comes from.
type origin int

const (
	originStored origin = iota // the widget's real connections
	originDemo                 // generated demo datasets, nothing fetched or stored
)

// demoURL makes sources return their demo dataset (see sources/demo.go).
const demoURL = "demo://gallery"

// demoConn is an unsaved connection that yields a service's demo data.
func demoConn(service enums.ServiceType) *model.Connection {
	if service == "" {
		return nil
	}
	return &model.Connection{Service: string(service), URL: demoURL}
}

func load(ctx context.Context, d *sql.DB, who *access.Principal, widget *model.Widget, fresh svcdata.Freshness, from origin) (*Fragment, error) {
	kind, ok := widgets.Get(widget.Type)
	if !ok {
		return nil, ErrUnknownType
	}
	cfg, _ := widgets.Decode(widget.Type, util.OpenSecrets(widget.Config))
	frag := &Fragment{WidgetID: widget.ID, Type: widget.Type, Title: widget.Title, Config: cfg, Slots: map[string]Slot{}}

	var conn, infoConn, hostConn *model.Connection
	var settings map[string]any
	infoKey := infoKeyOf(cfg)
	host := linkHost(cfg)
	err := db.WithRead(d, func(tx *sql.Tx) error {
		if widget.ConnectionID != nil {
			c, err := content.Connection(tx, *widget.ConnectionID)
			if err != nil {
				return err
			}
			conn = c
		}
		if infoKey != "" {
			c, err := infoConnection(tx, who, widget, infoKey)
			if err != nil {
				return err
			}
			infoConn = c
		}
		if conn == nil && infoConn == nil && host != "" {
			c, err := hostConnection(tx, who, widget, host)
			if err != nil {
				return err
			}
			hostConn = c
		}
		space, err := content.Space(tx, widget.SpaceID)
		if err != nil {
			return err
		}
		if space != nil {
			settings = space.Settings
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if settings == nil {
		settings = map[string]any{}
	}
	if from == originDemo {
		conn = demoConn(kind.Service)
	}

	live := widgets.LiveData(kind, widget.Config)
	peerOptions := map[string]map[string]any{}
	for _, q := range kind.Queries(cfg) {
		var target *model.Connection
		switch q.Conn {
		case widgets.ConnWidget:
			target = conn
		case widgets.ConnInfo:
			target = infoConn
		case widgets.ConnPeer:
			if from == originDemo {
				target = demoConn(q.Service)
				break
			}
			if target, err = peerConnection(d, who, widget, q.Service); err != nil {
				return nil, err
			}
			if target != nil {
				peerOptions[q.Name] = target.Options
			}
		}
		if q.Conn != widgets.ConnNone && target == nil {
			frag.Slots[q.Name] = Slot{Error: "connection.missing"}
			continue
		}
		if from == originDemo && target != nil {
			frag.Slots[q.Name] = demoQuery(ctx, sourceFor(q, target), q.Params)
			continue
		}
		frag.Slots[q.Name] = runQuery(ctx, d, sourceFor(q, target), q.Params, target, who.UserID, integrationFreshness(q, target, live, fresh))
	}

	serviceConn := conn
	if infoConn != nil {
		serviceConn = infoConn
	}
	if serviceConn == nil {
		serviceConn = hostConn
	}
	if serviceConn != nil && from == originStored {
		count, level, err := hints.CountFor(d, who, serviceConn.ID)
		if err != nil {
			return nil, err
		}
		frag.HintCount, frag.HintLevel, frag.HintConn = count, level, serviceConn.ID
	}

	if kind.Extra == widgets.ExtraPoints && conn != nil && from == originStored {
		trend := cfg.(widgets.TrendConfig)
		points, err := loadPoints(d, conn, who.UserID, string(trend.Metric), trend.Days)
		if err != nil {
			return nil, err
		}
		frag.Slots["points"] = Slot{Data: points}
	}

	if kind.Extra == widgets.ExtraStory {
		lines, err := weekly.Story(ctx, d, who, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		frag.Slots[widgets.StorySlot] = Slot{Data: lines}
	}
	if kind.Extra == widgets.ExtraGreeting {
		g, err := greetingData(d, who, cfg.(widgets.GreetingConfig))
		if err != nil {
			return nil, err
		}
		frag.Slots[widgets.GreetingSlot] = Slot{Data: g}
	}
	if kind.Extra == widgets.ExtraConnHealth {
		strips, err := connections.Strips(d, who, widgets.ConnHealthDays, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		frag.Slots[widgets.ConnHealthSlot] = Slot{Data: connStrips(strips)}
	}
	if kind.Extra == widgets.ExtraHistory {
		h, err := history.Load(d, widget.SpaceID, 0, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		frag.Slots[widgets.HistorySlot] = Slot{Data: h}
	}

	if kind.View != nil {
		viewCtx := widgets.ViewCtx{Today: time.Now().UTC().Format("2006-01-02"), Settings: settings, PeerOptions: peerOptions}
		if serviceConn != nil {
			viewCtx.Service, viewCtx.Options = serviceConn.Service, serviceConn.Options
		}
		results := map[string]any{}
		for name, slot := range frag.Slots {
			if slot.Data != nil {
				results[name] = slot.Data
			}
		}
		frag.View = kind.View(cfg, results, viewCtx)
	}
	if link, ok := cfg.(widgets.LinkConfig); ok && link.Status == widgets.StatusHTTP {
		if up, ok := linkstatus.Bars(d, widget.ID, time.Now().UTC()); ok {
			if frag.View == nil {
				frag.View = map[string]any{}
			}
			frag.View["Uptime"] = up
		}
	}

	if kind.Extra == widgets.ExtraHints {
		hcfg := cfg.(widgets.HintsConfig)
		filter := hints.Filter{MinSeverity: enums.Severity(hcfg.MinSeverity), Sources: hcfg.Sources, Rules: rules.RulesOf(hcfg.Topic)}
		views, err := hints.Filtered(d, who, filter, hcfg.Limit)
		if err != nil {
			return nil, err
		}
		frag.View = map[string]any{"Hints": views, "Groups": hintGroups(views)}
	}

	return frag, nil
}

// integrationFreshness: connection data comes from the background run
// (Stored) unless the widget is live, then the page view fetches it
// (Cached, reused for the source TTL). Peer data (another connection,
// e.g. Kimai hours next to Ninja revenue) always stays background, so
// one widget can mix both. Sources without a connection (status ping,
// feeds, weather) keep their own cache; Force always fetches.
func integrationFreshness(q widgets.Query, target *model.Connection, live bool, fresh svcdata.Freshness) svcdata.Freshness {
	if target == nil || fresh != svcdata.Cached {
		return fresh
	}
	if live && q.Conn != widgets.ConnPeer {
		return svcdata.Cached
	}
	return svcdata.Stored
}

func runQuery(ctx context.Context, d *sql.DB, source string, params map[string]any, conn *model.Connection, userID int64, fresh svcdata.Freshness) Slot {
	res, err := svcdata.Get(ctx, d, source, params, conn, &userID, fresh)
	if err != nil {
		if errors.Is(err, svcdata.ErrMissingCredential) {
			name := ""
			if conn != nil {
				name = conn.Name
			}
			return Slot{MissingCredential: name}
		}
		return Slot{Error: "source.unknown"}
	}
	return Slot{Data: res.Data, Error: res.Error, OkAt: res.OkAt, Pending: res.Pending}
}

// demoQuery asks a source directly for its demo dataset: no cache, no
// fetch log, since the connection doesn't exist.
func demoQuery(ctx context.Context, source string, params map[string]any) Slot {
	src, err := sources.Get(source)
	if err != nil {
		return Slot{Error: "source.unknown"}
	}
	out, err := src.Fetch(ctx, sources.Ctx{URL: demoURL, Params: params})
	if err != nil {
		return Slot{Error: err.Error()}
	}
	return Slot{Data: out, OkAt: time.Now().UTC()}
}

// loadPoints returns a trend widget's daily snapshots (written by the
// analysis job), scoped to the connection and credential owner.
func loadPoints(d *sql.DB, conn *model.Connection, userID int64, metric string, days int) ([][2]any, error) {
	owner := svcdata.CredentialOwner(conn, &userID)
	ownerID := int64(0)
	if owner != nil {
		ownerID = *owner
	}
	scope := fmt.Sprintf("%d:%d", conn.ID, ownerID)
	since := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02")

	var out [][2]any
	err := db.WithRead(d, func(tx *sql.Tx) error {
		points, err := data.Points(tx, scope, metric, since)
		if err != nil {
			return err
		}
		for _, p := range points {
			out = append(out, [2]any{p.Day, p.Value})
		}
		return nil
	})
	return out, err
}

// snapshot stores a revision of a widget's own fields. Simplification vs.
// app/services/porting.py: this is the widget's own JSON, not the
// cross-space YAML shape porting.widget_dict produces (see the same note
// on services/boards.snapshot).
func snapshot(q db.Queryer, who *access.Principal, w *model.Widget) error {
	raw, err := json.Marshal(map[string]any{"title": w.Title, "type": w.Type, "config": w.Config})
	if err != nil {
		return err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	rev := &model.Revision{
		Kind: enums.RevisionWidget, EntityID: w.ID, SpaceID: w.SpaceID, UserID: &who.UserID,
		Version: w.Version, Data: data, CreatedAt: time.Now().UTC(),
	}
	if err := content.AddRevision(q, rev); err != nil {
		return err
	}
	return content.PruneRevisions(q, enums.RevisionWidget, w.ID)
}

// Preview renders an unsaved widget with the same checks as Create
// (EDIT on the space, USE on the connection), so the editor can show
// live data before saving.
func Preview(ctx context.Context, d *sql.DB, who *access.Principal, spaceID int64, typeKey, title string,
	config map[string]any, connID *int64) (*Fragment, error) {
	err := db.WithTx(d, func(tx *sql.Tx) error {
		space, err := access.SpaceOf(tx, who, spaceID)
		if err != nil {
			return err
		}
		if err := access.Need(access.SpaceRight(who, space), enums.RightEdit); err != nil {
			return err
		}
		return checkConnection(tx, who, connID, typeKey)
	})
	if err != nil {
		return nil, err
	}
	w := &model.Widget{SpaceID: spaceID, Type: typeKey, Title: title, Config: config, ConnectionID: connID}
	return Load(ctx, d, who, w, svcdata.Cached)
}

// Demo renders a type with its default config and demo data in place of
// connections, for the gallery and for a form without a connection.
// Needs EDIT on the space, like Preview.
func Demo(ctx context.Context, d *sql.DB, who *access.Principal, spaceID int64, typeKey, title string,
	config map[string]any) (*Fragment, error) {
	if _, ok := widgets.Get(typeKey); !ok {
		return nil, ErrUnknownType
	}
	err := db.WithRead(d, func(tx *sql.Tx) error {
		space, err := access.SpaceOf(tx, who, spaceID)
		if err != nil {
			return err
		}
		return access.Need(access.SpaceRight(who, space), enums.RightEdit)
	})
	if err != nil {
		return nil, err
	}
	w := &model.Widget{SpaceID: spaceID, Type: typeKey, Title: title, Config: config}
	return load(ctx, d, who, w, svcdata.Cached, originDemo)
}

// HintGroup is one severity band of the hints widget, highest first.
type HintGroup struct {
	Severity enums.Severity
	Hints    []hints.View
}

// hintGroups splits hints into severity bands, keeping their order:
//
//	[warn a, crit b, info c, warn d]  →  crit [b] · warn [a d] · info [c]
func hintGroups(views []hints.View) []HintGroup {
	levels := []enums.Severity{enums.SeverityCritical, enums.SeverityWarn, enums.SeverityInfo}
	var out []HintGroup
	for _, level := range levels {
		group := HintGroup{Severity: level}
		for _, v := range views {
			if v.Severity.Key() == level.Key() {
				group.Hints = append(group.Hints, v)
			}
		}
		if len(group.Hints) > 0 {
			out = append(out, group)
		}
	}
	return out
}

// greetingData collects what a greeting says about the viewer: open hints
// and the timeline since yesterday evening in the greeting's timezone.
func greetingData(d *sql.DB, who *access.Principal, cfg widgets.GreetingConfig) (*widgets.GreetingData, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		loc = time.Local
	}
	since := widgets.GreetingSince(time.Now().In(loc), cfg.SinceHour)

	counts, err := hints.Summary(d, who)
	if err != nil {
		return nil, err
	}
	g := &widgets.GreetingData{Name: who.Name, Since: since}
	for sev, n := range counts {
		g.OpenHints += n
		if n > 0 && int(sev) > g.HintLevel {
			g.HintLevel = int(sev)
		}
	}

	entries, err := history.Timeline(d, who, since.UTC(), greetingChanges)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		g.Changes = append(g.Changes, widgets.GreetingChange{Kind: e.Kind, Subject: e.Subject, Detail: e.Detail, At: e.At})
	}
	return g, nil
}

// greetingChanges caps the timeline a greeting reads.
const greetingChanges = 200

// connStrips hands connection strips to the widget layer.
func connStrips(strips []connections.Strip) []widgets.ConnStrip {
	out := make([]widgets.ConnStrip, len(strips))
	for i, s := range strips {
		out[i] = widgets.ConnStrip{Name: s.Name, Service: s.Service, FailPct: s.FailPct}
		for _, d := range s.Days {
			out[i].Days = append(out[i].Days, widgets.ConnDayState{Day: d.Day, OK: d.OK, Fail: d.Fail})
		}
	}
	return out
}
