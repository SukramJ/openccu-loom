// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Combined-Parameter wire-shape conversion. Mirrors the Python
// Reference in
//
//   - COMBINED_PARAMETER (HmIP thermostats): CSV with KEY=VALUE
//     pairs, e.g. "L=100,L2=50". Keys map to LEVEL / LEVEL_2.
//   - LEVEL_COMBINED (cover/dimmer with ramp): comma-separated
//     hex (or decimal fallback) pair, e.g. "0x64,0x32". Splits
//     into LEVEL + LEVEL_SLATS.
//
// Behaviour deliberately mimics:1 — including the
// silent-fallback paths and the "no comma → empty result" rule for
// LEVEL_COMBINED — so contract tests against the Python output match.

// Combined-parameter shorthand keys + their canonical Parameter names.
//
// The parameter names are taken from [hmenum] rather than written out
// again: the same membership rule is stated on the write path
// (internal/parameter.ConvertableParameters), and two independently typed
// copies of one rule can drift without either side failing to compile.
const (
	parameterCombined      = string(hmenum.ParameterCombinedParameter)
	parameterLevelCombined = string(hmenum.ParameterLevelCombined)

	parameterLevel      = string(hmenum.ParameterLevel)
	parameterLevel2     = string(hmenum.ParameterLevel2)
	parameterLevelSlats = string(hmenum.ParameterLevelSlats)

	combinedShortLevel  = "L"
	combinedShortLevel2 = "L2"
)

// CombinedParameters is the set of parameters this package decomposes on the
// callback path, exported so the membership can be compared against the write
// path's own set rather than sampled.
//
// It has to hold the same members as internal/parameter.ConvertableParameters:
// that set decides whether a write is decomposed into the command tracker,
// this one decides whether the callback coming back is decomposed into
// constituent data points. A parameter listed on one side only is written as
// sub-parameters and never reassembled, or reassembled from a value that was
// never tracked.
var CombinedParameters = []hmenum.Parameter{
	hmenum.ParameterCombinedParameter,
	hmenum.ParameterLevelCombined,
}

// IsCombinedParameter reports whether name designates a wire shape
// that needs structural decomposition before reaching the model
// layer. Only two parameters are combined; the rest pass through
// untouched.
func IsCombinedParameter(name string) bool {
	return name == parameterCombined || name == parameterLevelCombined
}

// ParseCombinedParameter is the decode a running daemon uses for
// COMBINED_PARAMETER and LEVEL_COMBINED callbacks: the callback handler gates
// on [IsCombinedParameter] and calls this, and the command tracker resolves
// optimistic values through it. internal/model/value carries a second,
// exported CPV decode pair with the same scale constants but a different
// parser; the two do not agree on every input (a decimal LEVEL_COMBINED byte
// yields a float there and passes through as a string here), so a caller must
// not treat them as interchangeable.
//
// ParseCombinedParameter parses a CCU combined-parameter wire string
// into the resulting paramset map. Returns ok=false (and a nil map)
// When the value cannot be parsed — mirrors
// "silent fail with empty dict" contract so that downstream
// processing simply drops the update.
//
// Supported parameters:
//
//   - "COMBINED_PARAMETER" → {LEVEL: float, LEVEL_2: float}
//   - "LEVEL_COMBINED"     → {LEVEL: float, LEVEL_SLATS: float}
//
// Any other name yields (nil, false).
func ParseCombinedParameter(name, value string) (map[string]any, bool) {
	switch name {
	case parameterCombined:
		return parseCombined(value)
	case parameterLevelCombined:
		return parseLevelCombined(value)
	default:
		return nil, false
	}
}

// parseCombined handles "L=100,L2=50"-style HmIP thermostat values.
// Each shorthand key (`L`, `L2`) maps to a canonical Parameter and
// the numeric component is divided by 100 (HmIP percent → 0..1
// float). Unknown shortcuts are silently dropped. Any malformed
// pair (missing `=`, non-numeric value, …) aborts the whole parse
// and returns (nil, false).
func parseCombined(value string) (map[string]any, bool) {
	if value == "" {
		return nil, false
	}
	out := make(map[string]any, 2)
	for pair := range strings.SplitSeq(value, ",") {
		before, after, ok0 := strings.Cut(pair, "=")
		if !ok0 {
			return nil, false
		}
		short, raw := before, after
		canonical := combinedShortToParameter(short)
		if canonical == "" {
			// Unknown shorthand — ignore (matches python's silent
			// dict.get() miss).
			continue
		}
		v, ok := convertCpvLevelHmip(raw)
		if !ok {
			return nil, false
		}
		out[canonical] = v
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// parseLevelCombined handles "0x64,0x32"-style HM cover/dimmer
// values. Both halves go through the hex→float converter, with the
// non-hex branch returning the raw string (matching python). The
// "no comma → empty dict" quirk is preserved verbatim, as is the
// "exactly two parts" requirement (python's `l1, l2 = split(",")`
// raises ValueError on any other count, which the outer try/except
// converts to an empty result).
func parseLevelCombined(value string) (map[string]any, bool) {
	if !strings.Contains(value, ",") {
		return nil, false
	}
	parts := strings.Split(value, ",")
	if len(parts) != 2 {
		return nil, false
	}
	l1 := convertCpvLevelHm(parts[0])
	l2 := convertCpvLevelHm(parts[1])
	return map[string]any{
		parameterLevel:      l1,
		parameterLevelSlats: l2,
	}, true
}

// combinedShortToParameter maps the wire-side shortcut of a
// combined-parameter pair to its canonical Parameter name. Unknown
// shortcuts return "" so callers can choose to skip them.
func combinedShortToParameter(short string) string {
	switch short {
	case combinedShortLevel:
		return parameterLevel
	case combinedShortLevel2:
		return parameterLevel2
	default:
		return ""
	}
}

// convertCpvLevelHmip parses an HmIP-style level: a decimal integer
// where 0..100 maps to 0..1 float. A non-numeric input yields
// (nil, false), aborting the surrounding combined-parameter parse —
// the python implementation lets `int(value)` raise and the outer
// try/except swallows it into an empty dict.
func convertCpvLevelHmip(value string) (any, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return nil, false
	}
	return float64(n) / 100, true
}

// convertCpvLevelHm parses an HM-style hex level: 0xNN where the
// numeric value divided by 200 maps to 0..1 float. Non-hex input
// silently falls back to the raw string — this mirrors the python
// branch `if not value.startswith('0x'): return value`. Callers
// receive a string in that case and must be prepared for it.
func convertCpvLevelHm(value string) any {
	v := strings.TrimSpace(value)
	if !strings.HasPrefix(v, "0x") && !strings.HasPrefix(v, "0X") {
		return v
	}
	n, err := strconv.ParseUint(v[2:], 16, 64)
	if err != nil {
		// Hex prefix but unparseable — match python's outer
		// try/except: treat as silent failure by returning the raw
		// value through. The caller still produces a dict, just
		// with a string in this slot.
		return v
	}
	return float64(n) / 100 / 2
}
