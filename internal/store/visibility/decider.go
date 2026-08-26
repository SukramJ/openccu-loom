// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ParameterDecider answers per-parameter visibility questions.
//
// The decider combines the static [Rules] with the un_ignore overrides parsed
// from the CCU's user configuration. Lookups are memoised per
// [ignoreCacheKey] tuple so the hot path (event coordinator filtering) is
// allocation-free after warm-up.
//
// The required-parameter whitelist (see [SetRequiredParameters]) is a
// VALUES-only override: any parameter present in the whitelist is never
// ignored regardless of IGNORED_PARAMETERS or wildcard rules. The whitelist
// has no effect on MASTER paramsets, which have their own channel-whitelist
// gating via relevantMasterParamsetsByDevice.
type ParameterDecider struct {
	rules              *Rules
	mu                 sync.RWMutex
	unIgnore           []UnIgnoreEntry
	cacheVal           map[ignoreCacheKey]bool
	requiredParameters map[hmenum.Parameter]struct{} // VALUES-only whitelist

	// ruleGen counts the rule-set changes that invalidate memoised
	// verdicts. Verdicts are computed without the lock held, so a
	// concurrent rule change can land between the computation and the
	// store; the generation observed before computing is what tells the
	// two apart. Without it a verdict from the previous rule set is
	// written into the freshly emptied cache and outlives the change.
	ruleGen uint64
}

// maxCacheEntries bounds the memoisation cache so an ever-growing set of
// distinct (model, channelType, channelNo, paramset, parameter) tuples
// cannot grow it without limit. When the bound is reached the whole cache
// is cleared before the new entry is inserted — simpler than an eviction
// policy, and no less correct since the cache is a pure memoisation layer
// with no external freshness requirement. [ParameterDecider.Len] exposes
// the current size so callers can monitor how often this happens.
// Declared as a var (not a const) so tests can shrink it temporarily.
var maxCacheEntries = 50_000

// NewParameterDecider returns a decider backed by `rules`. Pass nil
// to use the built-in defaults from [NewRules].
func NewParameterDecider(rules *Rules) *ParameterDecider {
	if rules == nil {
		rules = NewRules()
	}
	return &ParameterDecider{
		rules:    rules,
		cacheVal: make(map[ignoreCacheKey]bool),
	}
}

// SetRequiredParameters replaces the VALUES-paramset whitelist with the
// given slice. Parameters in this set are never treated as ignored
// regardless of IGNORED_PARAMETERS or wildcard patterns — as long as
// the paramset is VALUES. Invalidates the memoisation cache.
//
// This must only be called before the decider enters active use, or
// while holding an external coordination mechanism. Calling it
// concurrently with active IsParameterIgnored calls is safe (protected
// by d.mu) but will cause a cache flush which serialises pending queries.
//
// The method mirrors the intent of
// whitelist described in parameter_decider.py:219 and
// parameter_decider.py:378.
func (d *ParameterDecider) SetRequiredParameters(params []hmenum.Parameter) {
	d.mu.Lock()
	if len(params) == 0 {
		d.requiredParameters = nil
	} else {
		m := make(map[hmenum.Parameter]struct{}, len(params))
		for _, p := range params {
			m[p] = struct{}{}
		}
		d.requiredParameters = m
	}
	// Invalidate cache so memoised ignore decisions are recomputed with
	// the new whitelist.
	d.invalidateCacheLocked()
	d.mu.Unlock()
}

// isRequired reports whether p is in the required-parameters whitelist.
// Safe to call without holding d.mu — acquires RLock internally.
func (d *ParameterDecider) isRequired(p hmenum.Parameter) bool {
	d.mu.RLock()
	m := d.requiredParameters
	d.mu.RUnlock()
	if m == nil {
		return false
	}
	_, ok := m[p]
	return ok
}

// channelNoUnknown is the sentinel used when the caller does not know the
// channel number. A negative value is safe because real channel numbers are
// always non-negative.
const channelNoUnknown = -1

// LoadUnIgnore replaces the un_ignore override set with `entries`.
// Invalidates the memoisation cache.
func (d *ParameterDecider) LoadUnIgnore(entries []UnIgnoreEntry) {
	d.mu.Lock()
	d.unIgnore = append(d.unIgnore[:0], entries...)
	d.invalidateCacheLocked()
	d.mu.Unlock()
}

