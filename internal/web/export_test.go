package web

import "andon/internal/services/hints"

// Test-only access to the hints page helpers.
var GroupHints = groupHints

type HintFilter = hintFilter

func (f hintFilter) Apply(all []hints.View) []hints.View { return f.apply(all) }
