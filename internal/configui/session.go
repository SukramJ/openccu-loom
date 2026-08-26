// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package configui

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// undoEntry stores one change for the undo/redo stacks.
type undoEntry struct {
	Parameter string
	OldValue  any
	NewValue  any
}

// ParameterChange describes one diff between the session's initial
// and current value. Returned by [Session.ChangedParameters] for the
// detailed change list a UI may render alongside the save button.
type ParameterChange struct {
	Parameter string
	From      any
	To        any
}

// Session tracks the in-progress edits a user makes against a channel's
// paramset.
//
// Concurrency: the session is safe to call from multiple goroutines. In
// practice the WebSocket layer serialises all calls per connection anyway,
// but the lock prevents accidental races when the session is shared between a
// save handler and an auxiliary diff endpoint.
type Session struct {
	mu            sync.Mutex
	descriptions  map[string]hmproto.ParameterData
	initialValues map[string]any
	currentValues map[string]any
	undoStack     []undoEntry
	redoStack     []undoEntry
}

// NewSession constructs a session against the given paramset
// descriptions and a snapshot of current CCU values. Both maps are
// copied — the caller may continue to mutate the originals freely.
func NewSession(descriptions map[string]hmproto.ParameterData, initialValues map[string]any) *Session {
	descCopy := make(map[string]hmproto.ParameterData, len(descriptions))
	for k := range descriptions {
		descCopy[k] = descriptions[k]
	}
	curCopy := make(map[string]any, len(initialValues))
	initCopy := make(map[string]any, len(initialValues))
	for k, v := range initialValues {
		curCopy[k] = v
		initCopy[k] = v
	}
	return &Session{
		descriptions:  descCopy,
		initialValues: initCopy,
		currentValues: curCopy,
	}
}

// CanUndo reports whether [Undo] would change anything.
func (s *Session) CanUndo() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.undoStack) > 0
}

// CanRedo reports whether [Redo] would change anything.
func (s *Session) CanRedo() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.redoStack) > 0
}

// valuesEqual compares two paramset values without assuming they are
// comparable. CCU paramsets only ever carry scalars, but the value of a
// staged change reaches the session verbatim from a WebSocket client, so a
// JSON array or object arrives as an uncomparable []any / map[string]any.
// Go's == panics on two of those, which used to kill the connection
// mid-frame; deep equality is the only safe answer for them.
func valuesEqual(a, b any) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ta, tb := reflect.TypeOf(a), reflect.TypeOf(b)
	if ta != tb {
		return false
	}
	if !ta.Comparable() {
		return reflect.DeepEqual(a, b)
	}
	return a == b
}

// IsDirty reports whether the current values differ from the initial
// snapshot.
func (s *Session) IsDirty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dirtyLocked()
}

func (s *Session) dirtyLocked() bool {
	if len(s.currentValues) != len(s.initialValues) {
		return true
	}
	for k, v := range s.currentValues {
		if iv, ok := s.initialValues[k]; !ok || !valuesEqual(iv, v) {
			return true
		}
	}
	return false
}

// CurrentValue returns the latest value the session holds for
// parameter, or nil when the parameter is not part of the session.
func (s *Session) CurrentValue(parameter string) any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.currentValues[parameter]
}

// Set records a parameter change and pushes it onto the undo stack.
// Calling Set with the value already present is a no-op (mirrors the
// Python reference). Setting a value diverges from any pending redo
// chain — the redo stack is cleared.
func (s *Session) Set(parameter string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.currentValues[parameter]
	if ok && valuesEqual(old, value) {
		return
	}
	s.undoStack = append(s.undoStack, undoEntry{Parameter: parameter, OldValue: old, NewValue: value})
	s.redoStack = nil
	s.currentValues[parameter] = value
}

// Undo reverts the most recent [Set] and reports whether an undo was
// performed. The reverted change moves to the redo stack.
func (s *Session) Undo() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.undoStack) == 0 {
		return false
	}
	entry := s.undoStack[len(s.undoStack)-1]
	s.undoStack = s.undoStack[:len(s.undoStack)-1]
	s.redoStack = append(s.redoStack, entry)
	s.currentValues[entry.Parameter] = entry.OldValue
	return true
}