// IsParameterIgnored reports whether the parameter should be filtered
// from the user-visible surface. The decision is memoised.
// channelNo is the channel number; pass [channelNoUnknown] (-1) when the
// channel number is not available (MASTER gating will be less precise but safe).
//
// IsParameterIgnored answers for the central-agnostic ("global") scope: only
// un-ignore entries with an empty [UnIgnoreEntry.Central] can re-enable a
// parameter here. Callers that know which central they are answering for —
// every production call site does, since multi-CCU is first class (ADR
// 0002) — must use [ParameterDecider.IsParameterIgnoredForCentral] instead,
// so a per-central un-ignore entry cannot decide visibility for a different
// central sharing this decider instance.
//
// Decision order
// 1. For VALUES: static IGNORED_PARAMETERS / wildcard patterns → ignored,
// unless model appears in unIgnoreParametersByDevice (prefix match).
// 2. For VALUES: ignoreParametersByDevice — device-specific suppress list
// → ignored (prefix match on model).
// 3. For MASTER: channel-whitelist gating via relevantMasterParamsetsByChannel
// and relevantMasterParamsetsByDevice → ignored if not whitelisted.
// 4. Rules.Evaluate (hiddenParameters merged in NewRules) → ignored,
// unless an UnIgnoreEntry re-enables it.
func (d *ParameterDecider) IsParameterIgnored(model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return d.IsParameterIgnoredForCentral("", model, channelType, channelNo, paramset, p)
}

// IsParameterIgnoredForCentral is [ParameterDecider.IsParameterIgnored]
// scoped to one central: an [UnIgnoreEntry] only re-enables the parameter
// here when its Central field is empty (global) or equals `central`.
func (d *ParameterDecider) IsParameterIgnoredForCentral(central, model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	key := ignoreCacheKey{
		central:     central,
		model:       model,
		channelType: channelType,
		channelNo:   channelNo,
		paramsetKey: paramset,
		parameter:   p,
	}
	// The verdict is computed without the lock so concurrent lookups do not
	// serialise on it. That leaves a window in which the rule set changes
	// between the computation and the store, so the generation observed
	// before computing is carried through and re-checked: a verdict from a
	// superseded rule set is recomputed instead of being written into the
	// cache the change just emptied.
	for {
		d.mu.RLock()
		if v, ok := d.cacheVal[key]; ok {
			d.mu.RUnlock()
			return v
		}
		gen := d.ruleGen
		d.mu.RUnlock()

		ignored := d.computeIgnored(central, model, channelNo, paramset, p)

		if d.storeVerdictIfCurrent(key, ignored, gen) {
			return ignored
		}
	}
}

// storeVerdictIfCurrent memoises `ignored` under `key` unless the rule set
// changed since generation `gen` was observed. It reports whether the
// verdict was accepted; a rejected verdict has to be recomputed, because it
// answers a question the current rules may answer differently.
func (d *ParameterDecider) storeVerdictIfCurrent(key ignoreCacheKey, ignored bool, gen uint64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ruleGen != gen {
		return false
	}
	if len(d.cacheVal) >= maxCacheEntries {
		// Size eviction alone does not change any answer, so it leaves the
		// generation untouched.
		d.cacheVal = make(map[ignoreCacheKey]bool)
	}
	d.cacheVal[key] = ignored
	return true
}

// computeIgnored performs the actual ignore decision without caching.
//
// Decision branches mirror
// (store/visibility/parameter_decider.py:296):
// - VALUES paramset: static ignore list + device-level suppress.
// - MASTER paramset: channel-whitelist gating only
// (_check_master_parameter_is_ignored). The Rules/hiddenParameters check
// is intentionally skipped for MASTER — a parameter can be in
// HIDDEN_PARAMETERS (UI-hidden) but still be created as a data point for
// MASTER if it is whitelisted by the device entry.
// - Other paramsets (LINK, …): no ignore rules — return false.
func (d *ParameterDecider) computeIgnored(central, model string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	switch paramset {
	case hmenum.ParamsetKeyValues:
		return d.computeIgnoredValues(central, model, channelNo, paramset, p)
	case hmenum.ParamsetKeyMaster:
		return d.computeIgnoredMaster(central, model, channelNo, paramset, p)
	default:
		return false
	}
}

