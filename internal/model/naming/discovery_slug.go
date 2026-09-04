// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package naming

import "strings"

// DiscoverySlug turns s into an HA-Discovery-safe identifier suitable
// for the `<node_id>` and `<object_id>` segments of
// `homeassistant/<component>/<node_id>/<object_id>/config` as well as
// the `unique_id` / device-identifier fields in the payload. HA only
// accepts `[A-Za-z0-9_-]+` for these segments — `:`, umlauts, spaces,
// and other punctuation that CCU names routinely carry
// (`Watchdog:_CCU-Jack`, `s0_Sensoren_Hülle_EG`, …) get HA to drop
// the discovery message with a warning, so the entity never appears.
//
// It is the one normaliser for the identifiers that carry a central
// name: per-device node ids ([PathData.DiscoveryNodeID]), hub node ids,
// and the retained-config orphan sweep that has to match them again.
// When a producer and the sweep disagree about the spelling of the same
// central, the sweep matches nothing and every retired entity keeps
// its retained config forever.
//
// Object ids do NOT go through it. [PathData.DiscoveryObjectID] takes
// the weaker [TopicSafe] route, which replaces only `+ # /` and space
// and leaves `:` and umlauts intact. That is safe exactly as long as
// object-id suffixes stay inside `[A-Za-z0-9_-]` — they are wire
// parameter names and component labels today. Routing them through
// DiscoverySlug would additionally collapse underscore runs and trim
// edges, changing published object ids and therefore retained-topic
// identity, so it is not a free tightening.
//
// Rules:
//   - German umlauts and ß are transliterated (ü→ue, ö→oe, ä→ae,
//     ß→ss) before the case fold so meaningful identifiers survive
//     the slug step (`Hülle` → `huelle` rather than `h_lle`).
//   - All remaining bytes outside `[A-Za-z0-9_-]` collapse to a single
//     `_`; runs are de-duplicated; leading/trailing `_` are trimmed.
//   - Empty input or input that reduces to "" returns "x" so callers
//     never emit a zero-length segment that HA would reject.
func DiscoverySlug(s string) string {
	if s == "" {
		return "x"
	}
	var out strings.Builder
	out.Grow(len(s))
	prevUnderscore := false
	emit := func(r rune) {
		out.WriteRune(r)
		prevUnderscore = r == '_'
	}
	flush := func() {
		if !prevUnderscore {
			out.WriteByte('_')
			prevUnderscore = true
		}
	}
	for _, r := range s {
		switch r {
		case 'ä', 'Ä':
			emit('a')
			emit('e')
		case 'ö', 'Ö':
			emit('o')
			emit('e')
		case 'ü', 'Ü':
			emit('u')
			emit('e')
		case 'ß':
			emit('s')
			emit('s')
		default:
			switch {
			case r >= 'A' && r <= 'Z':
				emit(r + ('a' - 'A'))
			case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_':
				emit(r)
			default:
				flush()
			}
		}
	}
	res := strings.Trim(out.String(), "_")
	if res == "" {
		return "x"
	}
	return res
}
