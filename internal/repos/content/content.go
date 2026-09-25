// Package content provides database access for spaces and their content:
// connections, widgets, boards, overlays, revisions.
package content

import (
	"database/sql"
	"errors"
	"strings"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
)

const revisionsKept = 50

// ── Spaces ──

const spaceCols = `id, kind, name, owner_user_id, team_id, settings, version`

func scanSpace(row interface{ Scan(...any) error }) (*model.Space, error) {
	var sp model.Space
	var ownerID, teamID sql.NullInt64
	var settings string

	err := row.Scan(&sp.ID, &sp.Kind, &sp.Name, &ownerID, &teamID, &settings, &sp.Version)
	if err != nil {
		return nil, err
	}
	if ownerID.Valid {
		sp.OwnerUserID = &ownerID.Int64
	}
	if teamID.Valid {
		sp.TeamID = &teamID.Int64
	}
	sp.Settings = map[string]any{}
	return &sp, db.FromJSON(settings, &sp.Settings)
}

// Space returns a space by id, or nil.
func Space(q db.Queryer, spaceID int64) (*model.Space, error) {
	row := q.QueryRow("SELECT "+spaceCols+" FROM spaces WHERE id = ?", spaceID)
	sp, err := scanSpace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return sp, err
}

// PersonalSpace returns one user's personal space, or nil.
func PersonalSpace(q db.Queryer, userID int64) (*model.Space, error) {
	row := q.QueryRow("SELECT "+spaceCols+" FROM spaces WHERE owner_user_id = ?", userID)
	sp, err := scanSpace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return sp, err
}

// TeamSpace returns one team's space, or nil.
func TeamSpace(q db.Queryer, teamID int64) (*model.Space, error) {
	row := q.QueryRow("SELECT "+spaceCols+" FROM spaces WHERE team_id = ?", teamID)
	sp, err := scanSpace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return sp, err
}