// computeIgnoredValues applies the VALUES-paramset ignore rules.
func (d *ParameterDecider) computeIgnoredValues(central, model string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	name := string(p)

	// 0. Early-exit guard: if the parameter is un-ignored (custom or
	// device-built-in), return false immediately — bypassing ALL ignore rules.
	// This mirrors _check_values_parameter_is_ignored (parameter_decider.py):
	// the un-ignored check fires first, before any static-list test.
	//
	// The required-parameters whitelist is intentionally NOT part of this
	// guard: in the reference stack `_required_parameters` only exempts from
	// the static ignore list (step 1); the device-specific suppress list
	// (step 2) applies regardless. OPERATING_VOLTAGE is required (it sits in
	// the default-DP catalogue) yet mains-powered models like HmIP-BSM must
	// still suppress it.
	if d.matchesUnIgnore(central, model, channelNo, paramset, p) || deviceUnIgnoresByPrefix(model, p) {
		return false
	}

	// 1. Check static IGNORED_PARAMETERS and wildcard patterns — unless the
	// parameter is on the required whitelist (default DPs, profile fields).
	if _, inIgnoreList := ignoredParameters[name]; (inIgnoreList || parameterIsWildcardIgnored(name)) && !d.isRequired(p) {
		return true
	}

	// 2. Check ignoreParametersByDevice — suppress for specific models
	// (prefix match: entry is a prefix of the actual model name).
	if models, ok := ignoreParametersByDevice[name]; ok {
		if modelMatchesByPrefix(model, models) {
			return true
		}
	}

	// 3. Check Rules (includes hiddenParameters from NewRules). Un-ignore
	// has already been handled by the leading guard above (step 0), so no
	// second matchesUnIgnore call is needed here. Like the static list,
	// this branch honours the required whitelist: the rules container is a
	// superset of the reference stack's static hides, which the whitelist
	// exempts — only the device-specific suppress list (step 2) and the
	// channel/event gates below apply unconditionally.
	if d.rules.Evaluate(model, p) == DecisionHide && !d.isRequired(p) {
		return true
	}

	// 4. Channel-specific parameter restriction. Without this branch the LOWBAT
	// parameter surfaces on every actor channel (HmIP-PSM, HmIP-BSM, …).
	//
	// Skip when the channel number is not known (channelNoUnknown == -1) —
	// without a real channel we cannot check the restriction.
	if channelNo >= 0 && IsAcceptedOnlyOnChannel(name, channelNo) {
		return true
	}

	// 5. Event-suppression gate: IGNORE_DEVICES_FOR_DATA_POINT_EVENTS. The
	// model key matches by case-insensitive prefix, like the other device
	// maps. In practice the sole entry is "HmIP-PS" → ClickEvents, covering
	// the whole HmIP-PS* family (HmIP-PS, HmIP-PSM, …): their click
	// parameters are filtered from the data-point event surface.
	if IsParameterIgnoredForDataPointEvent(model, p) {
		return true
	}

	return false
}

// computeIgnoredMaster applies the MASTER-paramset ignore rules.
// Note: hiddenParameters / Rules are NOT checked here because a parameter
// can be "hidden" (UI-only flag) but still whitelisted for MASTER data-point
// creation. The hidden flag is applied separately by IsParameterHidden.
func (d *ParameterDecider) computeIgnoredMaster(central, model string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	if checkMasterParameterIgnored(channelNo, p, model) {
		// Un-ignore entries can still re-enable a MASTER parameter.
		if !d.matchesUnIgnore(central, model, channelNo, paramset, p) {
			return true
		}
	}
	return false
}

