// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package alarm

import (
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/safety"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// sensorBinding is the routing entry of one enrolled data point.
type sensorBinding struct {
	// id is the enrolled sensor row ID.
	id string
	// activeValues are the operator's enrolled active value labels.
	// Empty selects the default rule.
	activeValues []string
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

// active decides whether a wire value is an activation for the binding.
//
// The rule itself lives in [safety.ActiveFromRaw] — one home for both
// this domain and the security plane, because a sensor that reads
// active in one and inactive in the other is a contradiction no
// operator can resolve. What stays here is the surfacing: a configured
// narrowing that could not be applied is logged once per data point
// rather than merely survived.
func (s *Service) active(b sensorBinding, v hmtypes.ParamValue) (activeNow, known bool) {
	if len(b.activeValues) == 0 {
		// Fast path for the unnarrowed majority: it reaches the same
		// verdict, and it skips the value-list resolution, whose cache
		// miss walks registry → unit → channel → parameter under a
		// lock for a value the rule would not consult anyway.
		activeNow, known, _ = safety.ActiveFromRaw(nil, v.Unwrap(), nil)
		return activeNow, known
	}
	activeNow, known, res := safety.ActiveFromRaw(b.activeValues, v.Unwrap(), s.enums.valueList(b))
	if res == safety.ActivationApplied {
		return activeNow, known
	}
	key := dpKey(b.centralName, b.interfaceID, b.channelAddress, b.parameter)
	if !s.enums.shouldWarn(key) {
		return activeNow, known
	}
	if res == safety.ActivationIndexOutOfRange {
		s.log.Warn("alarm sensor reported a value outside the declared value list; counted as inactive",
			"sensor", b.id, "central", b.centralName,
			"channel", b.channelAddress, "parameter", b.parameter, "value", v.Unwrap())
		return activeNow, known
	}
	s.log.Warn("alarm sensor active_values unresolvable: no value list for the parameter, falling back to the default rule",
		"sensor", b.id, "central", b.centralName,
		"channel", b.channelAddress, "parameter", b.parameter)
	return activeNow, known
}
