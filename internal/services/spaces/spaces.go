// Package spaces edits a space's settings: goals, tax values and the rule
// thresholds the analysis uses.
//
//	settings = {"goals": {...}, "tax": {...}, "rules": {"kimai.missing_day": {"enabled": true, ...}}}
package spaces

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"

	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/repos/content"
	"andon/internal/rules"
	"andon/internal/services/access"
	"andon/internal/services/audit"
	"andon/internal/services/util"
)

const enabledKey = "enabled"

// ParamKind is how a rule parameter is edited.
type ParamKind string

const (
	ParamNumber ParamKind = "number"
	ParamBool   ParamKind = "bool"
	ParamList   ParamKind = "list"
)

// Param is one rule parameter with its effective value.
type Param struct {
	Key  string
	Kind ParamKind
	Text string
	On   bool
}

// RuleView is one rule with its effective parameters.
type RuleView struct {
	ID      string
	Enabled bool
	Params  []Param
}

// Settings returns a space's settings. Requires VIEW.
func Settings(d *sql.DB, who *access.Principal, spaceID int64) (map[string]any, error) {
	ref, err := access.SpaceOf(d, who, spaceID)
	if err != nil {
		return nil, err
	}
	if err := access.Need(access.SpaceRight(who, ref), enums.RightView); err != nil {
		return nil, err
	}
	sp, err := content.Space(d, spaceID)
	if err != nil || sp == nil {
		return nil, util.ErrNotFound
	}
	if sp.Settings == nil {
		return map[string]any{}, nil
	}
	return sp.Settings, nil
}

// Update merges changes into a space's settings. Personal spaces need
// EDIT, team and instance spaces MANAGE (owners, admins).
func Update(d *sql.DB, who *access.Principal, spaceID int64, changes map[string]any, ip string) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		ref, err := access.SpaceOf(tx, who, spaceID)
		if err != nil {
			return err
		}
		need := enums.RightManage
		if ref.Kind == enums.SpacePersonal {
			need = enums.RightEdit
		}
		if err := access.Need(access.SpaceRight(who, ref), need); err != nil {
			return err
		}
		sp, err := content.Space(tx, spaceID)
		if err != nil || sp == nil {
			return util.ErrNotFound
		}
		merged := map[string]any{}
		for k, v := range sp.Settings {
			merged[k] = v
		}
		keys := make([]string, 0, len(changes))
		for k, v := range changes {
			merged[k] = v
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if err := content.UpdateSpaceSettings(tx, spaceID, merged, sp.Version); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "space.settings", sp.Name, ip, map[string]any{"keys": keys})
	})
}

func paramOf(key string, v any) Param {
	switch x := v.(type) {
	case bool:
		return Param{Key: key, Kind: ParamBool, On: x}
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, toText(item))
		}
		return Param{Key: key, Kind: ParamList, Text: strings.Join(parts, ", ")}
	case []string:
		return Param{Key: key, Kind: ParamList, Text: strings.Join(x, ", ")}
	default:
		return Param{Key: key, Kind: ParamNumber, Text: toText(v)}
	}
}

func toText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	}
	return ""
}

// RuleViews lists every rule with its effective parameters, sorted by id.
func RuleViews(settings map[string]any) []RuleView {
	specs := rules.AllRules()
	sort.Slice(specs, func(i, j int) bool { return specs[i].ID < specs[j].ID })
	out := make([]RuleView, 0, len(specs))
	for _, spec := range specs {
		cfg := rules.Config(spec, settings)
		view := RuleView{ID: spec.ID, Enabled: true}
		if on, ok := cfg[enabledKey].(bool); ok {
			view.Enabled = on
		}
		keys := make([]string, 0, len(cfg))
		for k := range cfg {
			if k != enabledKey {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			view.Params = append(view.Params, paramOf(k, cfg[k]))
		}
		out = append(out, view)
	}
	return out
}

// ParseRules reads "rule.<id>.<key>" form values back into rule configs,
// typed after each rule's defaults.
func ParseRules(get func(name string) string) map[string]any {
	out := map[string]any{}
	for _, spec := range rules.AllRules() {
		prefix := "rule." + spec.ID + "."
		values := map[string]any{enabledKey: get(prefix+enabledKey) != ""}
		for key, def := range spec.Defaults {
			raw := strings.TrimSpace(get(prefix + key))
			switch def.(type) {
			case bool:
				values[key] = raw != ""
			case []any, []string:
				list := []any{}
				for _, p := range strings.Split(raw, ",") {
					if p = strings.TrimSpace(p); p != "" {
						list = append(list, p)
					}
				}
				values[key] = list
			default:
				if n, err := strconv.ParseFloat(strings.ReplaceAll(raw, ",", "."), 64); err == nil {
					values[key] = n
				} else {
					values[key] = def
				}
			}
		}
		out[spec.ID] = values
	}
	return out
}