// deviceUnIgnoresByPrefix reports whether parameter p is un-ignored for the
// model via unIgnoreParametersByDevice. Mirrors the reference
// _get_parameters_for_model_prefix: an entry matches when its key STARTS WITH
// the device model (reverse prefix), i.e. the device inherits a longer
// variant's un-ignore (HmIP-PCBS picks up the HmIP-PCBS-BAT entry's
// OPERATING_VOLTAGE) while a longer variant does NOT inherit a shorter base's
// un-ignore (HM-Sec-Key-S / HM-Sec-Win-Generic do not pick up the HM-Sec-Key /
// HM-Sec-Win entries' ERROR/WORKING — the direct-CCU twin leaves those hidden).
func deviceUnIgnoresByPrefix(model string, p hmenum.Parameter) bool {
	modelL := strings.ToLower(model)
	for candidate, params := range unIgnoreParametersByDevice {
		if !strings.HasPrefix(strings.ToLower(candidate), modelL) {
			continue
		}
		if _, ok := params[p]; ok {
			return true
		}
	}
	return false
}

// modelMatchesByPrefix reports whether model (case-insensitive) starts with
// any key in the given model-name set.
func modelMatchesByPrefix(model string, models map[string]struct{}) bool {
	modelL := strings.ToLower(model)
	for candidate := range models {
		if strings.HasPrefix(modelL, strings.ToLower(candidate)) {
			return true
		}
	}
	return false
}

// checkMasterParameterIgnored reports whether a MASTER parameter should be
// ignored for the given channel and model.
//
// Without a whitelist match, MASTER DPs default to NoCreate so unlisted
// devices like HmIP-STE2-PCB / HmIP-SFD don't surface ~25 configuration
// entities (ARR_TIMEOUT, CYCLIC_INFO_MSG, COND_TX_*, …) HA users never tune
// from the climate / sensor card.
//
// Decision order: 1. If the parameter is in relevantMasterParamsetsByChannel
// for channelNo (or the nil-channel wildcard) → NOT ignored. 2. If the model
// (prefix match) is in relevantMasterParamsetsByDevice AND the channel is in
// its Channels set (empty set = any channel, i.e. the "frozenset({None})"
// wildcard from Python) AND the parameter is in its Parameters set → NOT
// ignored. 3. Otherwise → IS ignored (default-skip for MASTER).
//
// Operator un_ignore overrides ride on top via the wrapping
// [ParameterDecider.IsParameterIgnored] short-circuit, so this function does
// not need to consult un_ignore directly.
func checkMasterParameterIgnored(channelNo int, p hmenum.Parameter, model string) bool {
	// 1. Channel-level whitelist from relevantMasterParamsetsByChannel.
	// Check the specific channel number, then the nil-key fallback.
	for _, ptr := range masterChannelKeys(channelNo) {
		if params, ok := relevantMasterParamsetsByChannel[ptr]; ok {
			if _, inParams := params[p]; inParams {
				return false
			}
		}
	}

	// 2. Device-level whitelist from relevantMasterParamsetsByDevice.
	// Use longest-prefix match (case-sensitive — model names are
	// canonical in the registry).
	if entry, found := findMasterDeviceEntry(model); found {
		// Empty Channels set = Python frozenset({None}) = any channel.
		channelOK := len(entry.Channels) == 0
		if !channelOK {
			_, channelOK = entry.Channels[channelNo]
		}
		if channelOK {
			if _, paramAllowed := entry.Parameters[p]; paramAllowed {
				return false
			}
		}
	}

	// 3. Default-skip for MASTER (mirrors
	// `should_skip_parameter` default branch). Models without an
	// explicit whitelist entry get no MASTER DPs surfaced.
	return true
}

// masterChannelKeys returns the sequence of *int keys to probe in
// relevantMasterParamsetsByChannel: first the exact channel pointer, then nil
// (the wildcard fallback). nil is always last so specific entries take
// precedence.
func masterChannelKeys(channelNo int) []*int {
	if channelNo < 0 {
		// Unknown channel — only check the nil wildcard.
		return []*int{nil}
	}
	// We cannot take the address of channelNo directly and match map keys by
	// pointer value, so we must scan the map for a *int whose value equals
	// channelNo. The map is tiny (currently 2 entries) so a linear scan is fine.
	var specific *int
	for k := range relevantMasterParamsetsByChannel {
		if k != nil && *k == channelNo {
			specific = k
			break
		}
	}
	return []*int{specific, nil}
}