// Redo re-applies the most recent [Undo] and reports whether a redo
// was performed.
func (s *Session) Redo() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.redoStack) == 0 {
		return false
	}
	entry := s.redoStack[len(s.redoStack)-1]
	s.redoStack = s.redoStack[:len(s.redoStack)-1]
	s.undoStack = append(s.undoStack, entry)
	s.currentValues[entry.Parameter] = entry.NewValue
	return true
}

// Discard reverts the entire session to its initial state and clears
// both stacks. Used when the user cancels editing.
func (s *Session) Discard() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.currentValues = make(map[string]any, len(s.initialValues))
	maps.Copy(s.currentValues, s.initialValues)
	s.undoStack = nil
	s.redoStack = nil
}

// InitialValues returns a shallow copy of the paramset values the session
// was opened with. Used by the save path to record before/after diffs.
func (s *Session) InitialValues() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]any, len(s.initialValues))
	maps.Copy(out, s.initialValues)
	return out
}

// Changes returns the parameters that differ from their initial
// values, in the wire shape suitable for `Interface.putParamset`.
// Unchanged parameters are excluded so the CCU only sees the
// minimal patch.
func (s *Session) Changes() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]any)
	for k, v := range s.currentValues {
		if iv, ok := s.initialValues[k]; !ok || !valuesEqual(iv, v) {
			out[k] = v
		}
	}
	return out
}

// ChangedParameters returns the detailed before/after diff. Used by
// the UI to render the "you are about to change …" preview.
func (s *Session) ChangedParameters() []ParameterChange {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ParameterChange, 0)
	for k, cur := range s.currentValues {
		init, ok := s.initialValues[k]
		if !ok || !valuesEqual(init, cur) {
			out = append(out, ParameterChange{Parameter: k, From: init, To: cur})
		}
	}
	return out
}

// ApplyPreset applies a map of preset values to the session. Each
// key/value pair is forwarded to [Set]; unknown parameters are silently
// skipped (best-effort, no atomicity). All successfully applied values
// are pushed onto the undo stack individually and are therefore
// individually undoable.
//
// Mirrors the SPA's preset-application loop (channel-config.ts): iterate
// values, call session.set per parameter, do not roll back already-applied
// changes on a partial failure.
//
// ApplyPreset has no error return: [Set] never fails (it is a pure map
// write) and an unknown key is a deliberate, silent skip rather than a
// validation error — matching the frontend's best-effort behaviour. If a
// future preset source needs per-key validation (e.g. range-checking a
// value before it is applied), add that check here and only then
// reintroduce an error return.
func (s *Session) ApplyPreset(values map[string]any) {
	for key, value := range values {
		s.mu.Lock()
		_, known := s.descriptions[key]
		s.mu.Unlock()
		if !known {
			continue
		}
		s.Set(key, value)
	}
}

// ResetToDefaults walks every parameter in the session and sets it to its
// DEFAULT (when the descriptor declares one). Each successful reset goes
// through [Set] so it remains undoable.
func (s *Session) ResetToDefaults() {
	s.mu.Lock()
	pairs := make([]struct {
		Param string
		Value any
	}, 0)
	for param := range s.descriptions {
		def, ok := defaultValue(s.descriptions[param])
		if !ok {
			continue
		}
		if _, present := s.currentValues[param]; !present {
			continue
		}
		pairs = append(pairs, struct {
			Param string
			Value any
		}{param, def})
	}
	s.mu.Unlock()
	for _, p := range pairs {
		s.Set(p.Param, p.Value)
	}
}

// ValidationIssue describes one validation failure.
type ValidationIssue struct {
	Parameter string
	Reason    string
}

// Validate validates all current values against their descriptors. It returns
// all issues found — both single-parameter (bound, type, enum-member) and
// cross-parameter (CrossValidationConstraints). An empty slice means all
// values are valid.
func (s *Session) Validate(constraints []CrossValidationConstraint) []ValidationIssue {
	s.mu.Lock()
	values := make(map[string]any, len(s.currentValues))
	maps.Copy(values, s.currentValues)
	descs := s.descriptions
	s.mu.Unlock()
	return validateAll(descs, values, values, constraints)
}

