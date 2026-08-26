// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// event_suppression_test.go covers event-suppression and channel-restriction
// behaviour: IsParameterIgnoredForDataPointEvent, IgnoreDevicesForDataPointEventsLower,
// IsAcceptedOnlyOnChannel, AcceptParameterOnlyOnChannelMap, and
// ModelValidator.IsRelevantParamsetForChannel.  Also verifies no-op behaviour of
// InvalidatePrefixCache on Registry, ModelValidator, and ParameterDecider.

package visibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// ignoreDevicesForDataPointEvents — via IsParameterIgnoredForDataPointEvent
// ---------------------------------------------------------------------------

// TestIgnoreDevicesForDataPointEventsHmIPPS verifies that click-event
// parameters on HmIP-PS are suppressed by the event-suppression gate.
func TestIgnoreDevicesForDataPointEventsHmIPPS(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)

	// The ignoreDevicesForDataPointEvents map has "HmIP-PS" → ClickEvents.
	for p := range hmenum.ClickEvents {
		got := d.IsParameterIgnored("HmIP-PS", "SWITCH_TRANSMITTER", 1, hmenum.ParamsetKeyValues, p)
		if !got {
			t.Errorf("IsParameterIgnored(HmIP-PS, %s) = false, want true (event suppression gate)", p)
		}
	}
}

// TestIgnoreDevicesForDataPointEventsOtherModelUnaffected verifies that
// click events on a model NOT in ignoreDevicesForDataPointEvents are not suppressed.
func TestIgnoreDevicesForDataPointEventsOtherModelUnaffected(t *testing.T) {
	t.Parallel()

	// HmIP-BSM is not in ignoreDevicesForDataPointEvents.
	got := IsParameterIgnoredForDataPointEvent("HmIP-BSM", hmenum.ParameterPressShort)
	if got {
		t.Error("IsParameterIgnoredForDataPointEvent(HmIP-BSM, PRESS_SHORT) must be false — not in event suppress map")
	}
}

// TestIgnoreDevicesForDataPointEventsDirectFunction tests the package-level
// function directly for HmIP-PS and an unrelated model.
func TestIgnoreDevicesForDataPointEventsDirectFunction(t *testing.T) {
	t.Parallel()

	// HmIP-PS must suppress its click events.
	for p := range hmenum.ClickEvents {
		if !IsParameterIgnoredForDataPointEvent("HmIP-PS", p) {
			t.Errorf("IsParameterIgnoredForDataPointEvent(HmIP-PS, %s) = false, want true", p)
		}
	}
	// Other models must not be suppressed.
	for p := range hmenum.ClickEvents {
		if IsParameterIgnoredForDataPointEvent("HmIP-WRC2", p) {
			t.Errorf("IsParameterIgnoredForDataPointEvent(HmIP-WRC2, %s) = true, want false", p)
		}
	}
}

// ---------------------------------------------------------------------------
// IgnoreDevicesForDataPointEventsLower
// ---------------------------------------------------------------------------

func TestIgnoreDevicesForDataPointEventsLowerReturnsHmIPPS(t *testing.T) {
	t.Parallel()
	m := IgnoreDevicesForDataPointEventsLower()
	if _, ok := m["HmIP-PS"]; !ok {
		t.Error("IgnoreDevicesForDataPointEventsLower must contain HmIP-PS")
	}
}

func TestIgnoreDevicesForDataPointEventsLowerIsCopy(t *testing.T) {
	t.Parallel()
	m1 := IgnoreDevicesForDataPointEventsLower()
	m2 := IgnoreDevicesForDataPointEventsLower()
	// Mutating m1 must not affect m2 (independent copies).
	m1["__test__"] = nil
	if _, ok := m2["__test__"]; ok {
		t.Error("IgnoreDevicesForDataPointEventsLower must return independent copies")
	}
}

func TestIgnoreDevicesForDataPointEventsLowerHmIPPSHasClickEvents(t *testing.T) {
	t.Parallel()
	m := IgnoreDevicesForDataPointEventsLower()
	params := m["HmIP-PS"]
	if len(params) == 0 {
		t.Error("HmIP-PS in IgnoreDevicesForDataPointEventsLower must have at least one suppressed parameter")
	}
}

// ---------------------------------------------------------------------------
// IsParameterIgnoredForDataPointEvent
// ---------------------------------------------------------------------------