// IsRelevantMasterParameter reports whether the (model, channel, parameter)
// tuple is in the device-level MASTER paramset whitelist
// (`relevantMasterParamsetsByDevice`). Channel-level filtering: the entry's
// Channels map either lists explicit channel numbers or is empty (= any
// channel including device-root).
func IsRelevantMasterParameter(model string, channelNo int, p hmenum.Parameter) bool {
	entry, ok := findMasterDeviceEntry(model)
	if !ok {
		return false
	}
	if _, paramOK := entry.Parameters[p]; !paramOK {
		return false
	}
	if len(entry.Channels) == 0 {
		// Empty-set semantics
		// here, signalling "any channel including device-root".
		return true
	}
	if _, channelOK := entry.Channels[channelNo]; channelOK {
		return true
	}
	return false
}

// findMasterDeviceEntry returns the ModelMasterEntry for the given model from
// relevantMasterParamsetsByDevice using longest-prefix match (case-insensitive).
// The second return value reports whether a match was found.
func findMasterDeviceEntry(model string) (ModelMasterEntry, bool) {
	// Exact match first for performance.
	if entry, ok := relevantMasterParamsetsByDevice[model]; ok {
		return entry, true
	}
	// Prefix match: find the longest key that is a prefix of model.
	modelL := strings.ToLower(model)
	var best string
	var bestEntry ModelMasterEntry
	found := false
	for key, entry := range relevantMasterParamsetsByDevice {
		keyL := strings.ToLower(key)
		if strings.HasPrefix(modelL, keyL) && len(keyL) > len(best) {
			best = keyL
			bestEntry = entry
			found = true
		}
	}
	return bestEntry, found
}

// UnIgnoreEntries returns a copy of the current un-ignore override list.
// Used by Registry.InvalidateAllCaches to reload the same rules after a flush.
func (d *ParameterDecider) UnIgnoreEntries() []UnIgnoreEntry {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]UnIgnoreEntry, len(d.unIgnore))
	copy(out, d.unIgnore)
	return out
}

// ClearCache resets the memoisation map without touching the un-ignore rules.
func (d *ParameterDecider) ClearCache() {
	d.mu.Lock()
	d.invalidateCacheLocked()
	d.mu.Unlock()
}

// invalidateCacheLocked empties the memoisation map and advances the rule
// generation. Callers must hold d.mu for writing. The generation bump is
// what stops a verdict that is already being computed under the previous
// rule set from being stored afterwards.
func (d *ParameterDecider) invalidateCacheLocked() {
	d.cacheVal = make(map[ignoreCacheKey]bool)
	d.ruleGen++
}

// InvalidatePrefixCache is a no-op in Go: the Go implementation uses a flat
// memoisation map (ignoreCacheKey) rather than a dedicated prefix cache.
// The method exists for API parity
// ParameterVisibilityDecider.invalidate_prefix_cache (parameter_decider.py).
func (d *ParameterDecider) InvalidatePrefixCache() {
	// No-op: no separate prefix cache in Go.
}

// ShouldSkipParameter is the compound skip decision for one parameter.
// Returns true when the parameter should be excluded from the user-visible
// surface. It combines the ignore decision with the model-validity check.
func (d *ParameterDecider) ShouldSkipParameter(model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return d.IsParameterIgnored(model, channelType, channelNo, paramset, p)
}

// ShouldSkipParameterForCentral is [ParameterDecider.ShouldSkipParameter]
// scoped to one central; see [ParameterDecider.IsParameterIgnoredForCentral].
func (d *ParameterDecider) ShouldSkipParameterForCentral(central, model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return d.IsParameterIgnoredForCentral(central, model, channelType, channelNo, paramset, p)
}

// Len returns the number of entries currently held in the memoisation
// cache. It is safe to call concurrently and satisfies the
// [coordinators.CacheSizeProvider] interface so the CacheCoordinator
// can surface the visibility-cache size in its metrics without an
// extra adapter layer.
func (d *ParameterDecider) Len() int {
	d.mu.RLock()
	n := len(d.cacheVal)
	d.mu.RUnlock()
	return n
}

// IsParameterHidden is an alias for [IsParameterIgnored] kept for
// API parity
// share the same answer: hidden ≡ ignored from the user's POV.
// channelNo is the channel number; pass [channelNoUnknown] (-1) when
// the channel number is not available.
func (d *ParameterDecider) IsParameterHidden(model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return d.IsParameterIgnored(model, channelType, channelNo, paramset, p)
}

