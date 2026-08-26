// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for ordering and cache-coherency invariants of the patch Registry:
// patches must widen Min/Max before parameter.Validate reads them, and
// concurrent ApplyTo must not race on Registry internals.

package patches

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// makeJSON encodes a numeric literal as json.RawMessage.
func makeJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(b)
}

// writableFloat returns a ParameterData for a FLOAT parameter that
// supports Read+Write and has Min=lo, Max=hi.
func writableFloat(lo, hi float64) hmproto.ParameterData {
	return hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		Min:        makeJSON(lo),
		Max:        makeJSON(hi),
	}
}

// TestPatchAppliedBeforeBoundsValidation is the primary §11/10 invariant:
// a patch that widens Min must fire BEFORE parameter.Validate inspects
// the bounds; if the order is reversed the value is incorrectly rejected.
//
// Invariant: ApplyTo(pd) → Validate(pd, v) must accept v when the patch
// widens the range to include v. Validate(pd, v) without the patch must
// reject v.
func TestPatchAppliedBeforeBoundsValidation(t *testing.T) {
	t.Parallel()

	// A patch that widens Min from 0 to -10 for the fictional
	// WIND_SPEED parameter on model "HM-Test".
	widenMin := Patch{
		Model:     "HM-Test",
		Parameter: hmenum.ParameterWindSpeed,
		Paramset:  hmenum.ParamsetKeyValues,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Min = makeJSON(-10.0)
			return true
		},
	}

	// "raw" descriptor as it would arrive from the CCU: Min=0, Max=100.
	// The value -5 is below Min=0 and would be rejected without the patch.
	rawPD := func() hmproto.ParameterData { return writableFloat(0, 100) }
	val := hmtypes.FloatValue(-5.0)

	// Control case: validate WITHOUT applying the patch → must reject.
	if err := parameter.Validate(rawPD(), val); err == nil {
		t.Fatal("control: expected Validate to reject -5 when Min=0 (no patch applied)")
	}

	// Correct order: patch first, then validate → must accept.
	r := &Registry{}
	r.Register(widenMin)

	pd := rawPD()
	changes := r.ApplyTo("HM-Test", hmenum.ParamsetKeyValues, hmenum.ParameterWindSpeed, &pd)
	if changes == 0 {
		t.Fatal("patch must fire and report a change")
	}
	if err := parameter.Validate(pd, val); err != nil {
		t.Fatalf("patch+validate: unexpected rejection after patch widened Min: %v", err)
	}
}

// TestPatchIdempotentMultipleRegistries ensures that calling ApplyTo twice
// on the same (already-patched) ParameterData returns 0 on the second call
// (idempotent guard inside Apply).
func TestPatchIdempotentMultipleRegistries(t *testing.T) {
	t.Parallel()

	r := &Registry{}
	r.Register(Patch{
		Parameter: hmenum.ParameterWindSpeed,
		Apply: func(pd *hmproto.ParameterData) bool {
			if pd.Unit == "m/s" {
				return false // already set — no change
			}
			pd.Unit = "m/s"
			return true
		},
	})

	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}

	first := r.ApplyTo("any", hmenum.ParamsetKeyValues, hmenum.ParameterWindSpeed, pd)
	if first == 0 {
		t.Fatal("first apply must change the descriptor")
	}

	second := r.ApplyTo("any", hmenum.ParamsetKeyValues, hmenum.ParameterWindSpeed, pd)
	if second != 0 {
		t.Fatalf("second apply must return 0 (idempotent), got %d", second)
	}
}

// TestPatchScopedByModel verifies that a patch registered with a specific
// Model does not fire for a different model.
func TestPatchScopedByModel(t *testing.T) {
	t.Parallel()

	r := &Registry{}
	r.Register(Patch{
		Model:     "HmIP-RGBW",
		Parameter: hmenum.ParameterHue,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "°"
			return true
		},
	})

	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	changes := r.ApplyTo("HmIP-OTHER", hmenum.ParamsetKeyValues, hmenum.ParameterHue, pd)
	if changes != 0 {
		t.Fatalf("patch scoped to HmIP-RGBW must not fire for HmIP-OTHER, got %d changes", changes)
	}
	if pd.Unit != "" {
		t.Fatalf("Unit must be unchanged, got %q", pd.Unit)
	}
}

// TestPatchScopedByParamset verifies that a patch registered for
// ParamsetKeyValues does not fire when called with ParamsetKeyMaster.
func TestPatchScopedByParamset(t *testing.T) {
	t.Parallel()

	r := &Registry{}
	r.Register(Patch{
		Parameter: hmenum.ParameterLevel,
		Paramset:  hmenum.ParamsetKeyValues,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "%"
			return true
		},
	})

	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	changes := r.ApplyTo("any", hmenum.ParamsetKeyMaster, hmenum.ParameterLevel, pd)
	if changes != 0 {
		t.Fatalf("patch scoped to VALUES must not fire for MASTER, got %d changes", changes)
	}
}

