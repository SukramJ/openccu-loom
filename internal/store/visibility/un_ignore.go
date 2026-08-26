// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// UnIgnoreWildcard is the placeholder token used in full-format
// un-ignore candidate strings when the channel or model is left
// unspecified (e.g. "ALARM_COUNT:VALUES@HmIP-SWSD:all").
const UnIgnoreWildcard = "all"

// ignoreForUnIgnoreParameters lists parameters that must never be
// offered as un-ignore candidates because they apply at device or
// transport scope and toggling them per-parameter is meaningless.
var ignoreForUnIgnoreParameters = map[hmenum.Parameter]struct{}{
	hmenum.ParameterConfigPending: {},
	hmenum.ParameterStickyUnreach: {},
	hmenum.ParameterUnreach:       {},
}

// IsIgnoredForUnIgnore reports whether p must be skipped when
// building the un-ignore candidate list.
func IsIgnoredForUnIgnore(p hmenum.Parameter) bool {
	_, ok := ignoreForUnIgnoreParameters[p]
	return ok
}