// ValidateCrossConstraints evaluates only the cross-parameter validation
// constraints against the current session state. Per-parameter checks
// (bounds, types, enum membership) are NOT performed — callers that
// need both should call [Validate] instead.
//
// Used by the `config.session.save` WS handler to enforce
// easymode cross-validation rules without requiring the session to carry
// typed parameter descriptors (the WS layer uses opaque map[string]any
// descriptions).
//
// Returns nil when constraints is empty or all rules pass.
func (s *Session) ValidateCrossConstraints(constraints []CrossValidationConstraint) []ValidationIssue {
	if len(constraints) == 0 {
		return nil
	}
	s.mu.Lock()
	current := make(map[string]any, len(s.currentValues))
	maps.Copy(current, s.currentValues)
	s.mu.Unlock()
	seen := make(map[string]bool)
	var issues []ValidationIssue
	for i := range constraints {
		subject := crossSubject(constraints[i])
		if seen[subject] {
			continue
		}
		if reason := evaluateCross(constraints[i], current); reason != "" {
			issues = append(issues, ValidationIssue{Parameter: subject, Reason: reason})
			seen[subject] = true
		}
	}
	return issues
}

// ValidateChanges validates only the parameters that differ from
// their initial values. Cross-parameter rules are evaluated against
// the full current state (changed + unchanged), not just the diff.
func (s *Session) ValidateChanges(constraints []CrossValidationConstraint) []ValidationIssue {
	s.mu.Lock()
	changes := make(map[string]any)
	for k, v := range s.currentValues {
		if iv, ok := s.initialValues[k]; !ok || !valuesEqual(iv, v) {
			changes[k] = v
		}
	}
	current := make(map[string]any, len(s.currentValues))
	maps.Copy(current, s.currentValues)
	descs := s.descriptions
	s.mu.Unlock()
	if len(changes) == 0 {
		return nil
	}
	return validateAll(descs, changes, current, constraints)
}

// validateAll performs both per-parameter and cross-parameter
// validation and returns all issues found.
//
// - paramValues: the parameters to validate individually.
// - fullCurrent: the merged state used for cross-parameter evaluation
// (equals paramValues for Validate; equals all current values for
// ValidateChanges so unchanged params are still visible to rules).
func validateAll(
	descs map[string]hmproto.ParameterData,
	paramValues map[string]any,
	fullCurrent map[string]any,
	constraints []CrossValidationConstraint,
) []ValidationIssue {
	seen := make(map[string]bool)
	var issues []ValidationIssue
	for param, value := range paramValues {
		pd, ok := descs[param]
		if !ok {
			issues = append(issues, ValidationIssue{Parameter: param, Reason: fmt.Sprintf("unknown parameter %q", param)})
			seen[param] = true
			continue
		}
		if reason := validateValue(pd, value); reason != "" {
			issues = append(issues, ValidationIssue{Parameter: param, Reason: reason})
			seen[param] = true
		}
	}
	// Cross-parameter constraints: evaluate against fullCurrent.
	for i := range constraints {
		subject := crossSubject(constraints[i])
		if seen[subject] {
			continue // already reported
		}
		if reason := evaluateCross(constraints[i], fullCurrent); reason != "" {
			issues = append(issues, ValidationIssue{Parameter: subject, Reason: reason})
			seen[subject] = true
		}
	}
	return issues
}

// validateValue checks a single value against its descriptor.
// Returns a non-empty reason string when validation fails.
func validateValue(pd hmproto.ParameterData, value any) string {
	switch pd.Type { //nolint:exhaustive // Dummy and Empty are non-writable types; any value is accepted
	case hmenum.ParameterTypeAction:
		return "" // write-only trigger; any value is acceptable

	case hmenum.ParameterTypeBool:
		if _, ok := value.(bool); !ok {
			return fmt.Sprintf("expected bool, got %T", value)
		}

	case hmenum.ParameterTypeInteger, hmenum.ParameterTypeFloat:
		var fv float64
		switch x := value.(type) {
		case float64:
			fv = x
		case float32:
			fv = float64(x)
		case int:
			fv = float64(x)
		case int32:
			fv = float64(x)
		case int64:
			fv = float64(x)
		default:
			return fmt.Sprintf("expected numeric value, got %T", value)
		}
		if len(pd.Min) > 0 {
			var minVal float64
			if err := json.Unmarshal(pd.Min, &minVal); err == nil && fv < minVal {
				return fmt.Sprintf("value %v is below minimum %v", value, minVal)
			}
		}
		if len(pd.Max) > 0 {
			var maxVal float64
			if err := json.Unmarshal(pd.Max, &maxVal); err == nil && fv > maxVal {
				return fmt.Sprintf("value %v is above maximum %v", value, maxVal)
			}
		}

	case hmenum.ParameterTypeString:
		if _, ok := value.(string); !ok {
			return fmt.Sprintf("expected string, got %T", value)
		}

	case hmenum.ParameterTypeEnum:
		switch x := value.(type) {
		case int, int32, int64, float64:
			var idx int
			switch i := x.(type) {
			case int:
				idx = i
			case int32:
				idx = int(i)
			case int64:
				idx = int(i)
			case float64:
				idx = int(i)
			}
			if len(pd.ValueList) > 0 && (idx < 0 || idx >= len(pd.ValueList)) {
				return fmt.Sprintf("enum index %d out of range [0, %d]", idx, len(pd.ValueList)-1)
			}
		case string:
			if len(pd.ValueList) > 0 {
				found := slices.Contains(pd.ValueList, x)
				if !found {
					return fmt.Sprintf("value %q not in VALUE_LIST", x)
				}
			}
		default:
			return fmt.Sprintf("expected int or string for ENUM, got %T", value)
		}
	}
	return ""
}

