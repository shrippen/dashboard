// Package porting is the YAML import/export of a space and the Dashy
// conf.yml import. Credentials are never exported.
//
//	space: team:IT
//	settings: {...}
//	connections: [{id, type, name, url, credentials: shared|personal, options}]
//	widgets:     [{id, type, title, config, connection}]
//	boards:      [{name, slug, sections: [{title, cols, size, widgets: [key | team:X/key]}]}]
package porting

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/util"
	"dashboard/internal/widgets"
)

const (
	refSep         = "/"
	teamPrefix     = "team:"
	instancePrefix = "instance"
	mainArea       = "main"
	maxImport      = 2 * 1024 * 1024
)

// Mode decides what happens to the space's existing boards and widgets.
type Mode string

const (
	Merge   Mode = "merge"
	Replace Mode = "replace"
)

// Errors carry catalog keys.
var (
	ErrTooLarge   = errors.New("import.too_large")
	ErrNotMapping = errors.New("import.not_mapping")
)

// Report summarises an import.
type Report struct {
	Boards      int
	Widgets     int
	Connections int
	Skipped     []string
	Notes       []string
}

// ── Export ──

func spaceLabel(sp *model.Space) string {
	if sp.Kind == enums.SpaceTeam {
		return teamPrefix + sp.Name
	}
	return string(sp.Kind)
}

func widgetRef(w *model.Widget, home int64, spaces map[int64]*model.Space) string {
	if w.SpaceID == home {
		return w.Key
	}
	sp := spaces[w.SpaceID]
	switch {
	case sp == nil:
		return w.Key
	case sp.Kind == enums.SpaceInstance:
		return instancePrefix + refSep + w.Key
	default:
		return teamPrefix + sp.Name + refSep + w.Key
	}
}

func boardDoc(b *model.Board, spaces map[int64]*model.Space) map[string]any {
	sections := []any{}
	for _, sec := range b.Sections {
		refs := []any{}
		for _, p := range sec.Placements {
			if p.Widget != nil {
				refs = append(refs, widgetRef(p.Widget, b.SpaceID, spaces))
			}
		}
		item := map[string]any{"title": sec.Title, "widgets": refs}
		if sec.Cols != nil {
			item["cols"] = *sec.Cols
		}
		if sec.Size != enums.TileMedium {
			item["size"] = string(sec.Size)
		}
		if sec.Sort != enums.SortManual {
			item["sort"] = string(sec.Sort)
		}
		if sec.Collapsed {
			item["collapsed"] = true
		}
		if sec.Area != mainArea && sec.Area != "" {
			item["area"] = sec.Area
		}
		if sec.Span > 0 {
			item["span"] = sec.Span
		}
		if sec.Rows > 0 {
			item["rows"] = sec.Rows
		}
		if sec.Color != "" {
			item["color"] = sec.Color
		}
		sections = append(sections, item)
	}
	return map[string]any{"name": b.Name, "slug": b.Slug, "sections": sections}
}

