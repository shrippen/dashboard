package porting

// Board templates: import documents shipped with the binary, applied to a
// space like any YAML import (merge: existing content stays).

import (
	"database/sql"
	"embed"
	"errors"
	"sort"
	"strings"

	"andon/internal/services/access"
)

//go:embed templates/*.yml
var templateFiles embed.FS

const templateDir = "templates/"

// ErrNoTemplate means the template key is unknown.
var ErrNoTemplate = errors.New("template.unknown")

// Templates lists the template keys ("familie", "freelancer", "homelab").
func Templates() []string {
	entries, _ := templateFiles.ReadDir(strings.TrimSuffix(templateDir, "/"))
	var out []string
	for _, e := range entries {
		out = append(out, strings.TrimSuffix(e.Name(), ".yml"))
	}
	sort.Strings(out)
	return out
}

// ApplyTemplate imports one template into a space.
func ApplyTemplate(d *sql.DB, who *access.Principal, spaceID int64, key string) (*Report, error) {
	raw, err := templateFiles.ReadFile(templateDir + key + ".yml")
	if err != nil {
		return nil, ErrNoTemplate
	}
	return ImportSpace(d, who, spaceID, string(raw), Merge)
}
