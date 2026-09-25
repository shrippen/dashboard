package themes

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"dashboard/internal/db"
	"dashboard/internal/model"
	"dashboard/internal/repos/files"
	"dashboard/internal/repos/misc"
	"dashboard/internal/services/access"
)

// Theme fonts: admins upload font files per theme. The file name carries
// family and weight, e.g. "Inter-600.woff2" -> font-family "Inter", 600.
// Tokens (--font-heading, ...) then name the family.

const (
	maxFont       = 1 << 20
	maxFonts      = 12
	defaultWeight = "400"
	fontURLPrefix = "/theme-fonts/"
)

var fontName = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9 _]{0,40})(?:-([1-9]00))?\.(woff2|woff|ttf|otf)$`)

// fontTypes maps a font extension to its media type and CSS format().
var fontTypes = map[string][2]string{
	"woff2": {"font/woff2", "woff2"},
	"woff":  {"font/woff", "woff"},
	"ttf":   {"font/ttf", "truetype"},
	"otf":   {"font/otf", "opentype"},
}

var (
	storeMu   sync.RWMutex
	fontStore = files.New("")
)

// InitFonts sets the directory theme fonts live in (DATA_DIR/themes).
func InitFonts(dir string) {
	storeMu.Lock()
	fontStore = files.New(dir)
	storeMu.Unlock()
}

func store() files.Store {
	storeMu.RLock()
	defer storeMu.RUnlock()
	return fontStore
}

func themeKey(id int64) string { return strconv.FormatInt(id, 10) }

// parseFont splits "Inter-600.woff2" into family, weight, extension.
func parseFont(name string) (family, weight, ext string, ok bool) {
	m := fontName.FindStringSubmatch(name)
	if m == nil {
		return "", "", "", false
	}
	weight = m[2]
	if weight == "" {
		weight = defaultWeight
	}
	return m[1], weight, m[3], true
}

// fontFaces renders one @font-face rule per uploaded font.
func fontFaces(theme *model.Theme) string {
	var b strings.Builder
	for _, name := range theme.Fonts {
		family, weight, ext, ok := parseFont(name)
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "@font-face{font-family:%q;font-weight:%s;font-display:swap;src:url(%s%d/%s) format(%q)}\n",
			family, weight, fontURLPrefix, theme.ID, name, fontTypes[ext][1])
	}
	return b.String()
}

// editableByAdmin loads a non-builtin theme the admin may change.
func editableByAdmin(q db.Queryer, who *access.Principal, themeID int64) (*model.Theme, error) {
	if !who.IsAdmin() {
		return nil, ErrDenied
	}
	theme, err := misc.Theme(q, themeID)
	if err != nil {
		return nil, err
	}
	if theme == nil || theme.Builtin {
		return nil, ErrDenied
	}
	return theme, nil
}

// AddFont stores a font file for a theme. Admin only.
func AddFont(d *sql.DB, who *access.Principal, themeID int64, name string, data []byte) error {
	name = filepath.Base(strings.TrimSpace(name))
	if _, _, _, ok := parseFont(name); !ok {
		return ErrTheme{"theme.font_name"}
	}
	if len(data) == 0 || len(data) > maxFont {
		return ErrTheme{"theme.font_size"}
	}

	return db.WithTx(d, func(tx *sql.Tx) error {
		theme, err := editableByAdmin(tx, who, themeID)
		if err != nil {
			return err
		}
		if !slices.Contains(theme.Fonts, name) {
			if len(theme.Fonts) >= maxFonts {
				return ErrTheme{"theme.font_size"}
			}
			theme.Fonts = append(theme.Fonts, name)
		}
		if err := store().Put(themeKey(themeID), name, data); err != nil {
			return err
		}
		theme.Version++
		return misc.UpdateTheme(tx, theme)
	})
}

// RemoveFont deletes one font file of a theme. Admin only.
func RemoveFont(d *sql.DB, who *access.Principal, themeID int64, name string) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		theme, err := editableByAdmin(tx, who, themeID)
		if err != nil {
			return err
		}
		theme.Fonts = slices.DeleteFunc(theme.Fonts, func(n string) bool { return n == name })
		if err := store().Remove(themeKey(themeID), name); err != nil {
			return err
		}
		theme.Version++
		return misc.UpdateTheme(tx, theme)
	})
}

// Font returns a stored font and its media type (public, like the CSS).
func Font(d *sql.DB, themeID int64, name string) ([]byte, string, error) {
	theme, err := misc.Theme(d, themeID)
	if err != nil {
		return nil, "", err
	}
	if theme == nil || !slices.Contains(theme.Fonts, name) {
		return nil, "", ErrNotFound
	}
	_, _, ext, _ := parseFont(name)
	data, err := store().Get(themeKey(themeID), name)
	if err != nil {
		return nil, "", ErrNotFound
	}
	return data, fontTypes[ext][0], nil
}
