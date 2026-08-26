// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// activationRule decides whether a wire value counts as an activation
// for one enrolled sensor.
//
// With no configured labels it reproduces the historical rule exactly —
// booleans map directly, numbers activate on non-zero — so an existing
// enrollment never changes meaning. With labels it admits only those
// values, which is what keeps an enumerated parameter from reporting
// the alarm system's own output back as an input.
//
// Both the live event path and the restore path resolve through this
// one type. They used to carry separate copies of the rule
// (paramValueActive and normalizeActive), and a divergence between them
// would mean a sensor reads active while running and inactive after a
// restart, or the reverse.
type activationRule struct {
	// labels are the configured active value labels. Empty selects the
	// historical rule.
	labels []string
}

// configured reports whether the rule narrows activation at all.
func (r activationRule) configured() bool { return len(r.labels) > 0 }

// matches reports whether label is one of the configured active values.
// Matching is exact: a value list is a fixed vocabulary, and a
// case-insensitive match would silently accept a label the device never
// emits.
func (r activationRule) matches(label string) bool {
	for _, l := range r.labels {
		if l == label {
			return true
		}
	}
	return false
}

// sensorBinding is the routing entry of one enrolled data point.
type sensorBinding struct {
	// id is the enrolled sensor row ID.
	id string
	// rule narrows which values count as an activation.
	rule activationRule
	// centralName, interfaceID, channelAddress and parameter identify
	// the data point, so the enum resolution can find its value list.
	centralName    string
	interfaceID    string
	channelAddress string
	parameter      string
}

// enumResolver maps enumeration indices onto labels for enrolled
// sensors that configure active values.
//
// Resolution is lazy rather than done at index-build time: the index is
// rebuilt on start and on config change, but a central may attach later
// than either, and a value list that is missing at build time would
// then stay missing for the process lifetime. Looking it up on first
// use and caching the answer keeps the hot path a map lookup without
// depending on central-readiness ordering.
type enumResolver struct {
	reg *central.Registry

	mu sync.RWMutex
	// byKey caches the declared value list per data-point routing key.
	// A nil entry records "unavailable", which is a cached answer too.
	byKey map[string][]string
	// warned records the keys already logged as unresolvable, so a
	// chattering sensor cannot flood the log.
	warned map[string]bool
}

func newEnumResolver(reg *central.Registry) *enumResolver {
	return &enumResolver{reg: reg, byKey: map[string][]string{}, warned: map[string]bool{}}
}

// reset drops the cache. Called whenever the enrollment index is
// rebuilt, so a re-enrolled sensor cannot keep a stale value list.
func (e *enumResolver) reset() {
	e.mu.Lock()
	e.byKey = map[string][]string{}
	e.warned = map[string]bool{}
	e.mu.Unlock()
}

// valueList returns the parameter's declared value list, caching the
// answer — including a negative one, so a device that never publishes a
// list is not re-queried on every event.
func (e *enumResolver) valueList(b sensorBinding) []string {
	key := dpKey(b.centralName, b.interfaceID, b.channelAddress, b.parameter)
	e.mu.RLock()
	cached, hit := e.byKey[key]
	e.mu.RUnlock()
	if hit {
		return cached
	}
	list := e.lookup(b)
	e.mu.Lock()
	e.byKey[key] = list
	e.mu.Unlock()
	return list
}

// lookup reads the value list from the model. It returns nil when the
// central, channel, parameter or value list is unavailable.
func (e *enumResolver) lookup(b sensorBinding) []string {
	if e.reg == nil {
		return nil
	}
	u, ok := e.reg.Get(b.centralName)
	if !ok {
		return nil
	}
	ch := u.GetChannel(b.channelAddress)
	if ch == nil {
		return nil
	}
	p := ch.Parameter(hmenum.Parameter(b.parameter))
	if p == nil {
		return nil
	}
	return p.ParameterData().ValueList
}

// shouldWarn reports whether an unresolvable data point should be
// logged, and marks it as logged.
func (e *enumResolver) shouldWarn(key string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.warned[key] {
		return false
	}
	e.warned[key] = true
	return true
}

// resolveActive applies a rule to a raw wire value. valueList maps an
// enumeration index onto its label; an empty list means no mapping is
// available.
//
// The fallback when a configured rule cannot be applied — an
// enumeration whose value list is unavailable — is the historical rule,
// not "inactive". For an intrusion sensor that direction produces a
// false alarm; the other direction produces a hazard detector that
// silently never fires. Callers surface the condition in the log rather
// than merely surviving it.
//
// resolved reports whether the configured rule was actually applied, so
// a caller can tell a deliberate verdict from a fallback.
func resolveActive(rule activationRule, raw any, valueList []string) (activeNow, known, resolved bool) {
	if !rule.configured() {
		activeNow, known = normalizeActive(raw)
		return activeNow, known, true
	}
	if label, ok := raw.(string); ok {
		return rule.matches(label), true, true
	}
	idx, isInt := rawInt(raw)
	if !isInt || len(valueList) == 0 {
		// A configured value list on a non-enumerated parameter is a
		// misconfiguration, and a missing list is a model gap; neither
		// is a reason to stop reporting.
		activeNow, known = normalizeActive(raw)
		return activeNow, known, false
	}
	// An index outside the declared list cannot be an intended active
	// value. Reporting it inactive is safe because the list is
	// exhaustive by construction. Scanning rather than indexing keeps
	// the bound self-evident; a value list has a handful of entries.
	for i, label := range valueList {
		if i == idx {
			return rule.matches(label), true, true
		}
	}
	return false, true, true
}

// rawInt narrows the integer wire kinds onto an index.
func rawInt(raw any) (int, bool) {
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

// active decides whether a wire value is an activation for the binding.
func (s *Service) active(b sensorBinding, v hmtypes.ParamValue) (activeNow, known bool) {
	if !b.rule.configured() {
		return paramValueActive(v)
	}
	activeNow, known, resolved := resolveActive(b.rule, v.Unwrap(), s.enums.valueList(b))
	if !resolved {
		key := dpKey(b.centralName, b.interfaceID, b.channelAddress, b.parameter)
		if s.enums.shouldWarn(key) {
			s.log.Warn("alarm sensor active_values unresolvable: no value list for the parameter, falling back to the default rule",
				"sensor", b.id, "central", b.centralName,
				"channel", b.channelAddress, "parameter", b.parameter)
		}
	}
	return activeNow, known
}