// ExportSpace renders one space as YAML. Requires EDIT.
func ExportSpace(d *sql.DB, who *access.Principal, spaceID int64) (string, error) {
	var doc map[string]any
	err := db.WithTx(d, func(tx *sql.Tx) error {
		ref, err := access.SpaceOf(tx, who, spaceID)
		if err != nil {
			return err
		}
		if err := access.Need(access.SpaceRight(who, ref), enums.RightEdit); err != nil {
			return err
		}
		raw, err := content.Space(tx, spaceID)
		if err != nil || raw == nil {
			return util.ErrNotFound
		}
		all, err := content.AllSpaces(tx)
		if err != nil {
			return err
		}
		spaces := map[int64]*model.Space{}
		for _, sp := range all {
			spaces[sp.ID] = sp
		}

		conns, err := content.Connections(tx, []int64{spaceID})
		if err != nil {
			return err
		}
		connKeys := map[int64]string{}
		connDocs := []any{}
		for _, c := range conns {
			connKeys[c.ID] = c.Key
			item := map[string]any{"id": c.Key, "type": c.Service, "name": c.Name, "url": c.URL, "credentials": string(c.CredentialMode)}
			if len(c.Options) > 0 {
				item["options"] = c.Options
			}
			if !c.VerifyTLS {
				item["verify_tls"] = false
			}
			connDocs = append(connDocs, item)
		}

		list, err := content.Widgets(tx, []int64{spaceID})
		if err != nil {
			return err
		}
		widgetDocs := []any{}
		for _, w := range list {
			item := map[string]any{"id": w.Key, "type": w.Type, "title": w.Title, "config": util.StripSecrets(w.Config)}
			if w.ConnectionID != nil {
				item["connection"] = connKeys[*w.ConnectionID]
			}
			if w.MinTeamRole != nil {
				item["min_team_role"] = string(*w.MinTeamRole)
			}
			widgetDocs = append(widgetDocs, item)
		}

		boards, err := content.Boards(tx, []int64{spaceID})
		if err != nil {
			return err
		}
		boardDocs := []any{}
		for _, b := range boards {
			full, err := content.Board(tx, b.ID)
			if err != nil {
				return err
			}
			boardDocs = append(boardDocs, boardDoc(full, spaces))
		}

		settings := raw.Settings
		if settings == nil {
			settings = map[string]any{}
		}
		doc = map[string]any{
			"space": spaceLabel(raw), "settings": settings, "connections": connDocs,
			"widgets": widgetDocs, "boards": boardDocs,
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return dump(doc)
}

func dump(doc map[string]any) (string, error) {
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(orderedDoc(doc)); err != nil {
		return "", err
	}
	return b.String(), enc.Close()
}

// orderedDoc keeps the documented key order (space, settings, ...) in the file.
func orderedDoc(doc map[string]any) *yaml.Node {
	node := &yaml.Node{Kind: yaml.MappingNode}
	for _, key := range []string{"space", "settings", "connections", "widgets", "boards"} {
		value, ok := doc[key]
		if !ok {
			continue
		}
		var v yaml.Node
		_ = v.Encode(value)
		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &v)
	}
	return node
}

// ── Import ──

// Load parses import YAML, refusing oversized or non-mapping documents.
func Load(text string) (map[string]any, error) {
	if len(text) > maxImport {
		return nil, ErrTooLarge
	}
	var doc any
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, fmt.Errorf("yaml: %w", err)
	}
	if doc == nil {
		return map[string]any{}, nil
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil, ErrNotMapping
	}
	return m, nil
}

func str(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func list(m map[string]any, key string) []map[string]any {
	raw, _ := m[key].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if mm, ok := item.(map[string]any); ok {
			out = append(out, mm)
		}
	}
	return out
}

func intOf(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	}
	return 0, false
}

// ImportSpace applies an import document to a space. Requires EDIT.
func ImportSpace(d *sql.DB, who *access.Principal, spaceID int64, text string, mode Mode) (*Report, error) {
	doc, err := Load(text)
	if err != nil {
		return nil, err
	}
	report := &Report{}
	err = db.WithTx(d, func(tx *sql.Tx) error {
		ref, err := access.SpaceOf(tx, who, spaceID)
		if err != nil {
			return err
		}
		if err := access.Need(access.SpaceRight(who, ref), enums.RightEdit); err != nil {
			return err
		}
		if mode == Replace {
			if err := clear(tx, spaceID); err != nil {
				return err
			}
		}
		if settings, ok := doc["settings"].(map[string]any); ok && len(settings) > 0 {
			if err := mergeSettings(tx, spaceID, settings); err != nil {
				return err
			}
		}
		connIDs, err := importConnections(tx, spaceID, list(doc, "connections"), report)
		if err != nil {
			return err
		}
		keys, err := importWidgets(tx, spaceID, list(doc, "widgets"), connIDs, report)
		if err != nil {
			return err
		}
		for _, item := range list(doc, "boards") {
			if err := importBoard(tx, who, spaceID, item, keys, report); err != nil {
				return err
			}
		}
		return nil
	})
	return report, err
}

func clear(q db.Queryer, spaceID int64) error {
	boards, err := content.Boards(q, []int64{spaceID})
	if err != nil {
		return err
	}
	for _, b := range boards {
		if err := content.RemoveBoard(q, b.ID); err != nil {
			return err
		}
	}
	list, err := content.Widgets(q, []int64{spaceID})
	if err != nil {
		return err
	}
	for _, w := range list {
		if err := content.RemoveWidget(q, w.ID); err != nil {
			return err
		}
	}
	return nil
}

