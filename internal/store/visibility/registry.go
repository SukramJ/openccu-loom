// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package visibility

import (
	"io"
	"maps"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Registry is the public façade
// `ParameterVisibilityRegistry`
// (`store/visibility/registry.py:52`). It bundles the static rules,
// the un_ignore overrides, the model-level validator and the
// memoising decider behind one type so consumers only depend on this
// surface.
type Registry struct {
	rules     *Rules
	model     *ModelValidator
	parameter *ParameterDecider
}

// NewRegistry constructs a registry pre-wired with default Rules,
// an empty ModelValidator, and a fresh ParameterDecider.
func NewRegistry() *Registry {
	rules := NewRules()
	return &Registry{
		rules:     rules,
		model:     NewModelValidator(),
		parameter: NewParameterDecider(rules),
	}
}

// Rules returns the underlying [*Rules]. Callers can register
// additional global / per-model hides through it.
func (r *Registry) Rules() *Rules { return r.rules }

// Model returns the underlying [*ModelValidator].
func (r *Registry) Model() *ModelValidator { return r.model }

// Parameter returns the underlying [*ParameterDecider].
func (r *Registry) Parameter() *ParameterDecider { return r.parameter }

// LoadUnIgnore parses an un_ignore stream
// and installs the resulting overrides on the parameter decider.
func (r *Registry) LoadUnIgnore(reader io.Reader) error {
	entries, err := ParseUnIgnore(reader)
	if err != nil {
		return err
	}
	r.parameter.LoadUnIgnore(entries)
	return nil
}

// Len returns the number of entries in the parameter-decider's
// memoisation cache. It satisfies the [coordinators.CacheSizeProvider]
// interface so callers that hold a [*Registry] (rather than the
// underlying [*ParameterDecider]) can wire it directly into
// [CacheCoordinator.SetSizeProviders].
func (r *Registry) Len() int { return r.parameter.Len() }

// SetRequiredParameters forwards the required-parameter whitelist to
// the underlying [*ParameterDecider]. Parameters in this set are never
// treated as ignored for the VALUES paramset, regardless of
// IGNORED_PARAMETERS or wildcard rules. The MASTER paramset whitelist is
// unaffected — it is governed by relevantMasterParamsetsByDevice.
//
// Invalidates the decider's memoisation cache.
func (r *Registry) SetRequiredParameters(params []hmenum.Parameter) {
	r.parameter.SetRequiredParameters(params)
}

// IsAllowed is the high-level question almost every caller asks:
// "Should this (model, channelType, paramset, parameter) tuple be
// exposed to the user?" Combines model-level + paramset-level +
// parameter-level checks.
//
// The channel number is unknown at this call site; the MASTER
// channel-whitelist gating uses [channelNoUnknown] which skips the
// channel-number check and only applies the nil-key wildcard from
// relevantMasterParamsetsByChannel. Callers that know the channel
// number should use [Registry.IsAllowedForChannel] for more precise
// MASTER filtering.
func (r *Registry) IsAllowed(model, channelType string, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return r.IsAllowedForChannel(model, channelType, channelNoUnknown, paramset, p)
}

// InvalidateAllCaches flushes the memoisation caches on the parameter
// decider.
func (r *Registry) InvalidateAllCaches() {
	r.parameter.LoadUnIgnore(r.parameter.UnIgnoreEntries())
}

// ClearMemoizationCaches resets only the memoisation (hit/miss) caches
// without touching the un-ignore rules.
func (r *Registry) ClearMemoizationCaches() {
	r.parameter.ClearCache()
}

// CheckIgnoreParametersIsClean reports whether the current visibility rules
// contain any contradictions — specifically, whether any parameter that is
// in the required-parameters whitelist also appears in IGNORED_PARAMETERS or
// the device-level suppress lists. Returns true when there are no conflicts.
func (r *Registry) CheckIgnoreParametersIsClean() bool {
	// Retrieve the current required parameters from the decider.
	r.parameter.mu.RLock()
	required := r.parameter.requiredParameters
	r.parameter.mu.RUnlock()
	if len(required) == 0 {
		return true // nothing to conflict with
	}
	for p := range required {
		name := string(p)
		if _, ignored := ignoredParameters[name]; ignored {
			return false
		}
		if parameterIsWildcardIgnored(name) {
			return false
		}
	}
	return true
}

// IsAllowedForChannel is like [Registry.IsAllowed] but accepts the
// concrete channel number so the MASTER channel-whitelist gating can
// make a precise decision.
func (r *Registry) IsAllowedForChannel(model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	if r.model.IsModelIgnored(model) {
		return false
	}
	if !r.model.IsRelevantParamset(model, paramset) {
		return false
	}
	return !r.parameter.IsParameterIgnored(model, channelType, channelNo, paramset, p)
}

// ParameterIsHidden reports whether a parameter is in the HIDDEN_PARAMETERS
// set — i.e. it is created as a data point but should not be shown in the UI
// by default.
func (r *Registry) ParameterIsHidden(p hmenum.Parameter) bool {
	_, ok := hiddenParameters[p]
	return ok
}

// ShouldSkipParameter is the high-level "should this parameter be excluded?"
// question asked by the model layer. It delegates to
// [ParameterDecider.ShouldSkipParameter] while additionally gating on
// model-validity.
func (r *Registry) ShouldSkipParameter(model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	if r.model.IsModelIgnored(model) {
		return true
	}
	return r.parameter.ShouldSkipParameter(model, channelType, channelNo, paramset, p)
}

// HiddenParameters returns a read-only copy of the HIDDEN_PARAMETERS set.
func HiddenParameters() map[hmenum.Parameter]struct{} {
	out := make(map[hmenum.Parameter]struct{}, len(hiddenParameters))
	maps.Copy(out, hiddenParameters)
	return out
}

// ParameterIsHiddenConst reports whether the parameter is in the built-in
// HIDDEN_PARAMETERS set (package-level function for callers that do not hold
// a *Registry).
func ParameterIsHiddenConst(p hmenum.Parameter) bool {
	_, ok := hiddenParameters[p]
	return ok
}

// RelevantMasterParamsetsByDevice returns a read-only copy of the
// device-level MASTER paramset whitelist.
func RelevantMasterParamsetsByDevice() map[string]ModelMasterEntry {
	out := make(map[string]ModelMasterEntry, len(relevantMasterParamsetsByDevice))
	maps.Copy(out, relevantMasterParamsetsByDevice)
	return out
}

// IgnoreParametersByDeviceLower returns a copy of the per-device parameter
// ignore map.
func IgnoreParametersByDeviceLower() map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{}, len(ignoreParametersByDevice))
	for param, models := range ignoreParametersByDevice {
		cp := make(map[string]struct{}, len(models))
		for m := range models {
			cp[m] = struct{}{}
		}
		out[param] = cp
	}
	return out
}