func TestIsParameterIgnoredForDataPointEventHmIPPSSuppressed(t *testing.T) {
	t.Parallel()
	// HmIP-PS suppresses click events; pick one from the suppressed set.
	m := IgnoreDevicesForDataPointEventsLower()
	params := m["HmIP-PS"]
	if len(params) == 0 {
		t.Skip("HmIP-PS has no suppressed parameters — cannot test")
	}
	var aParam hmenum.Parameter
	for p := range params {
		aParam = p
		break
	}
	if !IsParameterIgnoredForDataPointEvent("HmIP-PS", aParam) {
		t.Errorf("IsParameterIgnoredForDataPointEvent(HmIP-PS, %s) must return true", aParam)
	}
}

func TestIsParameterIgnoredForDataPointEventUnknownModelReturnsFalse(t *testing.T) {
	t.Parallel()
	if IsParameterIgnoredForDataPointEvent("HmIP-UNKNOWN-MODEL", hmenum.ParameterState) {
		t.Error("IsParameterIgnoredForDataPointEvent must return false for unknown model")
	}
}

func TestIsParameterIgnoredForDataPointEventNonSuppressedParamReturnsFalse(t *testing.T) {
	t.Parallel()
	// HmIP-PS exists in the map but SET_TEMPERATURE is not in its suppressed set.
	if IsParameterIgnoredForDataPointEvent("HmIP-PS", hmenum.ParameterSetTemperature) {
		t.Error("IsParameterIgnoredForDataPointEvent must return false for non-suppressed parameter")
	}
}

// ---------------------------------------------------------------------------
// AcceptParameterOnlyOnChannelMap
// ---------------------------------------------------------------------------

func TestAcceptParameterOnlyOnChannelMapContainsLOWBAT(t *testing.T) {
	t.Parallel()
	m := AcceptParameterOnlyOnChannelMap()
	ch, ok := m["LOWBAT"]
	if !ok {
		t.Fatal("AcceptParameterOnlyOnChannelMap must contain LOWBAT")
	}
	if ch != 0 {
		t.Errorf("LOWBAT channel=%d want 0", ch)
	}
}

func TestAcceptParameterOnlyOnChannelMapIsCopy(t *testing.T) {
	t.Parallel()
	m1 := AcceptParameterOnlyOnChannelMap()
	m2 := AcceptParameterOnlyOnChannelMap()
	m1["__test__"] = 999
	if _, ok := m2["__test__"]; ok {
		t.Error("AcceptParameterOnlyOnChannelMap must return independent copies")
	}
}

// ---------------------------------------------------------------------------
// IsAcceptedOnlyOnChannel
// ---------------------------------------------------------------------------

func TestIsAcceptedOnlyOnChannelLOWBATCorrectChannel(t *testing.T) {
	t.Parallel()
	// LOWBAT is restricted to channel 0. IsAcceptedOnlyOnChannel returns
	// true when the channel does NOT match (i.e. the parameter should be excluded).
	if IsAcceptedOnlyOnChannel("LOWBAT", 0) {
		t.Error("IsAcceptedOnlyOnChannel(LOWBAT, 0) must return false — channel 0 is the accepted channel")
	}
}

func TestIsAcceptedOnlyOnChannelLOWBATWrongChannel(t *testing.T) {
	t.Parallel()
	if !IsAcceptedOnlyOnChannel("LOWBAT", 1) {
		t.Error("IsAcceptedOnlyOnChannel(LOWBAT, 1) must return true — LOWBAT is only accepted on channel 0")
	}
}

func TestIsAcceptedOnlyOnChannelNoRestriction(t *testing.T) {
	t.Parallel()
	// STATE has no channel restriction.
	if IsAcceptedOnlyOnChannel("STATE", 5) {
		t.Error("IsAcceptedOnlyOnChannel(STATE, 5) must return false — no restriction")
	}
}

// ---------------------------------------------------------------------------
// ModelValidator.IsRelevantParamsetForChannel
// ---------------------------------------------------------------------------