// TestPatchEmptyModelMatchesAny documents that Model=="" fires for every
// model string, and Paramset=="" fires for every paramset key.
func TestPatchEmptyModelMatchesAny(t *testing.T) {
	t.Parallel()

	r := &Registry{}
	r.Register(Patch{
		// Model="" and Paramset="" → matches any model and any paramset.
		Parameter: hmenum.ParameterTemperature,
		Apply: func(pd *hmproto.ParameterData) bool {
			if pd.Unit == "°C" {
				return false
			}
			pd.Unit = "°C"
			return true
		},
	})

	for _, model := range []string{"HmIP-RGBW", "HmIP-Other", "HM-CC-RT-DN"} {
		for _, ps := range []hmenum.ParamsetKey{hmenum.ParamsetKeyValues, hmenum.ParamsetKeyMaster} {
			pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
			changes := r.ApplyTo(model, ps, hmenum.ParameterTemperature, pd)
			if changes == 0 {
				t.Errorf("Model=%q Paramset=%q: empty-model/paramset patch must fire, got 0 changes", model, ps)
			}
			if pd.Unit != "°C" {
				t.Errorf("Model=%q Paramset=%q: Unit=%q want °C", model, ps, pd.Unit)
			}
		}
	}
}

// TestPatchRegistryConcurrentApply registers one custom patch, fans out
// 100 goroutines each calling ApplyTo on a fresh *ParameterData, and
// verifies no data race and consistent change-count return values.
// Run with -race.
func TestPatchRegistryConcurrentApply(t *testing.T) {
	t.Parallel()

	r := &Registry{}
	r.Register(Patch{
		Parameter: hmenum.ParameterHumidity,
		Apply: func(pd *hmproto.ParameterData) bool {
			if pd.Unit == "%" {
				return false
			}
			pd.Unit = "%"
			return true
		},
	})

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
			changes := r.ApplyTo("HM-WDS40-TH-I", hmenum.ParamsetKeyValues, hmenum.ParameterHumidity, pd)
			if changes != 1 {
				// Use t.Logf to avoid races on t.Errorf from multiple goroutines;
				// a non-1 count is still detected via the WaitGroup join below.
				t.Errorf("expected 1 change, got %d", changes)
			}
		}()
	}
	wg.Wait()
}

// TestPatchOrderingFirstMatchWins verifies that after the first
// matching patch in registration order wins (most-specific-first semantics).
// Two patches that share the same tier (no channel, no paramset) result in
// exactly one change and the first-registered patch's value is used.
func TestPatchOrderingFirstMatchWins(t *testing.T) {
	t.Parallel()

	r := &Registry{}
	r.Register(Patch{
		Parameter: hmenum.ParameterPower,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "W"
			return true
		},
	})
	r.Register(Patch{
		Parameter: hmenum.ParameterPower,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "kW"
			return true
		},
	})

	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	changes := r.ApplyTo("any", hmenum.ParamsetKeyValues, hmenum.ParameterPower, pd)
	// First-match: only 1 patch fires.
	if changes != 1 {
		t.Fatalf("expected 1 patch to fire (first-match semantics), got %d", changes)
	}
	// First-registered patch must win.
	if pd.Unit != "W" {
		t.Fatalf("Unit=%q want W (first-registered patch must win with first-match semantics)", pd.Unit)
	}
}

// TestPatchOrderingSpecificOverridesGeneric verifies the tier priority:
// a patch with an explicit paramset (tier 1/2) overrides a generic patch
// (tier 4) even when the generic was registered first.
func TestPatchOrderingSpecificOverridesGeneric(t *testing.T) {
	t.Parallel()

	r := &Registry{}
	// Generic patch registered first.
	r.Register(Patch{
		Parameter: hmenum.ParameterPower,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "W"
			return true
		},
	})
	// More-specific patch: restricts to VALUES paramset.
	r.Register(Patch{
		Parameter: hmenum.ParameterPower,
		Paramset:  hmenum.ParamsetKeyValues,
		Apply: func(pd *hmproto.ParameterData) bool {
			pd.Unit = "kW"
			return true
		},
	})

	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
	changes := r.ApplyTo("any", hmenum.ParamsetKeyValues, hmenum.ParameterPower, pd)
	if changes != 1 {
		t.Fatalf("expected 1 patch to fire, got %d", changes)
	}
	// Specific patch (tier 2 = paramset match, no channel) must win over
	// generic patch (tier 4 = no paramset, no channel).
	if pd.Unit != "kW" {
		t.Fatalf("Unit=%q want kW (specific paramset patch must override generic)", pd.Unit)
	}
}