// IsParameterHiddenForCentral is [ParameterDecider.IsParameterHidden]
// scoped to one central; see [ParameterDecider.IsParameterIgnoredForCentral].
func (d *ParameterDecider) IsParameterHiddenForCentral(central, model, channelType string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return d.IsParameterIgnoredForCentral(central, model, channelType, channelNo, paramset, p)
}

// IsUnIgnored reports whether the (model, channelNo, paramset, parameter)
// tuple is explicitly un-ignored. Mirrors
// `ParameterVisibilityDecider.is_un_ignored`.
func (d *ParameterDecider) IsUnIgnored(model, channelType string, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return d.IsUnIgnoredForCentral("", model, channelType, paramset, p)
}

// IsUnIgnoredForCentral is [ParameterDecider.IsUnIgnored] scoped to one
// central; see [ParameterDecider.IsParameterIgnoredForCentral].
func (d *ParameterDecider) IsUnIgnoredForCentral(central, model, channelType string, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	// channelType is retained in the public signature for API stability;
	// un-ignore matching uses channelNoUnknown because the channel number is not
	// available in this call path.
	return d.matchesUnIgnoreLocked(central, model, channelNoUnknown, paramset, p)
}

// IsUnIgnoredCustomOnly reports whether p is explicitly un-ignored,
// considering only user-provided rules when customOnly is true, or all rules
// (including built-in device rules) when customOnly is false.
//
// When customOnly is false, the function additionally checks
// unIgnoreParametersByDevice (built-in per-device un-ignore entries) via
// [deviceUnIgnoresByPrefix]. When customOnly is true only the user-provided
// [UnIgnoreEntry] list (loaded via [LoadUnIgnore]) is consulted — built-in
// device un-ignores are intentionally excluded.
func (d *ParameterDecider) IsUnIgnoredCustomOnly(model, channelType string, paramset hmenum.ParamsetKey, p hmenum.Parameter, customOnly bool) bool {
	return d.IsUnIgnoredCustomOnlyForCentral("", model, channelType, paramset, p, customOnly)
}

// IsUnIgnoredCustomOnlyForCentral is [ParameterDecider.IsUnIgnoredCustomOnly]
// scoped to one central; see [ParameterDecider.IsParameterIgnoredForCentral].
// The built-in device un-ignores checked when customOnly is false have no
// central dimension — they are global by construction, not per-CCU data.
func (d *ParameterDecider) IsUnIgnoredCustomOnlyForCentral(central, model, channelType string, paramset hmenum.ParamsetKey, p hmenum.Parameter, customOnly bool) bool {
	if !customOnly {
		// Full check: user entries + built-in device entries.
		if deviceUnIgnoresByPrefix(model, p) {
			return true
		}
		return d.IsUnIgnoredForCentral(central, model, channelType, paramset, p)
	}
	// customOnly=true: only consult user-provided un_ignore entries.
	// Built-in device un-ignores (unIgnoreParametersByDevice) are
	// deliberately skipped — callers that set custom_only=true want to
	// know whether the user has explicitly un-ignored a parameter.
	// Mirrors _check_parameter_is_un_ignored custom_only=True path
	// (parameter_decider.py).
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.matchesUnIgnoreLocked(central, model, channelNoUnknown, paramset, p)
}

// matchesUnIgnore acquires RLock and delegates to [matchesUnIgnoreLocked].
func (d *ParameterDecider) matchesUnIgnore(central, model string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.matchesUnIgnoreLocked(central, model, channelNo, paramset, p)
}

