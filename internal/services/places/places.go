// Package places finds places by name for settings that need
// coordinates, e.g. where DWD weather warnings apply.
package places

import (
	"context"
	"strings"

	"andon/internal/enums"
	"andon/internal/sources"
)

// minQuery is the shortest name worth searching for.
const minQuery = 2

// Place is one search hit.
type Place = sources.Place

// Search returns places matching name; short names give nothing.
func Search(ctx context.Context, name string, locale enums.Locale) ([]Place, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < minQuery {
		return nil, nil
	}
	return sources.SearchPlaces(ctx, sources.GeocodeURL, name, locale)
}
