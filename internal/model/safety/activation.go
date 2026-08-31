// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package safety

// ActivationResolution reports how [ActiveFromRaw] reached its verdict.
//
// The two non-applied outcomes are kept apart because they call for
// opposite reactions: a missing value list is a model gap that resolves
// itself once the parameter is hydrated, while an index the declared
// list does not cover is a device speaking a vocabulary this daemon
// does not know. Collapsing both into one "unresolved" flag makes the
// transient case log the permanent case's message forever.
// loom:reachable:reason="the third return of ActiveFromRaw, carried through internal/security/subscribe.go sourceActive and warnUnresolvedActivation and through the alarm engine's activation path, where it selects the unresolved-value log line; a numeric type whose methods production never calls, which the analyzer's type heuristic cannot see used"
type ActivationResolution int

const (
	// ActivationApplied means the rule decided the value as configured.
	ActivationApplied ActivationResolution = iota
	// ActivationNoValueList means active values are configured but the
	// value could not be mapped onto a label — an integer index with no
	// declared value list, or a value of a shape an enumeration never
	// takes. The verdict is the default rule's, not the configured
	// one's.
	ActivationNoValueList
	// ActivationIndexOutOfRange means the value is an integer index the
	// declared value list does not cover. The verdict is inactive.
	ActivationIndexOutOfRange
)

// ActiveFromRaw is the domain's single sensor-activation rule: it
// decides whether one raw wire value of a safety-relevant data point
// counts as an activation.
//
// activeValues names the labels that count as active — the operator's
// enrolment selection where there is one, otherwise
// [Classification.ActiveValues]. An empty set selects the default rule:
// booleans map directly, numbers activate on non-zero.
//
// valueList is the parameter's declared enumeration vocabulary, which
// maps an integer wire index onto its label. An index the list does not
// cover is inactive: the list is exhaustive by construction, and a
// value outside it must not raise an alarm — the concrete case is
// INTRUSION_ALARM inside SMOKE_DETECTOR_ALARM_STATUS (see
// [smokeActiveValues]), where "not index 0" reads the installation's
// own siren command as a fire.
//
// The exhaustiveness is deliberately partial: only *integer* indices are
// narrowed against the list. A float value carries no index semantics on
// any parameter this domain classifies, so it stays on the default rule
// rather than being silently reinterpreted as an enumeration position.
//
// known reports whether the value has activation semantics at all; a
// caller must discard the event when it is false rather than treat it
// as inactive.
func ActiveFromRaw(activeValues []string, raw any, valueList []string) (active, known bool, res ActivationResolution) {
	if len(activeValues) == 0 {
		// An unconfigured rule is applied, not fallen back from: the
		// default rule is the answer here, and a caller must not log it
		// as unresolved.
		active, known = normalizeActive(raw)
		return active, known, ActivationApplied
	}
	if label, ok := raw.(string); ok {
		// Matching is exact: a value list is a fixed vocabulary, and a
		// case-insensitive match would silently accept a label the
		// device never emits.
		return containsLabel(activeValues, label), true, ActivationApplied
	}
	idx, isIndex := rawIndex(raw)
	if !isIndex || len(valueList) == 0 {
		active, known = normalizeActive(raw)
		return active, known, ActivationNoValueList
	}
	// Scanning rather than indexing keeps the bound self-evident and
	// covers a negative index without a second check; a value list has
	// a handful of entries.
	for i, label := range valueList {
		if i == idx {
			return containsLabel(activeValues, label), true, ActivationApplied
		}
	}
	return false, true, ActivationIndexOutOfRange
}

// rawIndex narrows the integer wire kinds onto an enumeration index.
// The live event path delivers `int`, while the restore and index-seed
// paths read the model back as int32, so both arms are load-bearing.
func rawIndex(raw any) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	default:
		return 0, false
	}
}

// normalizeActive is the default activation rule: booleans map directly
// (open/motion true), numbers activate on non-zero — the rotary-handle
// shape, where 0 is closed and every other position is not.
func normalizeActive(raw any) (active, known bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case int:
		return v != 0, true
	case int32:
		return v != 0, true
	case int64:
		return v != 0, true
	case float64:
		return v != 0, true
	default:
		return false, false
	}
}

// containsLabel reports whether want is one of list's entries.
func containsLabel(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
