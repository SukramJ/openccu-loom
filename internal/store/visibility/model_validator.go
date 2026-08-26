// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ModelValidator decides at the device-model granularity whether data points
// should be exposed at all.
//
// Two responsibilities:
//
// - `IsModelIgnored(model)` — entire models can be filtered (e.g. virtual
// devices the user does not want surfaced). - `IsRelevantParamset(model,
// key)` / `IsRelevantParamsetForChannel` — MASTER paramsets are only exposed
// when the channel is in the RELEVANT_MASTER_PARAMSETS_BY_CHANNEL set OR the
// model+channel pair matches the per-device whitelist.
//
// The channel-level check mirrors Python `is_relevant_paramset`
// (model_validator.py:61-94).
type ModelValidator struct {
	mu             sync.RWMutex
	ignoredModels  map[string]struct{}
	relevantMaster []string // case-insensitive prefix list
	// relevantMasterChannels maps a short device-model prefix to the set
	// of channel numbers for which MASTER paramsets should be fetched.
	// Set via [SetRelevantMasterChannels]. Nil means no device-channel
	// filtering — all channels are relevant for models on the prefix list.
	relevantMasterChannels map[string]map[int]struct{}
}

// NewModelValidator returns a validator pre-loaded with sensible
// defaults: no models ignored, MASTER paramsets exposed for every
// model. Callers narrow the allow-list via [SetRelevantMasterPrefixes].
func NewModelValidator() *ModelValidator {
	return &ModelValidator{
		ignoredModels: make(map[string]struct{}),
	}
}

// IgnoreModel adds `model` to the ignored set. Models are matched
// case-insensitively against the full string, not as a prefix.
func (v *ModelValidator) IgnoreModel(model string) {
	v.mu.Lock()
	v.ignoredModels[strings.ToUpper(model)] = struct{}{}
	v.mu.Unlock()
}

// UnIgnoreModel removes `model` from the ignored set.
func (v *ModelValidator) UnIgnoreModel(model string) {
	v.mu.Lock()
	delete(v.ignoredModels, strings.ToUpper(model))
	v.mu.Unlock()
}

// IsModelIgnored reports whether the model is on the ignore list.
func (v *ModelValidator) IsModelIgnored(model string) bool {
	v.mu.RLock()
	defer v.mu.RUnlock()
	_, ok := v.ignoredModels[strings.ToUpper(model)]
	return ok
}

// SetRelevantMasterPrefixes installs the prefix allow-list for MASTER
// paramset visibility. An empty list means "every model is relevant"
// — the default.
func (v *ModelValidator) SetRelevantMasterPrefixes(prefixes []string) {
	v.mu.Lock()
	v.relevantMaster = append(v.relevantMaster[:0], prefixes...)
	v.mu.Unlock()
}

// SetRelevantMasterChannels installs the per-model channel whitelist for
// MASTER paramset fetching. The map key is a short model prefix
// (case-insensitive) and the value is the set of channel numbers for which
// MASTER paramsets should be loaded. An empty set means "all channels".
func (v *ModelValidator) SetRelevantMasterChannels(m map[string]map[int]struct{}) {
	v.mu.Lock()
	v.relevantMasterChannels = m
	v.mu.Unlock()
}

// InvalidatePrefixCache is a no-op in Go: Go uses a flat ignored-model set
// rather than a prefix cache. The method exists for API parity with
// Invalidate_prefix_cache
// (model_validator.py).
func (v *ModelValidator) InvalidatePrefixCache() {
	// No-op: no separate prefix cache in Go.
}

// IsRelevantParamset reports whether the (model, paramsetKey) tuple
// is exposed to consumers. VALUES paramsets are always relevant;
// MASTER paramsets honour the prefix allow-list. The channel number
// is not checked here — use [IsRelevantParamsetForChannel] when the
// channel number is known.
func (v *ModelValidator) IsRelevantParamset(model string, key hmenum.ParamsetKey) bool {
	return v.IsRelevantParamsetForChannel(model, channelNoUnknown, key)
}

// IsRelevantParamsetForChannel is like [IsRelevantParamset] but also
// considers the concrete channel number.
//
// Decision order mirrors py:61-94
// 1. VALUES → always relevant.
// 2. MASTER: if channel.no is in RELEVANT_MASTER_PARAMSETS_BY_CHANNEL
// → relevant.
// 3. MASTER: resolve model prefix against relevantMasterChannels; if
// found, check channel.no ∈ entry's channel set → relevant.
// 4. MASTER with no relevantMaster prefix list → relevant (default-open).
// 5. MASTER with prefix list but no match → not relevant.
func (v *ModelValidator) IsRelevantParamsetForChannel(model string, channelNo int, key hmenum.ParamsetKey) bool {
	if key != hmenum.ParamsetKeyMaster {
		return true
	}

	// Step 2: channel-level whitelist from relevantMasterParamsetsByChannel.
	// Mirrors Python: if channel.no in RELEVANT_MASTER_PARAMSETS_BY_CHANNEL: return True
	if channelNo >= 0 {
		for ptr := range relevantMasterParamsetsByChannel {
			if ptr != nil && *ptr == channelNo {
				return true
			}
		}
	}

	v.mu.RLock()
	defer v.mu.RUnlock()

	// Step 3: device-level per-model channel whitelist.
	if v.relevantMasterChannels != nil {
		modelL := strings.ToLower(model)
		// Find the longest matching prefix in relevantMasterChannels.
		best := ""
		for key2 := range v.relevantMasterChannels {
			if strings.HasPrefix(modelL, strings.ToLower(key2)) && len(key2) > len(best) {
				best = key2
			}
		}
		if best != "" {
			channelSet := v.relevantMasterChannels[best]
			if len(channelSet) == 0 {
				// empty set = any channel
				return true
			}
			_, ok := channelSet[channelNo]
			return ok
		}
	}

	// Steps 4-5: legacy prefix-list check.
	if len(v.relevantMaster) == 0 {
		return true
	}
	for _, p := range v.relevantMaster {
		if modelPrefixMatch(model, p) {
			return true
		}
	}
	return false
}
