// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for first-match (most-specific-first) patch semantics in
// Registry.applyToWithChannel.

package patches

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestFirstMatchExactChannelParamsetBeatsGeneric verifies that a patch with
// both channel+paramset (tier 1 = most specific) beats a patch with neither
// (tier 4 = least specific), regardless of registration order.
func TestFirstMatchExactChannelParamsetBeatsGeneric(t *testing.T) {
	t.Parallel()

	ch1 := 1
	r := &Registry{}
	// Generic patch registered first (tier 4).
	r.Register(Patch{
		Parameter: hmenum.ParameterLevel,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "generic"
			return true
		},
	})
	// Exact-tier patch registered after (tier 1: channel + paramset).
	r.Register(Patch{
		Parameter: hmenum.ParameterLevel,
		ChannelNo: &ch1,
		Paramset:  hmenum.ParamsetKeyValues,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "exact"
			return true
		},
	})

	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	// ApplyParamset calls applyToWithChannel internally with channelNo=1.
	ps := hmproto.Paramset{"LEVEL": *pd}
	r.ApplyParamset("any", "DEV001:1", hmenum.ParamsetKeyValues, ps)
	result := ps["LEVEL"]
	if result.Unit != "exact" {
		t.Errorf("Unit=%q want 'exact' (tier-1 exact patch must win over tier-4 generic)", result.Unit)
	}
}

// TestFirstMatchNoChannelParamsetBeatsNoChannelNoParamset verifies that tier 2
// (no channel + paramset) beats tier 4 (no channel + no paramset).
func TestFirstMatchNoChannelParamsetBeatsNoChannelNoParamset(t *testing.T) {
	t.Parallel()

	r := &Registry{}
	// Tier 4 (no channel, no paramset) registered first.
	r.Register(Patch{
		Parameter: hmenum.ParameterTemperature,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "tier4"
			return true
		},
	})
	// Tier 2 (no channel, exact paramset) registered after.
	r.Register(Patch{
		Parameter: hmenum.ParameterTemperature,
		Paramset:  hmenum.ParamsetKeyValues,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "tier2"
			return true
		},
	})

	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	changes := r.ApplyTo("any", hmenum.ParamsetKeyValues, hmenum.ParameterTemperature, pd)
	if changes != 1 {
		t.Fatalf("expected 1 change, got %d", changes)
	}
	if pd.Unit != "tier2" {
		t.Errorf("Unit=%q want 'tier2'", pd.Unit)
	}
}

// TestFirstMatchOnlyOnePatchFires verifies that exactly one patch fires
// per call, even when multiple patches match.
func TestFirstMatchOnlyOnePatchFires(t *testing.T) {
	t.Parallel()

	counter := 0
	r := &Registry{}
	for i := 0; i < 3; i++ {
		r.Register(Patch{
			Parameter: hmenum.ParameterHumidity,
			Apply: func(pd *hmproto.ParameterData) bool {
				counter++
				pd.Unit = "%"
				return true
			},
		})
	}

	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	r.ApplyTo("any", hmenum.ParamsetKeyValues, hmenum.ParameterHumidity, pd)
	if counter != 1 {
		t.Errorf("expected exactly 1 patch to fire, got %d", counter)
	}
}

// TestFirstMatchNoMatchReturns0 verifies that a patch that matched but
// returned false (no change) causes applyToWithChannel to return 0 and
// no lower-tier patch is consulted.
func TestFirstMatchNoMatchReturns0(t *testing.T) {
	t.Parallel()

	r := &Registry{}
	// Tier 4 patch that does nothing (returns false).
	r.Register(Patch{
		Parameter: hmenum.ParameterWindSpeed,
		Apply: func(pd *hmproto.ParameterData) bool {
			return false // no change
		},
	})
	// Would-be fallback — must NOT fire because tier 4 matched above.
	r.Register(Patch{
		Parameter: hmenum.ParameterWindSpeed,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "m/s"
			return true
		},
	})

	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	changes := r.ApplyTo("any", hmenum.ParamsetKeyValues, hmenum.ParameterWindSpeed, pd)
	if changes != 0 {
		t.Errorf("changes=%d want 0 (first-matched-but-no-change must stop search)", changes)
	}
	if pd.Unit != "" {
		t.Errorf("Unit=%q want '' (second patch must not fire)", pd.Unit)
	}
}

// TestFirstMatchChannelBeatsAnyChannel verifies tier 1 (channel+paramset)
// beats tier 2 (no channel + paramset).
func TestFirstMatchChannelBeatsAnyChannel(t *testing.T) {
	t.Parallel()

	ch1 := 1
	r := &Registry{}
	// Tier 2 registered first.
	r.Register(Patch{
		Parameter: hmenum.ParameterLevel,
		Paramset:  hmenum.ParamsetKeyValues,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "tier2"
			return true
		},
	})
	// Tier 1 registered after (channel + paramset).
	r.Register(Patch{
		Parameter: hmenum.ParameterLevel,
		ChannelNo: &ch1,
		Paramset:  hmenum.ParamsetKeyValues,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "tier1"
			return true
		},
	})

	ps := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	r.ApplyParamset("any", "DEV001:1", hmenum.ParamsetKeyValues, ps)
	if ps["LEVEL"].Unit != "tier1" {
		t.Errorf("Unit=%q want 'tier1' (tier-1 beats tier-2)", ps["LEVEL"].Unit)
	}
}