// matchesUnIgnoreLocked checks whether any UnIgnoreEntry in d.unIgnore matches
// the (model, channelNo, paramset, parameter) tuple.
//
// For VALUES paramsets the search uses a four-point matrix (mirroring
// _check_parameter_is_un_ignored in parameter_decider.py):
//
//	(model_l, channelNo)           — exact model + exact channel
//	(model_l, unIgnoreWildcard)    — exact model + any channel
//	(unIgnoreWildcard, channelNo)  — any model + exact channel
//	(unIgnoreWildcard, wildcard)   — any model + any channel
//
// For non-VALUES (MASTER, LINK, …) only the exact (model_l, channelNo) point
// is checked.
//
// Simple entries (IsSimple==true) match any VALUES parameter lookup
// immediately. Caller must hold d.mu (read or write).
//
// An entry whose Central field is set only matches a lookup for the same
// central; an entry with an empty Central is global and matches every
// lookup. This is what keeps an un-ignore registered for one CCU from
// deciding visibility for another CCU sharing this decider instance.
func (d *ParameterDecider) matchesUnIgnoreLocked(central, model string, channelNo int, paramset hmenum.ParamsetKey, p hmenum.Parameter) bool {
	modelL := strings.ToLower(model)
	for _, e := range d.unIgnore {
		if e.Parameter != p {
			continue
		}
		if e.Central != "" && e.Central != central {
			continue
		}
		// Simple entries match any VALUES lookup without further checks.
		if e.IsSimple {
			if paramset == hmenum.ParamsetKeyValues {
				return true
			}
			continue
		}
		// Complex entry: paramset must match (empty ParamsetKey matches any).
		if e.ParamsetKey != "" && e.ParamsetKey != paramset {
			continue
		}
		// Use the requested paramset for search-matrix purposes when the
		// entry does not specify one.
		effectiveParamset := paramset
		if e.ParamsetKey != "" {
			effectiveParamset = e.ParamsetKey
		}
		if entryMatchesSearchMatrix(e, modelL, channelNo, effectiveParamset) {
			return true
		}
	}
	return false
}

// entryMatchesSearchMatrix reports whether a complex UnIgnoreEntry matches
// any of the search-matrix points for the given (modelL, channelNo, paramset)
// lookup. Mirrors the search_patterns logic in
// _check_parameter_is_un_ignored (parameter_decider.py).
//
// Entry model comparison is always case-insensitive. An empty entry Model
// means "any model" (backward compatibility for programmatically-constructed
// entries).
func entryMatchesSearchMatrix(e UnIgnoreEntry, modelL string, channelNo int, paramset hmenum.ParamsetKey) bool {
	eModelL := strings.ToLower(e.Model)
	if paramset == hmenum.ParamsetKeyValues {
		// Four-point matrix for VALUES.
		// Point 1: (model_l, channelNo) — exact model + exact channel.
		if entryModelChannelMatch(eModelL, modelL, e, channelNo) {
			return true
		}
		// Point 2: (model_l, unIgnoreWildcard) — exact model + any channel.
		if eModelL == modelL && e.ChannelNoIsWildcard {
			return true
		}
		// Point 3: (unIgnoreWildcard, channelNo) — any model + exact channel.
		if eModelL == unIgnoreWildcard && entryChannelMatch(e, channelNo) {
			return true
		}
		// Point 4: (unIgnoreWildcard, wildcard) — any model + any channel.
		if eModelL == unIgnoreWildcard && e.ChannelNoIsWildcard {
			return true
		}
		return false
	}
	// Non-VALUES: only exact (model_l, channelNo) match.
	return entryModelChannelMatch(eModelL, modelL, e, channelNo)
}

// entryModelChannelMatch checks the exact model and channel match.
// eModelL is the lower-cased entry model. An empty eModelL means "any model".
func entryModelChannelMatch(eModelL, modelL string, e UnIgnoreEntry, channelNo int) bool {
	if eModelL != "" && eModelL != modelL {
		return false
	}
	return entryChannelMatch(e, channelNo)
}

// entryChannelMatch checks whether an entry's ChannelNo matches the given
// channel number. A nil ChannelNo (Python None) matches any channel.
func entryChannelMatch(e UnIgnoreEntry, channelNo int) bool {
	if e.ChannelNo == nil {
		return true // Python None = any channel
	}
	if channelNo < 0 {
		return true // channelNoUnknown matches any entry
	}
	return *e.ChannelNo == channelNo
}

// modelPrefixMatch reports whether model starts with pattern
// (case-insensitive prefix match).
func modelPrefixMatch(model, pattern string) bool {
	if pattern == "" {
		return true
	}
	if len(model) < len(pattern) {
		return false
	}
	return strings.EqualFold(model[:len(pattern)], pattern)
}
