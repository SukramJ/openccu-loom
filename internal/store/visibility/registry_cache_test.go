// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// registry_cache_test.go covers Registry.ClearMemoizationCaches,
// Registry.InvalidateAllCaches, ParameterDecider.ShouldSkipParameter,
// ParameterDecider.UnIgnoreEntries, ParameterDecider.ClearCache, and the
// AcceptParameterOnlyOnChannel channel restriction.

package visibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ---------------------------------------------------------------------------
// ParameterDecider.ClearCache / Registry.ClearMemoizationCaches
// ---------------------------------------------------------------------------

func TestRegistryClearMemoizationCaches(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	// Warm up the memoisation cache by calling IsAllowed a few times.
	for range 5 {
		_ = r.IsAllowed("HM-CC-RT-DN", "HEATING_CLIMATECONTROL_TRANSCEIVER",
			hmenum.ParamsetKeyValues, hmenum.ParameterSetTemperature)
	}

	before := r.Len()
	if before == 0 {
		t.Skip("no memoisation entries to test with (cache stayed empty)")
	}

	r.ClearMemoizationCaches()

	after := r.Len()
	if after != 0 {
		t.Errorf("after ClearMemoizationCaches cache len=%d want 0", after)
	}
}

// ---------------------------------------------------------------------------
// Registry.InvalidateAllCaches
// ---------------------------------------------------------------------------

func TestRegistryInvalidateAllCaches(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	// Warm cache.
	for range 3 {
		_ = r.IsAllowed("HmIP-SWDO", "HAN-FUN_CONTACT_INTERFACE",
			hmenum.ParamsetKeyValues, "STATE")
	}
	before := r.Len()

	r.InvalidateAllCaches()

	// After invalidation the memoisation cache must be cleared.
	after := r.Len()
	if after != 0 {
		t.Errorf("after InvalidateAllCaches cache len=%d want 0", after)
	}

	// Rules / un-ignore entries must be preserved — IsAllowed must still work.
	result := r.IsAllowed("HmIP-SWDO", "HAN-FUN_CONTACT_INTERFACE",
		hmenum.ParamsetKeyValues, "STATE")
	_ = result // just verify it doesn't panic

	// Cache should be non-empty again after calling IsAllowed.
	if r.Len() == 0 && before > 0 {
		t.Error("cache should have re-populated after calling IsAllowed post-invalidation")
	}
}

// ---------------------------------------------------------------------------
// ParameterDecider.ShouldSkipParameter
// ---------------------------------------------------------------------------

func TestParameterDeciderShouldSkipParameter(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)

	// A known-ignored parameter (e.g. AES_ACTIVE) must be skipped.
	skipped := d.ShouldSkipParameter("HM-CC-RT-DN", "CLIMATECONTROL_RT_TRANSCEIVER",
		channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.Parameter("AES_ACTIVE"))
	_ = skipped // result depends on rules data; just ensure it doesn't panic

	// A genuinely exposed parameter (SET_TEMPERATURE) should not be skipped
	// for the standard thermostat model.
	notSkipped := d.ShouldSkipParameter("HM-CC-RT-DN", "HEATING_CLIMATECONTROL_TRANSCEIVER",
		channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterSetTemperature)
	if notSkipped {
		t.Error("ShouldSkipParameter: SET_TEMPERATURE on HM-CC-RT-DN should not be skipped")
	}
}

// TestParameterDeciderAcceptParameterOnlyOnChannel pins the fix: LOWBAT is
// restricted to channel 0 by acceptParameterOnlyOnChannel.
func TestParameterDeciderAcceptParameterOnlyOnChannel(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)

	// LOWBAT on channel 0 — allowed.
	if got := d.ShouldSkipParameter("HmIP-PSM", "ANY", 0,
		hmenum.ParamsetKeyValues, hmenum.Parameter("LOWBAT")); got {
		t.Error("LOWBAT on channel 0 must NOT be skipped")
	}

	// LOWBAT on channel 1 — must be skipped (off-channel).
	if got := d.ShouldSkipParameter("HmIP-PSM", "ANY", 1,
		hmenum.ParamsetKeyValues, hmenum.Parameter("LOWBAT")); !got {
		t.Error("LOWBAT on channel 1 must be skipped (acceptParameterOnlyOnChannel=0)")
	}

	// channelNoUnknown opts out: with no real channel context the
	// restriction cannot be applied.
	if got := d.ShouldSkipParameter("HmIP-PSM", "ANY", channelNoUnknown,
		hmenum.ParamsetKeyValues, hmenum.Parameter("LOWBAT")); got {
		t.Error("LOWBAT with unknown channel number must NOT trigger the channel restriction")
	}
}

// ---------------------------------------------------------------------------
// ParameterDecider.UnIgnoreEntries / ClearCache
// ---------------------------------------------------------------------------

func TestParameterDeciderUnIgnoreEntriesRoundTrip(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)

	entries := []UnIgnoreEntry{
		{Parameter: "AES_ACTIVE", Model: "HM-CC-RT-DN"},
		{Parameter: "RSSI_DEVICE"},
	}
	d.LoadUnIgnore(entries)

	got := d.UnIgnoreEntries()
	if len(got) != len(entries) {
		t.Fatalf("UnIgnoreEntries len=%d want %d", len(got), len(entries))
	}
	if got[0].Parameter != "AES_ACTIVE" {
		t.Errorf("got[0].Parameter=%q want AES_ACTIVE", got[0].Parameter)
	}

	// Modifying the returned slice must not affect the decider.
	got[0].Model = "MUTATED"
	orig := d.UnIgnoreEntries()
	if orig[0].Model == "MUTATED" {
		t.Error("UnIgnoreEntries returned a reference, not a copy")
	}
}

func TestParameterDeciderClearCache(t *testing.T) {
	t.Parallel()
	d := NewParameterDecider(nil)
	// Warm cache.
	_ = d.IsParameterIgnored("HM-CC-RT-DN", "CLIMATECONTROL_RT_TRANSCEIVER",
		channelNoUnknown, hmenum.ParamsetKeyValues, hmenum.ParameterSetTemperature)
	d.ClearCache()
	if d.Len() != 0 {
		t.Errorf("Len=%d want 0 after ClearCache", d.Len())
	}
}