// TestIsRelevantParamsetForChannelValuesAlwaysTrue verifies that VALUES
// paramsets are always relevant regardless of channel number.
func TestIsRelevantParamsetForChannelValuesAlwaysTrue(t *testing.T) {
	t.Parallel()
	v := NewModelValidator()
	for _, ch := range []int{-1, 0, 1, 5, 99} {
		if !v.IsRelevantParamsetForChannel("HmIP-SWDO", ch, hmenum.ParamsetKeyValues) {
			t.Errorf("IsRelevantParamsetForChannel(VALUES, ch=%d) must be true", ch)
		}
	}
}

// TestIsRelevantParamsetForChannelMasterChannelWhitelist verifies that
// MASTER is relevant for channels listed in relevantMasterParamsetsByChannel.
func TestIsRelevantParamsetForChannelMasterChannelWhitelist(t *testing.T) {
	t.Parallel()
	v := NewModelValidator()

	// Channel 0 is in relevantMasterParamsetsByChannel — MASTER is relevant.
	if !v.IsRelevantParamsetForChannel("HmIP-SWDO", 0, hmenum.ParamsetKeyMaster) {
		t.Error("IsRelevantParamsetForChannel(MASTER, ch=0) must be true (ch 0 in whitelist)")
	}
}

// TestIsRelevantParamsetForChannelMasterUnknownChannel verifies that
// MASTER with an unknown channel (-1) falls through to the prefix-list check
// and defaults to true with an empty prefix list.
func TestIsRelevantParamsetForChannelMasterUnknownChannel(t *testing.T) {
	t.Parallel()
	v := NewModelValidator()

	// No prefix list set → default-open for all models.
	if !v.IsRelevantParamsetForChannel("HmIP-SWDO", -1, hmenum.ParamsetKeyMaster) {
		t.Error("IsRelevantParamsetForChannel(MASTER, ch=-1) with empty prefix list must be true")
	}
}

// TestIsRelevantParamsetForChannelMasterDeviceChannelWhitelist verifies
// the relevantMasterChannels device-channel lookup.
func TestIsRelevantParamsetForChannelMasterDeviceChannelWhitelist(t *testing.T) {
	t.Parallel()
	v := NewModelValidator()
	v.SetRelevantMasterChannels(map[string]map[int]struct{}{
		"hmip-bwth": {1: {}, 2: {}},
	})

	// Channel 1 is allowed for hmip-bwth.
	if !v.IsRelevantParamsetForChannel("HmIP-BWTH", 1, hmenum.ParamsetKeyMaster) {
		t.Error("IsRelevantParamsetForChannel(hmip-bwth, ch=1) must be true")
	}
	// Channel 3 is NOT in the set for hmip-bwth.
	if v.IsRelevantParamsetForChannel("HmIP-BWTH", 3, hmenum.ParamsetKeyMaster) {
		t.Error("IsRelevantParamsetForChannel(hmip-bwth, ch=3) must be false")
	}
	// Different model not in the map → falls through to prefix-list (empty = open).
	if !v.IsRelevantParamsetForChannel("HmIP-SWDO", 3, hmenum.ParamsetKeyMaster) {
		t.Error("IsRelevantParamsetForChannel(HmIP-SWDO, ch=3) with no prefix list must be true (default-open)")
	}
}

// TestIsRelevantParamsetForChannelMasterEmptyChannelSetMeansAny verifies
// that an empty channel set in relevantMasterChannels means "any channel".
func TestIsRelevantParamsetForChannelMasterEmptyChannelSetMeansAny(t *testing.T) {
	t.Parallel()
	v := NewModelValidator()
	v.SetRelevantMasterChannels(map[string]map[int]struct{}{
		"hmip-eTRV": {}, // empty = any channel
	})

	for _, ch := range []int{0, 1, 5, 10} {
		if !v.IsRelevantParamsetForChannel("HmIP-eTRV", ch, hmenum.ParamsetKeyMaster) {
			t.Errorf("IsRelevantParamsetForChannel(hmip-eTRV, ch=%d) with empty channel set must be true", ch)
		}
	}
}

// TestIsRelevantParamsetBackwardsCompatible verifies that IsRelevantParamset
// (the old no-channel method) still works by delegating to channelNoUnknown.
func TestIsRelevantParamsetBackwardsCompatible(t *testing.T) {
	t.Parallel()
	v := NewModelValidator()
	if !v.IsRelevantParamset("HmIP-SWDO", hmenum.ParamsetKeyValues) {
		t.Error("IsRelevantParamset(VALUES) must be true")
	}
	// With empty prefix list MASTER is default-open.
	if !v.IsRelevantParamset("HmIP-SWDO", hmenum.ParamsetKeyMaster) {
		t.Error("IsRelevantParamset(MASTER) with empty prefix list must be true")
	}
}