// IgnoreDevicesForDataPointEventsLower returns a read-only copy of the
// per-device event-suppression map. Keys are model names; values are the set
// of parameters for which data-point events should not be surfaced.
func IgnoreDevicesForDataPointEventsLower() map[string]map[hmenum.Parameter]struct{} {
	out := make(map[string]map[hmenum.Parameter]struct{}, len(ignoreDevicesForDataPointEvents))
	for model, params := range ignoreDevicesForDataPointEvents {
		cp := make(map[hmenum.Parameter]struct{}, len(params))
		maps.Copy(cp, params)
		out[model] = cp
	}
	return out
}

// AcceptParameterOnlyOnChannelMap returns a read-only copy of the
// channel-restriction map. The key is a parameter name (string); the
// value is the single channel number on which the parameter is accepted.
func AcceptParameterOnlyOnChannelMap() map[string]int {
	out := make(map[string]int, len(acceptParameterOnlyOnChannel))
	maps.Copy(out, acceptParameterOnlyOnChannel)
	return out
}

// IsParameterIgnoredForDataPointEvent reports whether a data-point event
// for (model, parameter) should be suppressed according to the
// IGNORE_DEVICES_FOR_DATA_POINT_EVENTS rule set.
func IsParameterIgnoredForDataPointEvent(model string, p hmenum.Parameter) bool {
	params, ok := ignoreDevicesForDataPointEvents[model]
	if !ok {
		return false
	}
	_, suppressed := params[p]
	return suppressed
}

// IsAcceptedOnlyOnChannel reports whether parameter is restricted to a single
// channel. When the parameter has a channel restriction and channelNo does
// not match, the caller should exclude the parameter. Returns false when no
// restriction exists for the parameter.
func IsAcceptedOnlyOnChannel(parameter string, channelNo int) bool {
	ch, ok := acceptParameterOnlyOnChannel[parameter]
	if !ok {
		return false // no restriction
	}
	return channelNo != ch
}

// InvalidatePrefixCache is a no-op in Go: the Go visibility
// implementation uses a flat memoisation map rather than a dedicated
// Prefix cache. The method exists for API parity
// ModelVisibilityValidator.invalidate_prefix_cache and
// ParameterVisibilityDecider.invalidate_prefix_cache (model_validator.py
// parameter_decider.py). Calling it is safe and has no side effects.
func (r *Registry) InvalidatePrefixCache() {
	// No-op: no separate prefix cache in Go.
}