// InstanceSpace returns the single instance-wide space, or nil.
func InstanceSpace(q db.Queryer) (*model.Space, error) {
	row := q.QueryRow("SELECT "+spaceCols+" FROM spaces WHERE kind = ?", enums.SpaceInstance)
	sp, err := scanSpace(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return sp, err
}

// Spaces returns the spaces with the given ids.
func Spaces(q db.Queryer, ids []int64) ([]*model.Space, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	query, args := inClause("SELECT "+spaceCols+" FROM spaces WHERE id IN (%s)", ids)
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSpaces(rows)
}

// AllSpaces returns every space.
func AllSpaces(q db.Queryer) ([]*model.Space, error) {
	rows, err := q.Query("SELECT " + spaceCols + " FROM spaces")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSpaces(rows)
}

func scanSpaces(rows *sql.Rows) ([]*model.Space, error) {
	var out []*model.Space
	for rows.Next() {
		sp, err := scanSpace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}

// AddSpace inserts a new space.
func AddSpace(q db.Queryer, sp *model.Space) error {
	settings, err := db.ToJSON(orEmpty(sp.Settings))
	if err != nil {
		return err
	}
	res, err := q.Exec(
		"INSERT INTO spaces (kind, name, owner_user_id, team_id, settings, version) VALUES (?,?,?,?,?,?)",
		sp.Kind, sp.Name, sp.OwnerUserID, sp.TeamID, settings, sp.Version,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	sp.ID = id
	return nil
}

// UpdateSpaceSettings writes settings and bumps version.
func UpdateSpaceSettings(q db.Queryer, spaceID int64, settings map[string]any, version int) error {
	text, err := db.ToJSON(orEmpty(settings))
	if err != nil {
		return err
	}
	_, err = q.Exec("UPDATE spaces SET settings = ?, version = ? WHERE id = ?", text, version, spaceID)
	return err
}

// RenameSpace updates a space's display name (e.g. to follow a team rename).
func RenameSpace(q db.Queryer, spaceID int64, name string) error {
	_, err := q.Exec("UPDATE spaces SET name = ? WHERE id = ?", name, spaceID)
	return err
}

// RemoveSpace deletes a space (cascades to its connections/widgets/boards).
func RemoveSpace(q db.Queryer, spaceID int64) error {
	_, err := q.Exec("DELETE FROM spaces WHERE id = ?", spaceID)
	return err
}

// ── Connections ──

const connCols = `id, space_id, key, name, service, url, credential_mode, secret_enc,
	options, verify_tls, created_at`

func scanConnection(row interface{ Scan(...any) error }) (*model.Connection, error) {
	var c model.Connection
	var options, createdAt string

	err := row.Scan(
		&c.ID, &c.SpaceID, &c.Key, &c.Name, &c.Service, &c.URL, &c.CredentialMode,
		&c.SecretEnc, &options, &c.VerifyTLS, &createdAt,
	)
	if err != nil {
		return nil, err
	}
	c.Options = map[string]any{}
	if err := db.FromJSON(options, &c.Options); err != nil {
		return nil, err
	}
	c.CreatedAt, err = db.ParseTime(createdAt)
	return &c, err
}

// Connection returns a connection by id, or nil.
func Connection(q db.Queryer, connID int64) (*model.Connection, error) {
	row := q.QueryRow("SELECT "+connCols+" FROM connections WHERE id = ?", connID)
	c, err := scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// Connections returns the connections of the given spaces, ordered by name.
func Connections(q db.Queryer, spaceIDs []int64) ([]*model.Connection, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	query, args := inClause("SELECT "+connCols+" FROM connections WHERE space_id IN (%s) ORDER BY name", spaceIDs)
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConnections(rows)
}

// AllConnections returns every connection.
func AllConnections(q db.Queryer) ([]*model.Connection, error) {
	rows, err := q.Query("SELECT " + connCols + " FROM connections")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanConnections(rows)
}

func scanConnections(rows *sql.Rows) ([]*model.Connection, error) {
	var out []*model.Connection
	for rows.Next() {
		c, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ConnectionByKey looks up a connection by its space-scoped key.
func ConnectionByKey(q db.Queryer, spaceID int64, key string) (*model.Connection, error) {
	row := q.QueryRow(
		"SELECT "+connCols+" FROM connections WHERE space_id = ? AND key = ?", spaceID, key,
	)
	c, err := scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return c, err
}

// AddConnection inserts a new connection.
func AddConnection(q db.Queryer, c *model.Connection) error {
	options, err := db.ToJSON(orEmpty(c.Options))
	if err != nil {
		return err
	}
	res, err := q.Exec(`INSERT INTO connections
		(space_id, key, name, service, url, credential_mode, secret_enc, options, verify_tls, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		c.SpaceID, c.Key, c.Name, c.Service, c.URL, c.CredentialMode, c.SecretEnc, options,
		c.VerifyTLS, db.TimeStr(c.CreatedAt),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	c.ID = id
	return nil
}

// UpdateConnection writes back every mutable field.
func UpdateConnection(q db.Queryer, c *model.Connection) error {
	options, err := db.ToJSON(orEmpty(c.Options))
	if err != nil {
		return err
	}
	_, err = q.Exec(`UPDATE connections SET
		name=?, service=?, url=?, credential_mode=?, secret_enc=?, options=?, verify_tls=?
		WHERE id=?`,
		c.Name, c.Service, c.URL, c.CredentialMode, c.SecretEnc, options, c.VerifyTLS, c.ID,
	)
	return err
}

// RemoveConnection deletes a connection.
func RemoveConnection(q db.Queryer, connID int64) error {
	_, err := q.Exec("DELETE FROM connections WHERE id = ?", connID)
	return err
}

// Credential returns one user's personal credential for a connection, or nil.
func Credential(q db.Queryer, connID, userID int64) (*model.UserCredential, error) {
	var c model.UserCredential
	err := q.QueryRow(
		"SELECT id, connection_id, user_id, secret_enc FROM user_credentials WHERE connection_id = ? AND user_id = ?",
		connID, userID,
	).Scan(&c.ID, &c.ConnectionID, &c.UserID, &c.SecretEnc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &c, err
}

// Credentials returns every personal credential of a connection.
func Credentials(q db.Queryer, connID int64) ([]*model.UserCredential, error) {
	rows, err := q.Query(
		"SELECT id, connection_id, user_id, secret_enc FROM user_credentials WHERE connection_id = ?",
		connID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.UserCredential
	for rows.Next() {
		var c model.UserCredential
		if err := rows.Scan(&c.ID, &c.ConnectionID, &c.UserID, &c.SecretEnc); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// SetCredential inserts or replaces one user's personal credential.
func SetCredential(q db.Queryer, connID, userID int64, secretEnc []byte) error {
	existing, err := Credential(q, connID, userID)
	if err != nil {
		return err
	}
	if existing == nil {
		_, err := q.Exec(
			"INSERT INTO user_credentials (connection_id, user_id, secret_enc) VALUES (?,?,?)",
			connID, userID, secretEnc,
		)
		return err
	}
	_, err = q.Exec("UPDATE user_credentials SET secret_enc = ? WHERE id = ?", secretEnc, existing.ID)
	return err
}

// RemoveCredential deletes one user's personal credential, if any.
func RemoveCredential(q db.Queryer, connID, userID int64) error {
	_, err := q.Exec(
		"DELETE FROM user_credentials WHERE connection_id = ? AND user_id = ?", connID, userID,
	)
	return err
}

// ── Widgets ──

const widgetCols = `id, space_id, key, type, title, config, connection_id, min_team_role,
	version, updated_at`

func scanWidget(row interface{ Scan(...any) error }) (*model.Widget, error) {
	var w model.Widget
	var config, updatedAt string
	var connID sql.NullInt64
	var minRole sql.NullString

	err := row.Scan(
		&w.ID, &w.SpaceID, &w.Key, &w.Type, &w.Title, &config, &connID, &minRole,
		&w.Version, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	w.Config = map[string]any{}
	if err := db.FromJSON(config, &w.Config); err != nil {
		return nil, err
	}
	if connID.Valid {
		w.ConnectionID = &connID.Int64
	}
	if minRole.Valid {
		role := enums.TeamRole(minRole.String)
		w.MinTeamRole = &role
	}
	w.UpdatedAt, err = db.ParseTime(updatedAt)
	return &w, err
}

// Widget returns a widget by id, or nil.
func Widget(q db.Queryer, widgetID int64) (*model.Widget, error) {
	row := q.QueryRow("SELECT "+widgetCols+" FROM widgets WHERE id = ?", widgetID)
	w, err := scanWidget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return w, err
}

// Widgets returns the widgets of the given spaces, ordered by title.
func Widgets(q db.Queryer, spaceIDs []int64) ([]*model.Widget, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	query, args := inClause("SELECT "+widgetCols+" FROM widgets WHERE space_id IN (%s) ORDER BY title", spaceIDs)
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWidgets(rows)
}

func scanWidgets(rows *sql.Rows) ([]*model.Widget, error) {
	var out []*model.Widget
	for rows.Next() {
		w, err := scanWidget(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// WidgetByKey looks up a widget by its space-scoped key.
func WidgetByKey(q db.Queryer, spaceID int64, key string) (*model.Widget, error) {
	row := q.QueryRow("SELECT "+widgetCols+" FROM widgets WHERE space_id = ? AND key = ?", spaceID, key)
	w, err := scanWidget(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return w, err
}

// WidgetUses counts how many placements reference a widget.
func WidgetUses(q db.Queryer, widgetID int64) (int, error) {
	var n int
	err := q.QueryRow("SELECT COUNT(*) FROM placements WHERE widget_id = ?", widgetID).Scan(&n)
	return n, err
}

// WidgetsOnConnection returns every widget attached to a connection.
func WidgetsOnConnection(q db.Queryer, connID int64) ([]*model.Widget, error) {
	rows, err := q.Query("SELECT "+widgetCols+" FROM widgets WHERE connection_id = ?", connID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanWidgets(rows)
}

// AddWidget inserts a new widget.
func AddWidget(q db.Queryer, w *model.Widget) error {
	config, err := db.ToJSON(orEmpty(w.Config))
	if err != nil {
		return err
	}
	res, err := q.Exec(`INSERT INTO widgets
		(space_id, key, type, title, config, connection_id, min_team_role, version, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		w.SpaceID, w.Key, w.Type, w.Title, config, w.ConnectionID, minRoleStr(w.MinTeamRole),
		w.Version, db.TimeStr(w.UpdatedAt),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	w.ID = id
	return nil
}

// UpdateWidget writes back every mutable field and bumps version/updated_at.
func UpdateWidget(q db.Queryer, w *model.Widget) error {
	config, err := db.ToJSON(orEmpty(w.Config))
	if err != nil {
		return err
	}
	_, err = q.Exec(`UPDATE widgets SET
		title=?, config=?, connection_id=?, min_team_role=?, version=?, updated_at=?
		WHERE id=?`,
		w.Title, config, w.ConnectionID, minRoleStr(w.MinTeamRole), w.Version,
		db.TimeStr(w.UpdatedAt), w.ID,
	)
	return err
}

// RemoveWidget deletes a widget (cascades to placements).
func RemoveWidget(q db.Queryer, widgetID int64) error {
	_, err := q.Exec("DELETE FROM widgets WHERE id = ?", widgetID)
	return err
}

// ── Boards ──

const boardCols = `id, space_id, slug, name, position, theme_id, is_template, min_team_role,
	version, updated_at`

func scanBoardRow(row interface{ Scan(...any) error }) (*model.Board, error) {
	var b model.Board
	var updatedAt string
	var themeID sql.NullInt64
	var minRole sql.NullString

	err := row.Scan(
		&b.ID, &b.SpaceID, &b.Slug, &b.Name, &b.Position, &themeID, &b.IsTemplate, &minRole,
		&b.Version, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	if themeID.Valid {
		b.ThemeID = &themeID.Int64
	}
	if minRole.Valid {
		role := enums.TeamRole(minRole.String)
		b.MinTeamRole = &role
	}
	b.UpdatedAt, err = db.ParseTime(updatedAt)
	return &b, err
}

// loadSections fills Board.Sections (with Placements and Widget) for a board.
func loadSections(q db.Queryer, board *model.Board) error {
	rows, err := q.Query(
		"SELECT id, board_id, title, position, cols, size, sort, collapsed, area, span, row_span, color FROM sections WHERE board_id = ? ORDER BY position",
		board.ID,
	)
	if err != nil {
		return err
	}
	defer rows.Close()

	var sections []model.Section
	for rows.Next() {
		var sec model.Section
		var cols sql.NullInt64
		if err := rows.Scan(&sec.ID, &sec.BoardID, &sec.Title, &sec.Position, &cols,
			&sec.Size, &sec.Sort, &sec.Collapsed, &sec.Area, &sec.Span, &sec.Rows, &sec.Color); err != nil {
			return err
		}
		if cols.Valid {
			n := int(cols.Int64)
			sec.Cols = &n
		}
		sections = append(sections, sec)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range sections {
		placements, err := loadPlacements(q, sections[i].ID)
		if err != nil {
			return err
		}
		sections[i].Placements = placements
	}
	board.Sections = sections
	return nil
}

func loadPlacements(q db.Queryer, sectionID int64) ([]model.Placement, error) {
	rows, err := q.Query(
		`SELECT p.id, p.section_id, p.widget_id, p.position,
			widgets.id, widgets.space_id, widgets.key, widgets.type, widgets.title, widgets.config,
			widgets.connection_id, widgets.min_team_role, widgets.version, widgets.updated_at
		FROM placements p JOIN widgets ON widgets.id = p.widget_id
		WHERE p.section_id = ? ORDER BY p.position`,
		sectionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.Placement
	for rows.Next() {
		var p model.Placement
		w, err := scanPlacementJoined(rows, &p)
		if err != nil {
			return nil, err
		}
		p.Widget = w
		out = append(out, p)
	}
	return out, rows.Err()
}

// scanPlacementJoined scans a placement row followed by a widget row (used
// by loadPlacements' JOIN query) into p and a returned Widget.
func scanPlacementJoined(rows *sql.Rows, p *model.Placement) (*model.Widget, error) {
	var w model.Widget
	var config, updatedAt string
	var connID sql.NullInt64
	var minRole sql.NullString

	err := rows.Scan(
		&p.ID, &p.SectionID, &p.WidgetID, &p.Position,
		&w.ID, &w.SpaceID, &w.Key, &w.Type, &w.Title, &config, &connID, &minRole,
		&w.Version, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	w.Config = map[string]any{}
	if err := db.FromJSON(config, &w.Config); err != nil {
		return nil, err
	}
	if connID.Valid {
		w.ConnectionID = &connID.Int64
	}
	if minRole.Valid {
		role := enums.TeamRole(minRole.String)
		w.MinTeamRole = &role
	}
	if w.UpdatedAt, err = db.ParseTime(updatedAt); err != nil {
		return nil, err
	}
	return &w, nil
}

// Board returns a board with its sections/placements/widgets, or nil.
func Board(q db.Queryer, boardID int64) (*model.Board, error) {
	row := q.QueryRow("SELECT "+boardCols+" FROM boards WHERE id = ?", boardID)
	b, err := scanBoardRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := loadSections(q, b); err != nil {
		return nil, err
	}
	return b, nil
}

// BoardBySlug returns a board (with content) by its space-scoped slug.
func BoardBySlug(q db.Queryer, spaceID int64, slug string) (*model.Board, error) {
	row := q.QueryRow("SELECT "+boardCols+" FROM boards WHERE space_id = ? AND slug = ?", spaceID, slug)
	b, err := scanBoardRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := loadSections(q, b); err != nil {
		return nil, err
	}
	return b, nil
}

// Boards returns the boards of the given spaces (without content), ordered
// by position then id.
func Boards(q db.Queryer, spaceIDs []int64) ([]*model.Board, error) {
	if len(spaceIDs) == 0 {
		return nil, nil
	}
	query, args := inClause("SELECT "+boardCols+" FROM boards WHERE space_id IN (%s) ORDER BY position, id", spaceIDs)
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.Board
	for rows.Next() {
		b, err := scanBoardRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// AddBoard inserts a new board.
func AddBoard(q db.Queryer, b *model.Board) error {
	res, err := q.Exec(`INSERT INTO boards
		(space_id, slug, name, position, theme_id, is_template, min_team_role, version, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		b.SpaceID, b.Slug, b.Name, b.Position, b.ThemeID, b.IsTemplate, minRoleStr(b.MinTeamRole),
		b.Version, db.TimeStr(b.UpdatedAt),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	b.ID = id
	return nil
}

// UpdateBoard writes back the board's own fields (not its sections).
func UpdateBoard(q db.Queryer, b *model.Board) error {
	_, err := q.Exec(`UPDATE boards SET
		name=?, position=?, theme_id=?, is_template=?, min_team_role=?, version=?, updated_at=?
		WHERE id=?`,
		b.Name, b.Position, b.ThemeID, b.IsTemplate, minRoleStr(b.MinTeamRole), b.Version,
		db.TimeStr(b.UpdatedAt), b.ID,
	)
	return err
}

// RemoveBoard deletes a board (cascades to sections/placements/overlays).
func RemoveBoard(q db.Queryer, boardID int64) error {
	_, err := q.Exec("DELETE FROM boards WHERE id = ?", boardID)
	return err
}

// Section returns a section by id (without placements), or nil.
func Section(q db.Queryer, sectionID int64) (*model.Section, error) {
	var sec model.Section
	var cols sql.NullInt64
	err := q.QueryRow(
		"SELECT id, board_id, title, position, cols, size, sort, collapsed, area, span, row_span, color FROM sections WHERE id = ?",
		sectionID,
	).Scan(&sec.ID, &sec.BoardID, &sec.Title, &sec.Position, &cols, &sec.Size, &sec.Sort,
		&sec.Collapsed, &sec.Area, &sec.Span, &sec.Rows, &sec.Color)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if cols.Valid {
		n := int(cols.Int64)
		sec.Cols = &n
	}
	return &sec, nil
}

// AddSection inserts a new section.
func AddSection(q db.Queryer, sec *model.Section) error {
	res, err := q.Exec(
		"INSERT INTO sections (board_id, title, position, cols, size, sort, collapsed, area, span, row_span, color) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		sec.BoardID, sec.Title, sec.Position, sec.Cols, sec.Size, sec.Sort, sec.Collapsed, sec.Area, sec.Span, sec.Rows, sec.Color,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	sec.ID = id
	return nil
}

// UpdateSection writes back a section's own fields.
func UpdateSection(q db.Queryer, sec *model.Section) error {
	_, err := q.Exec(
		"UPDATE sections SET title=?, position=?, cols=?, size=?, sort=?, collapsed=?, area=?, span=?, row_span=?, color=? WHERE id=?",
		sec.Title, sec.Position, sec.Cols, sec.Size, sec.Sort, sec.Collapsed, sec.Area, sec.Span, sec.Rows, sec.Color, sec.ID,
	)
	return err
}

// RemoveSection deletes a section (cascades to placements).
func RemoveSection(q db.Queryer, sectionID int64) error {
	_, err := q.Exec("DELETE FROM sections WHERE id = ?", sectionID)
	return err
}

// Placement returns a placement with its widget and section, or nil.
func Placement(q db.Queryer, placementID int64) (*model.Placement, error) {
	var p model.Placement
	err := q.QueryRow(
		"SELECT id, section_id, widget_id, position FROM placements WHERE id = ?", placementID,
	).Scan(&p.ID, &p.SectionID, &p.WidgetID, &p.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	w, err := Widget(q, p.WidgetID)
	if err != nil {
		return nil, err
	}
	p.Widget = w
	return &p, nil
}

// AddPlacement inserts a new placement.
func AddPlacement(q db.Queryer, p *model.Placement) error {
	res, err := q.Exec(
		"INSERT INTO placements (section_id, widget_id, position) VALUES (?,?,?)",
		p.SectionID, p.WidgetID, p.Position,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	p.ID = id
	return nil
}

// UpdatePlacementPosition moves a placement, possibly to another section.
func UpdatePlacementPosition(q db.Queryer, placementID, sectionID int64, position int) error {
	_, err := q.Exec(
		"UPDATE placements SET section_id = ?, position = ? WHERE id = ?",
		sectionID, position, placementID,
	)
	return err
}

// RemovePlacement deletes a placement.
func RemovePlacement(q db.Queryer, placementID int64) error {
	_, err := q.Exec("DELETE FROM placements WHERE id = ?", placementID)
	return err
}

// Overlay returns one user's layout overlay for a board, or nil.
func Overlay(q db.Queryer, userID, boardID int64) (*model.Overlay, error) {
	var o model.Overlay
	var data string
	err := q.QueryRow(
		"SELECT id, user_id, board_id, data FROM overlays WHERE user_id = ? AND board_id = ?",
		userID, boardID,
	).Scan(&o.ID, &o.UserID, &o.BoardID, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	o.Data = map[string]any{}
	return &o, db.FromJSON(data, &o.Data)
}

// SetOverlay inserts or replaces one user's board overlay.
func SetOverlay(q db.Queryer, userID, boardID int64, data map[string]any) error {
	text, err := db.ToJSON(orEmpty(data))
	if err != nil {
		return err
	}
	existing, err := Overlay(q, userID, boardID)
	if err != nil {
		return err
	}
	if existing == nil {
		_, err := q.Exec(
			"INSERT INTO overlays (user_id, board_id, data) VALUES (?,?,?)", userID, boardID, text,
		)
		return err
	}
	_, err = q.Exec("UPDATE overlays SET data = ? WHERE id = ?", text, existing.ID)
	return err
}

// RemoveOverlay deletes one user's board overlay, if any.
func RemoveOverlay(q db.Queryer, userID, boardID int64) error {
	_, err := q.Exec("DELETE FROM overlays WHERE user_id = ? AND board_id = ?", userID, boardID)
	return err
}

// ── Revisions ──

// Revisions returns the revisions of one entity, newest first.
func Revisions(q db.Queryer, kind enums.RevisionKind, entityID int64) ([]*model.Revision, error) {
	rows, err := q.Query(
		"SELECT id, kind, entity_id, space_id, user_id, version, data, created_at FROM revisions "+
			"WHERE kind = ? AND entity_id = ? ORDER BY id DESC",
		kind, entityID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.Revision
	for rows.Next() {
		r, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanRevision(row interface{ Scan(...any) error }) (*model.Revision, error) {
	var r model.Revision
	var data, createdAt string
	var userID sql.NullInt64

	err := row.Scan(&r.ID, &r.Kind, &r.EntityID, &r.SpaceID, &userID, &r.Version, &data, &createdAt)
	if err != nil {
		return nil, err
	}
	if userID.Valid {
		r.UserID = &userID.Int64
	}
	r.Data = map[string]any{}
	if err := db.FromJSON(data, &r.Data); err != nil {
		return nil, err
	}
	r.CreatedAt, err = db.ParseTime(createdAt)
	return &r, err
}

// RevisionByID returns one revision, or nil.
func RevisionByID(q db.Queryer, revID int64) (*model.Revision, error) {
	row := q.QueryRow(
		"SELECT id, kind, entity_id, space_id, user_id, version, data, created_at FROM revisions WHERE id = ?",
		revID,
	)
	r, err := scanRevision(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

// AddRevision inserts a new revision.
func AddRevision(q db.Queryer, r *model.Revision) error {
	data, err := db.ToJSON(orEmpty(r.Data))
	if err != nil {
		return err
	}
	res, err := q.Exec(
		"INSERT INTO revisions (kind, entity_id, space_id, user_id, version, data, created_at) VALUES (?,?,?,?,?,?,?)",
		r.Kind, r.EntityID, r.SpaceID, r.UserID, r.Version, data, db.TimeStr(r.CreatedAt),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	r.ID = id
	return nil
}

// PruneRevisions deletes all but the most recent revisionsKept revisions of
// one entity.
func PruneRevisions(q db.Queryer, kind enums.RevisionKind, entityID int64) error {
	all, err := Revisions(q, kind, entityID)
	if err != nil {
		return err
	}
	if len(all) <= revisionsKept {
		return nil
	}
	for _, old := range all[revisionsKept:] {
		if _, err := q.Exec("DELETE FROM revisions WHERE id = ?", old.ID); err != nil {
			return err
		}
	}
	return nil
}

// ── helpers ──

func minRoleStr(role *enums.TeamRole) any {
	if role == nil {
		return nil
	}
	return string(*role)
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// inClause builds "... IN (?,?,?)" for a slice of int64 ids and returns the
// matching arg list, so callers avoid ad-hoc string building.
func inClause(format string, ids []int64) (string, []any) {
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	return sprintfIn(format, placeholders), args
}

func sprintfIn(format string, placeholders []string) string {
	return strings.Replace(format, "%s", strings.Join(placeholders, ","), 1)
}
