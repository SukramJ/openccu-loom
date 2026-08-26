// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestIgnoreCacheKeyEqualityForSameInputs verifies that two ignoreCacheKey
// structs built from identical field values compare equal, making them safe
// to use as Go map keys.
func TestIgnoreCacheKeyEqualityForSameInputs(t *testing.T) {
	t.Parallel()
	a := ignoreCacheKey{
		model:       "HmIP-eTRV-CL",
		channelType: "HEATING_CLIMATECONTROL_TRANSCEIVER",
		channelNo:   1,
		paramsetKey: hmenum.ParamsetKeyValues,
		parameter:   hmenum.ParameterTemperature,
	}
	b := ignoreCacheKey{
		model:       "HmIP-eTRV-CL",
		channelType: "HEATING_CLIMATECONTROL_TRANSCEIVER",
		channelNo:   1,
		paramsetKey: hmenum.ParamsetKeyValues,
		parameter:   hmenum.ParameterTemperature,
	}
	if a != b {
		t.Error("identical ignoreCacheKey structs must be equal")
	}
}

// TestIgnoreCacheKeyDiffersOnParamset verifies that a different ParamsetKey
// produces a different key, preventing cross-paramset cache collisions.
func TestIgnoreCacheKeyDiffersOnParamset(t *testing.T) {
	t.Parallel()
	base := ignoreCacheKey{
		model:       "HmIP-BS2",
		channelType: "SWITCH",
		channelNo:   channelNoUnknown,
		paramsetKey: hmenum.ParamsetKeyValues,
		parameter:   hmenum.ParameterState,
	}
	other := base
	other.paramsetKey = hmenum.ParamsetKeyMaster
	if base == other {
		t.Error("ignoreCacheKey must differ when paramsetKey changes")
	}
}

// TestIgnoreCacheKeyDiffersOnChannelNo verifies that different channel
// numbers produce different keys, which is required for per-channel MASTER
// gating to be cached independently.
func TestIgnoreCacheKeyDiffersOnChannelNo(t *testing.T) {
	t.Parallel()
	base := ignoreCacheKey{
		model:       "HmIP-WTH",
		channelType: "WEATHER",
		channelNo:   0,
		paramsetKey: hmenum.ParamsetKeyMaster,
		parameter:   hmenum.ParameterTemperature,
	}
	other := base
	other.channelNo = 1
	if base == other {
		t.Error("ignoreCacheKey must differ when channelNo changes")
	}
}

// TestIgnoreCacheKeyDiffersOnModel verifies that different model strings
// produce different keys, preventing cross-device cache pollution.
func TestIgnoreCacheKeyDiffersOnModel(t *testing.T) {
	t.Parallel()
	base := ignoreCacheKey{
		model:       "HmIP-eTRV-BL",
		channelType: "HEATING",
		channelNo:   channelNoUnknown,
		paramsetKey: hmenum.ParamsetKeyValues,
		parameter:   hmenum.ParameterTemperature,
	}
	other := base
	other.model = "HmIP-eTRV-CL"
	if base == other {
		t.Error("ignoreCacheKey must differ when model changes")
	}
}

// TestIgnoreCacheKeyUsableAsMapKey verifies that ignoreCacheKey can be used
// as a Go map key and that lookups work correctly. This is the primary
// contract: the compiler allows struct keys if all fields are comparable.
func TestIgnoreCacheKeyUsableAsMapKey(t *testing.T) {
	t.Parallel()
	m := make(map[ignoreCacheKey]bool)
	k := ignoreCacheKey{
		model:       "HmIP-RGBW",
		channelType: "DIMMER",
		channelNo:   2,
		paramsetKey: hmenum.ParamsetKeyValues,
		parameter:   hmenum.ParameterLevel,
	}
	m[k] = true
	if !m[k] {
		t.Error("stored value must be retrievable using the same key")
	}
	// A different key must not retrieve the stored value.
	kOther := k
	kOther.parameter = hmenum.ParameterState
	if m[kOther] {
		t.Error("distinct key must not return the stored value")
	}
}

// TestUnIgnoreCacheKeyDiffersOnCustomOnly verifies that the customOnly
// field differentiates two keys that are otherwise identical, mirroring
// Custom_only dimension.
func TestUnIgnoreCacheKeyDiffersOnCustomOnly(t *testing.T) {
	t.Parallel()
	base := UnIgnoreCacheKey{
		Model:       "HmIP-BS2",
		ChannelType: "SWITCH",
		ParamsetKey: hmenum.ParamsetKeyValues,
		Parameter:   hmenum.ParameterState,
		CustomOnly:  false,
	}
	custom := base
	custom.CustomOnly = true
	if base == custom {
		t.Error("UnIgnoreCacheKey must differ when customOnly changes")
	}
}

// TestUnIgnoreCacheKeyEqualityForSameInputs verifies struct equality holds
// for two UnIgnoreCacheKey values built from the same fields.
func TestUnIgnoreCacheKeyEqualityForSameInputs(t *testing.T) {
	t.Parallel()
	a := UnIgnoreCacheKey{
		Model:       "HmIP-BROLL",
		ChannelType: "BLIND",
		ParamsetKey: hmenum.ParamsetKeyMaster,
		Parameter:   hmenum.ParameterLevel,
		CustomOnly:  true,
	}
	b := a // copy
	if a != b {
		t.Error("identical UnIgnoreCacheKey structs must be equal")
	}
}