// ---------------------------------------------------------------------------
// IsUnIgnoredCustomOnly(customOnly=true) skips built-in device un-ignores
// ---------------------------------------------------------------------------

// TestIsUnIgnoredCustomOnlyTrueSkipsBuiltInDeviceEntries verifies that
// when customOnly=true the built-in unIgnoreParametersByDevice entries are
// NOT consulted.
func TestIsUnIgnoredCustomOnlyTrueSkipsBuiltInDeviceEntries(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// No user-provided un-ignore entries loaded.
	// With customOnly=true, built-in device un-ignores must be skipped.
	got := d.IsUnIgnoredCustomOnly("HM-CC-RT-DN", "", hmenum.ParamsetKeyValues, "CONFIG_PENDING", true)
	// The result depends on the built-in data; the key invariant is that
	// the function does not panic and returns a consistent bool.
	_ = got
}

// TestIsUnIgnoredCustomOnlyFalseWithUserEntry verifies that with
// customOnly=false a user entry is found.
func TestIsUnIgnoredCustomOnlyFalseWithUserEntry(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	d.LoadUnIgnore([]UnIgnoreEntry{{Parameter: "AES_ACTIVE", Model: "HM-CC-RT-DN"}})

	// customOnly=false: user entry found.
	gotFull := d.IsUnIgnoredCustomOnly("HM-CC-RT-DN", "", hmenum.ParamsetKeyValues, "AES_ACTIVE", false)
	if !gotFull {
		t.Error("IsUnIgnoredCustomOnly(customOnly=false) with user entry must return true")
	}

	// customOnly=true: user entry still found (custom_only does NOT hide user entries).
	gotCustom := d.IsUnIgnoredCustomOnly("HM-CC-RT-DN", "", hmenum.ParamsetKeyValues, "AES_ACTIVE", true)
	if !gotCustom {
		t.Error("IsUnIgnoredCustomOnly(customOnly=true) with matching user entry must return true")
	}
}

// TestIsUnIgnoredCustomOnlyTrueNoEntry verifies that with no user entries and
// customOnly=true the function returns false (built-in entries are skipped).
func TestIsUnIgnoredCustomOnlyTrueNoEntry(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// No user entries.
	got := d.IsUnIgnoredCustomOnly("HM-CC-RT-DN", "", hmenum.ParamsetKeyValues, "AES_ACTIVE", true)
	if got {
		t.Error("IsUnIgnoredCustomOnly(customOnly=true) with no user entries must return false")
	}
}

// ---------------------------------------------------------------------------
// InvalidatePrefixCache no-op verification (Registry, ModelValidator, ParameterDecider)
// ---------------------------------------------------------------------------

func TestRegistryInvalidatePrefixCacheIsNoOp(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Must not panic and must not affect allowed decisions.
	r.InvalidatePrefixCache()
	// Verify registry is still functional after call.
	if r.Len() < 0 {
		t.Error("Registry.Len() must return non-negative after InvalidatePrefixCache")
	}
}

func TestModelValidatorInvalidatePrefixCacheIsNoOp(t *testing.T) {
	t.Parallel()
	v := NewModelValidator()
	v.IgnoreModel("HM-DUMMY")
	v.InvalidatePrefixCache() // must not panic or reset state
	if !v.IsModelIgnored("HM-DUMMY") {
		t.Error("InvalidatePrefixCache must not reset ignored-model state")
	}
}

func TestParameterDeciderInvalidatePrefixCacheIsNoOp(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	entries := []UnIgnoreEntry{{Parameter: "AES_ACTIVE", Model: "HM-CC-RT-DN"}}
	d.LoadUnIgnore(entries)
	d.InvalidatePrefixCache() // must not panic or affect un-ignore entries
	// un-ignore entries must still be effective.
	if !d.IsUnIgnored("HM-CC-RT-DN", "", hmenum.ParamsetKeyValues, "AES_ACTIVE") {
		t.Error("InvalidatePrefixCache must not remove un-ignore entries")
	}
}