func mergeSettings(q db.Queryer, spaceID int64, extra map[string]any) error {
	sp, err := content.Space(q, spaceID)
	if err != nil || sp == nil {
		return util.ErrNotFound
	}
	merged := map[string]any{}
	for k, v := range sp.Settings {
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	return content.UpdateSpaceSettings(q, spaceID, merged, sp.Version)
}

func importConnections(q db.Queryer, spaceID int64, items []map[string]any, report *Report) (map[string]int64, error) {
	existing, err := content.Connections(q, []int64{spaceID})
	if err != nil {
		return nil, err
	}
	found := map[string]int64{}
	for _, c := range existing {
		found[c.Key] = c.ID
	}
	for _, item := range items {
		key := str(item, "id")
		if key == "" {
			key = util.Slug(str(item, "name"), "conn")
		}
		if _, ok := found[key]; ok {
			continue
		}
		service := str(item, "type")
		if !enums.ServiceType(service).Known() {
			report.Skipped = append(report.Skipped, "connection "+key+": type")
			continue
		}
		mode := enums.CredentialMode(str(item, "credentials"))
		if mode != enums.CredentialPersonal {
			mode = enums.CredentialShared
		}
		options, _ := item["options"].(map[string]any)
		verify := true
		if v, ok := item["verify_tls"].(bool); ok {
			verify = v
		}
		name := str(item, "name")
		if name == "" {
			name = key
		}
		c := &model.Connection{
			SpaceID: spaceID, Key: key, Name: name, Service: service, URL: strings.TrimRight(str(item, "url"), "/"),
			CredentialMode: mode, Options: options, VerifyTLS: verify, CreatedAt: time.Now().UTC(),
		}
		if err := content.AddConnection(q, c); err != nil {
			return nil, err
		}
		found[key] = c.ID
		report.Connections++
		report.Notes = append(report.Notes, "connection "+key+": token missing")
	}
	return found, nil
}

func importWidgets(q db.Queryer, spaceID int64, items []map[string]any, connIDs map[string]int64, report *Report) (map[string]int64, error) {
	existing, err := content.Widgets(q, []int64{spaceID})
	if err != nil {
		return nil, err
	}
	keys := map[string]int64{}
	taken := map[string]bool{}
	for _, w := range existing {
		keys[w.Key], taken[w.Key] = w.ID, true
	}

	for _, item := range items {
		kind := str(item, "type")
		key := str(item, "id")
		if key == "" {
			key = util.Slug(str(item, "title"), "widget")
		}
		if _, ok := widgets.Get(kind); !ok {
			report.Skipped = append(report.Skipped, "widget "+key+": type "+kind)
			continue
		}
		config, _ := item["config"].(map[string]any)
		if config == nil {
			config = map[string]any{}
		}
		config, err := util.SealSecrets(config, nil)
		if err != nil {
			return nil, err
		}
		unique := util.Unique(key, taken)
		w := &model.Widget{SpaceID: spaceID, Key: unique, Type: kind, Title: str(item, "title"), Config: config,
			Version: 1, UpdatedAt: time.Now().UTC()}
		if id, ok := connIDs[str(item, "connection")]; ok {
			w.ConnectionID = &id
		}
		if role := str(item, "min_team_role"); role != "" {
			r := enums.TeamRole(role)
			w.MinTeamRole = &r
		}
		if err := content.AddWidget(q, w); err != nil {
			return nil, err
		}
		keys[key], keys[unique], taken[unique] = w.ID, w.ID, true
		report.Widgets++
	}
	return keys, nil
}

// resolveRef finds a board's widget reference: a local key, or
// "instance/key" / "team:Name/key" in a space the importer can reach.
func resolveRef(q db.Queryer, who *access.Principal, ref string, local map[string]int64) (int64, bool, error) {
	if !strings.Contains(ref, refSep) {
		id, ok := local[ref]
		return id, ok, nil
	}
	i := strings.LastIndex(ref, refSep)
	scope, key := ref[:i], ref[i+1:]

	var space *model.Space
	var err error
	switch {
	case scope == instancePrefix:
		space, err = content.InstanceSpace(q)
	case strings.HasPrefix(scope, teamPrefix):
		team, terr := users.TeamByName(q, strings.TrimPrefix(scope, teamPrefix))
		if terr != nil {
			return 0, false, terr
		}
		if team != nil {
			space, err = content.TeamSpace(q, team.ID)
		}
	}
	if err != nil || space == nil {
		return 0, false, err
	}
	if _, reachable := who.Spaces[space.ID]; !reachable {
		return 0, false, nil
	}
	w, err := content.WidgetByKey(q, space.ID, key)
	if err != nil || w == nil {
		return 0, false, err
	}
	return w.ID, true, nil
}

func importBoard(q db.Queryer, who *access.Principal, spaceID int64, item map[string]any, keys map[string]int64, report *Report) error {
	existing, err := content.Boards(q, []int64{spaceID})
	if err != nil {
		return err
	}
	taken := map[string]bool{}
	for _, b := range existing {
		taken[b.Slug] = true
	}
	name := str(item, "name")
	if name == "" {
		name = "Board"
	}
	slugBase := str(item, "slug")
	if slugBase == "" {
		slugBase = name
	}
	board := &model.Board{SpaceID: spaceID, Slug: util.Unique(util.Slug(slugBase, "board"), taken), Name: name,
		Position: len(taken), Version: 1, UpdatedAt: time.Now().UTC()}
	if err := content.AddBoard(q, board); err != nil {
		return err
	}

	for index, raw := range list(item, "sections") {
		sec := &model.Section{BoardID: board.ID, Title: str(raw, "title"), Position: index,
			Size: enums.TileMedium, Sort: enums.SortManual, Area: mainArea}
		if n, ok := intOf(raw["cols"]); ok && n > 0 {
			sec.Cols = &n
		}
		if s := str(raw, "size"); s != "" {
			sec.Size = enums.TileSize(s)
		}
		if s := str(raw, "sort"); s != "" {
			sec.Sort = enums.SortOrder(s)
		}
		if a := str(raw, "area"); a != "" {
			sec.Area = a
		}
		sec.Collapsed, _ = raw["collapsed"].(bool)
		sec.Span, _ = intOf(raw["span"])
		sec.Rows, _ = intOf(raw["rows"])
		sec.Color = str(raw, "color")
		if err := content.AddSection(q, sec); err != nil {
			return err
		}

		refs, _ := raw["widgets"].([]any)
		for pos, r := range refs {
			ref := fmt.Sprint(r)
			id, ok, err := resolveRef(q, who, ref, keys)
			if err != nil {
				return err
			}
			if !ok {
				report.Skipped = append(report.Skipped, "board "+name+": widget "+ref)
				continue
			}
			if err := content.AddPlacement(q, &model.Placement{SectionID: sec.ID, WidgetID: id, Position: pos}); err != nil {
				return err
			}
		}
	}
	report.Boards++
	return nil
}

// ExportBoard renders one board as YAML (for use as a template). Requires VIEW.
func ExportBoard(d *sql.DB, who *access.Principal, boardID int64) (string, error) {
	var doc map[string]any
	err := db.WithTx(d, func(tx *sql.Tx) error {
		board, err := content.Board(tx, boardID)
		if err != nil || board == nil {
			return util.ErrNotFound
		}
		ref, err := access.SpaceOf(tx, who, board.SpaceID)
		if err != nil {
			return err
		}
		if err := access.Need(access.SpaceRight(who, ref), enums.RightView); err != nil {
			return err
		}
		all, err := content.AllSpaces(tx)
		if err != nil {
			return err
		}
		spaces := map[int64]*model.Space{}
		for _, sp := range all {
			spaces[sp.ID] = sp
		}
		doc = map[string]any{"boards": []any{boardDoc(board, spaces)}}
		return nil
	})
	if err != nil {
		return "", err
	}
	return dump(doc)
}

// Kind is the format of an import file.
type Kind string

const (
	KindYAML  Kind = "yaml"
	KindDashy Kind = "dashy"
)

// ErrNoUser means no account has the given email.
var ErrNoUser = errors.New("import: no user with that email")

// ImportForEmail imports into a user's personal space (CLI).
func ImportForEmail(d *sql.DB, email, text string, kind Kind) (*Report, error) {
	user, err := users.ByEmail(d, strings.TrimSpace(email))
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrNoUser
	}
	who, err := access.Load(d, user.ID)
	if err != nil {
		return nil, err
	}
	space := access.Personal(who)
	if space == nil {
		return nil, util.ErrNotFound
	}
	if kind == KindDashy {
		return ImportDashy(d, who, space.ID, text)
	}
	return ImportSpace(d, who, space.ID, text, Merge)
}

// DumpMap renders a plain map (e.g. connection options) as YAML; {} for none.
func DumpMap(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	out, err := yaml.Marshal(m)
	if err != nil {
		return ""
	}
	return string(out)
}
