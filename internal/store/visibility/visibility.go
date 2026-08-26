// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Decision is the verdict for one (model, parameter) pair.
type Decision int

// Decision values.
const (
	DecisionAllow Decision = iota
	DecisionHide           // explicitly hidden by a rule
)

// Rules holds the built-in parameter filter. It is safe for
// concurrent use.
type Rules struct {
	mu            sync.RWMutex
	hiddenGlobal  map[hmenum.Parameter]struct{}
	hiddenByModel map[string]map[hmenum.Parameter]struct{}
}

// NewRules returns a [Rules] pre-populated with the built-in hides.
// The set merges the legacy builtInGlobalHides list with the full
// list takes precedence on any conflict.
func NewRules() *Rules {
	r := &Rules{
		hiddenGlobal:  make(map[hmenum.Parameter]struct{}),
		hiddenByModel: make(map[string]map[hmenum.Parameter]struct{}),
	}
	for _, p := range builtInGlobalHides() {
		r.hiddenGlobal[p] = struct{}{}
	}
	// Merge the full HIDDEN_PARAMETERS set from rules.go.
	for p := range hiddenParameters {
		r.hiddenGlobal[p] = struct{}{}
	}
	return r
}

// HideGlobal marks a parameter as hidden for every device model.
func (r *Rules) HideGlobal(p hmenum.Parameter) {
	r.mu.Lock()
	r.hiddenGlobal[p] = struct{}{}
	r.mu.Unlock()
}

// HideForModel marks a parameter as hidden for a specific CCU model
// ("HmIP-BROLL", "HmIP-BSM", …).
func (r *Rules) HideForModel(model string, p hmenum.Parameter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	inner, ok := r.hiddenByModel[model]
	if !ok {
		inner = make(map[hmenum.Parameter]struct{})
		r.hiddenByModel[model] = inner
	}
	inner[p] = struct{}{}
}

// Evaluate reports the decision for (model, parameter).
func (r *Rules) Evaluate(model string, p hmenum.Parameter) Decision {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.hiddenGlobal[p]; ok {
		return DecisionHide
	}
	if byModel, ok := r.hiddenByModel[model]; ok {
		if _, ok := byModel[p]; ok {
			return DecisionHide
		}
	}
	return DecisionAllow
}

// IsAllowed is a convenience wrapper around [Evaluate].
func (r *Rules) IsAllowed(model string, p hmenum.Parameter) bool {
	return r.Evaluate(model, p) == DecisionAllow
}

// BuiltInGlobalHides mirrors the common
// list. These parameters are uninteresting to downstream consumers:
// raw AES keys, direct-link flags, party-mode scratch registers.
func builtInGlobalHides() []hmenum.Parameter {
	return []hmenum.Parameter{
		hmenum.ParameterPartyModeSubmit,
		hmenum.ParameterPartyStartDay,
		hmenum.ParameterPartyStartTime,
		hmenum.ParameterPartyStopDay,
		hmenum.ParameterPartyStopTime,
		hmenum.ParameterPartyTemperature,
		hmenum.ParameterPartyTimeEnd,
		hmenum.ParameterPartyTimeStart,
		hmenum.ParameterOnTimeList1,
	}
}