// evaluateCross checks one cross-parameter constraint against a value
// map. Returns a non-empty reason string on violation.
func evaluateCross(c CrossValidationConstraint, values map[string]any) string {
	// Require all referenced parameters to be present.
	for _, p := range c.AppliesToParams {
		if _, ok := values[p]; !ok {
			return "" // partial state — skip
		}
	}
	toFloat := func(k string) (float64, bool) {
		v, ok := values[k]
		if !ok || v == nil {
			return 0, false
		}
		switch x := v.(type) {
		case float64:
			return x, true
		case float32:
			return float64(x), true
		case int:
			return float64(x), true
		case int32:
			return float64(x), true
		case int64:
			return float64(x), true
		}
		return 0, false
	}
	switch c.Rule {
	case CrossValidationRuleGTE:
		a, aOK := toFloat(c.ParamA)
		b, bOK := toFloat(c.ParamB)
		if aOK && bOK && a < b {
			return fmt.Sprintf("%s (%v) must be >= %s (%v)", c.ParamA, a, c.ParamB, b)
		}
	case CrossValidationRuleLTE:
		a, aOK := toFloat(c.ParamA)
		b, bOK := toFloat(c.ParamB)
		if aOK && bOK && a > b {
			return fmt.Sprintf("%s (%v) must be <= %s (%v)", c.ParamA, a, c.ParamB, b)
		}
	case CrossValidationRuleNotEqual:
		a, aOK := toFloat(c.ParamA)
		b, bOK := toFloat(c.ParamB)
		if aOK && bOK && a == b {
			return fmt.Sprintf("%s (%v) must differ from %s", c.ParamA, a, c.ParamB)
		}
	case CrossValidationRuleBetween:
		v, vOK := toFloat(c.Param)
		mn, mnOK := toFloat(c.MinParam)
		mx, mxOK := toFloat(c.MaxParam)
		if vOK && mnOK && mxOK && (v < mn || v > mx) {
			return fmt.Sprintf("%s (%v) must be between %s (%v) and %s (%v)",
				c.Param, v, c.MinParam, mn, c.MaxParam, mx)
		}
	default:
		// An unrecognised rule must not be mistaken for "no violation" —
		// that would let a typo'd rule name in the embedded metadata
		// archives silently disable a constraint the operator believes
		// is being enforced.
		return fmt.Sprintf("unknown cross-validation rule %q", c.Rule)
	}
	return ""
}

// crossSubject returns the primary parameter name for a
// CrossValidationConstraint (used to deduplicate issue reporting).
func crossSubject(c CrossValidationConstraint) string {
	if c.Param != "" {
		return c.Param
	}
	if c.ParamA != "" {
		return c.ParamA
	}
	if len(c.AppliesToParams) > 0 {
		return c.AppliesToParams[0]
	}
	return ""
}

// defaultValue extracts the descriptor's DEFAULT field as a Go value.
// The hmproto.ParameterData carries it as a json.RawMessage, so we
// decode here. Returns (nil, false) when there is no default.
func defaultValue(pd hmproto.ParameterData) (any, bool) {
	if len(pd.Default) == 0 {
		return nil, false
	}
	var v any
	if err := json.Unmarshal(pd.Default, &v); err != nil {
		return nil, false
	}
	return v, true
}
