// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package masterprofile

import "math"

// MatchActiveProfile reports the ID of the master profile whose
// constraints best match the supplied `currentValues`. Mirrors
// 's `MasterProfileStore.match_active_profile`
// (`master_profile_store.py:48-86`).
//
// Algorithm (1:1 with the Python reference):
//
//  1. Look up the profile list for (deviceType, channelType). When
//     the lookup fails, return 0 (Expert / fallback).
//  2. For each non-Expert profile (id != 0) compute a score: the
//     number of "fixed" constraints whose declared value equals the
//     observed value in `currentValues`. Any mismatch on a "fixed"
//     constraint disqualifies the profile entirely (score = -1).
//     "list" constraints require the observed value to be in the
//     declared list. "range" constraints require min ≤ observed ≤ max.
//  3. The profile with the highest non-negative score wins. Ties are
//     resolved deterministically by lower profile ID.
//  4. When no profile matches, return 0.
//
// Float values are compared with [math.Abs] tolerance ≤ 1e-6 (rel_tol
// In.
//
// `currentValues` is the observed MASTER-paramset values map. Missing
// keys are ignored — a constraint on an unobserved parameter does
// not contribute to the score (mirrors the Python `if (current ...)`
// pre-check).
func (s *Store) MatchActiveProfile(deviceType, channelType string, currentValues map[string]any) int {
	profiles, err := s.Profiles(deviceType, channelType)
	if err != nil {
		return 0
	}
	bestID := 0
	bestScore := -1
	for _, p := range profiles {
		if p.ID == 0 {
			continue // Expert is always the fallback.
		}
		if len(p.Params) == 0 {
			continue
		}
		score := scoreProfile(p, currentValues)
		if score < 0 {
			continue // disqualified
		}
		if score > bestScore || (score == bestScore && p.ID < bestID) {
			bestScore = score
			bestID = p.ID
		}
	}
	return bestID
}

// scoreProfile returns a non-negative score (count of matching "fixed"
// constraints) when every constraint either matches or is unset, or -1
// when any constraint is violated. Mirrors `_score_profile`.
func scoreProfile(p Profile, currentValues map[string]any) int {
	fixedCount := 0
	for paramName, c := range p.Params {
		current, present := currentValues[paramName]
		if !present {
			continue
		}
		switch c.ConstraintType {
		case "", "fixed":
			if c.Value == nil {
				continue
			}
			if !valuesEqual(current, c.Value) {
				return -1
			}
			fixedCount++
		case "list":
			if !listContains(c, current) {
				return -1
			}
		case "range":
			if !inRange(c, current) {
				return -1
			}
		default:
			// Unknown constraint type — skip without disqualifying so
			// future extractor extensions don't break existing matching.
			continue
		}
	}
	return fixedCount
}

// ValuesEqual mirrors: floats compare
// with relative tolerance 1e-6, every other type with strict equality.
// Heterogeneous numeric pairs (int vs float) coerce to float for the
// comparison.
func valuesEqual(a, b any) bool {
	af, aOK := toFloat(a)
	bf, bOK := toFloat(b)
	if aOK && bOK {
		// Use a relative-tolerance check: |a-b| <= 1e-6 * max(|a|,|b|,1).
		scale := math.Max(math.Max(math.Abs(af), math.Abs(bf)), 1.0)
		return math.Abs(af-bf) <= 1e-6*scale
	}
	return a == b
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

// listContains reports whether the constraint's `Value` (decoded as a
// list / array) contains the observed value. The upstream JSON encodes
// list-typed constraints either as a plain array under `Value` or as
// the constraint Value being one of the list members already. Both
// shapes are handled.
func listContains(c ParamConstraint, current any) bool {
	switch v := c.Value.(type) {
	case []any:
		for _, item := range v {
			if valuesEqual(item, current) {
				return true
			}
		}
		return false
	default:
		// Single value treated as a degenerate one-element list.
		return valuesEqual(v, current)
	}
}

// inRange reports whether the observed value sits within the
// `[min, max]` range encoded as either a 2-element list or a map
// `{"min": …, "max": …}`. Numeric coercion follows [valuesEqual].
func inRange(c ParamConstraint, current any) bool {
	cf, ok := toFloat(current)
	if !ok {
		return false
	}
	switch v := c.Value.(type) {
	case []any:
		if len(v) != 2 {
			return false
		}
		minV, ok1 := toFloat(v[0])
		maxV, ok2 := toFloat(v[1])
		if !ok1 || !ok2 {
			return false
		}
		return cf >= minV && cf <= maxV
	case map[string]any:
		minV, ok1 := toFloat(v["min"])
		maxV, ok2 := toFloat(v["max"])
		if !ok1 || !ok2 {
			return false
		}
		return cf >= minV && cf <= maxV
	default:
		return false
	}
}
