// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestUsageOverrideForcedSensor pins the head-of-chain rule: a DP flagged via
// [BaseDataPointFields.MarkForcedSensor] reports `Usage() == DataPoint`
// regardless of any subsequent forced-usage or Config.Usage value.
//
// if self._is_forced_sensor or self._is_un_ignored: return
// DataPointUsage.DATA_POINT
//
// Without this override an HmIP-eTRV.LEVEL DP that was both
// `_SWITCH_DP_TO_SENSOR`-marked and reached by the custom-DP suppression pass
// (which forces NoCreate) would render as hidden in HA — exactly the opposite
// of the intended outcome.
func TestUsageOverrideForcedSensor(t *testing.T) {
	t.Parallel()
	dp := makeUsageTestDP(t)
	dp.MarkForcedSensor()
	dp.SetForcedUsage(hmenum.DataPointUsageNoCreate)

	if got := dp.Usage(); got != hmenum.DataPointUsageDataPoint {
		t.Errorf("forced-sensor DP usage = %q, want DataPoint (override wins over forced NoCreate)", got)
	}
	if !dp.Visible() {
		t.Error("forced-sensor DP must be Visible() despite a NoCreate forced-usage")
	}
}

// TestUsageOverrideUnIgnored mirrors the same head for the operator
// `un_ignore` flag — even if the suppression pass demoted the DP to
// NoCreate, the un-ignore override re-promotes it.
func TestUsageOverrideUnIgnored(t *testing.T) {
	t.Parallel()
	dp := makeUsageTestDP(t)
	dp.MarkUnIgnored()
	dp.SetForcedUsage(hmenum.DataPointUsageNoCreate)

	if got := dp.Usage(); got != hmenum.DataPointUsageDataPoint {
		t.Errorf("un-ignored DP usage = %q, want DataPoint", got)
	}
	if !dp.Visible() {
		t.Error("un-ignored DP must be Visible() despite a NoCreate forced-usage")
	}
}

// TestUsageForcedUsageWinsOverConfig pins tier 2 of the chain:
// without the head-of-chain overrides, a forced-usage value (CDPVisible
// or NoCreate) overrides the constructor's Config.Usage.
func TestUsageForcedUsageWinsOverConfig(t *testing.T) {
	t.Parallel()
	dp := makeUsageTestDP(t)
	dp.SetForcedUsage(hmenum.DataPointUsageCDPVisible)

	if got := dp.Usage(); got != hmenum.DataPointUsageCDPVisible {
		t.Errorf("forced-usage = %q, want CDPVisible", got)
	}

	dp.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	if got := dp.Usage(); got != hmenum.DataPointUsageNoCreate {
		t.Errorf("forced-usage NoCreate = %q, want NoCreate", got)
	}
	if dp.Visible() {
		t.Error("forced NoCreate must hide the DP")
	}
}

// TestUsageDefaultWhenUnconfigured pins the default-tier: an
// unmarked DP with no Config.Usage falls through to DataPoint.
func TestUsageDefaultWhenUnconfigured(t *testing.T) {
	t.Parallel()
	dp := makeUsageTestDP(t)
	if got := dp.Usage(); got != hmenum.DataPointUsageDataPoint {
		t.Errorf("default usage = %q, want DataPoint", got)
	}
}

// makeUsageTestDP constructs a minimal *DataPoint[bool] for the
// override tests above.
func makeUsageTestDP(t *testing.T) *DataPoint[bool] {
	t.Helper()
	cfg := Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: "ABC0001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	}
	return NewDataPoint[bool](cfg)
}

// --- StateUncertain initial-true until first OnEvent ---

// TestNewDataPointHasStateUncertainTrueUntilRefreshed verifies that
// a freshly constructed DataPoint must be uncertain (initial-true) and
// must clear that flag once a CCU-confirmed value arrives via OnEvent.
// and the `self._state_uncertain = False` reset (data_point.py:1305).
func TestNewDataPointHasStateUncertainTrueUntilRefreshed(t *testing.T) {
	t.Parallel()
	dp := makeUsageTestDP(t)

	// Newly constructed → must be uncertain.
	if !dp.StateUncertain() {
		t.Fatal("new DataPoint must have StateUncertain() = true before first OnEvent")
	}

	// After receiving a confirmed CCU value → must clear the flag.
	dp.OnEvent(true)
	if dp.StateUncertain() {
		t.Fatal("after OnEvent, DataPoint must have StateUncertain() = false (no write in flight)")
	}
}

// TestStateUncertainClearsOnFirstEvent ensures the flag is false after
// the first event even when the value is the zero value.
func TestStateUncertainClearsOnFirstEvent(t *testing.T) {
	t.Parallel()
	dp := makeUsageTestDP(t)
	if !dp.StateUncertain() {
		t.Fatal("precondition: new DP must be uncertain")
	}
	dp.OnEvent(false) // zero value of bool — still a valid CCU event
	if dp.StateUncertain() {
		t.Fatal("zero-value CCU event must also clear stateUncertain flag")
	}
}

// --- EnabledByChannelOperationMode gate in Usage() ---

// TestUsageRespectsChannelOperationModeGate verifies that
// a DP whose operation-mode gate is set to false must return NoCreate
// from Usage(), regardless of Config.Usage and ForcedUsage.
func TestUsageRespectsChannelOperationModeGate(t *testing.T) {
	t.Parallel()
	dp := makeUsageTestDP(t)

	// Default: gate not set (nil) → Usage falls through normally.
	if got := dp.Usage(); got != hmenum.DataPointUsageDataPoint {
		t.Fatalf("gate not set: Usage() = %q, want DataPoint", got)
	}

	// Gate explicitly excluded → Usage must be NoCreate.
	dp.SetOperationModeAllowed(false)
	if got := dp.Usage(); got != hmenum.DataPointUsageNoCreate {
		t.Fatalf("gate=false: Usage() = %q, want NoCreate", got)
	}

	// Gate explicitly included → falls through to normal resolution.
	dp.SetOperationModeAllowed(true)
	if got := dp.Usage(); got != hmenum.DataPointUsageDataPoint {
		t.Fatalf("gate=true: Usage() = %q, want DataPoint", got)
	}
}

// TestUsageGateDoesNotOverrideForcedSensor ensures that the IsForcedSensor
// head-of-chain rule still wins over the operation-mode gate.
func TestUsageGateDoesNotOverrideForcedSensor(t *testing.T) {
	t.Parallel()
	dp := makeUsageTestDP(t)
	dp.MarkForcedSensor()
	dp.SetOperationModeAllowed(false)

	if got := dp.Usage(); got != hmenum.DataPointUsageDataPoint {
		t.Fatalf("IsForcedSensor must win over op-mode gate; got %q, want DataPoint", got)
	}
}

// --- HasValidValueType with OPTIONAL/ACTION parameters ---

// TestHasValidValueTypeAllowsNoneForOptionalParameter verifies that
// an unobserved DP whose parameter is in _OPTIONAL_PARAMETERS must pass
// HasValidValueType() even before any CCU event arrives.
func TestHasValidValueTypeAllowsNoneForOptionalParameter(t *testing.T) {
	t.Parallel()

	// Non-optional parameter → false before first CCU event.
	nonOpt := makeUsageTestDP(t) // STATE is not optional
	if nonOpt.HasValidValueType() {
		t.Fatal("non-optional unobserved DP must have HasValidValueType() = false")
	}
	nonOpt.OnEvent(true)
	if !nonOpt.HasValidValueType() {
		t.Fatal("after OnEvent, non-optional DP must have HasValidValueType() = true")
	}

	// Optional parameter (LEVEL_2 is in hmenum.OptionalParameters).
	optCfg := Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "ABC:1",
			Parameter:      "LEVEL_2",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead,
		},
	}
	optDP := NewDataPoint[float64](optCfg)
	if !optDP.HasValidValueType() {
		t.Fatal("optional unobserved DP must have HasValidValueType() = true (allows none value)")
	}
}

// TestHasValidValueTypeAllowsNoneForAction verifies that ACTION-kind DPs
// pass HasValidValueType() even when unobserved (no persistent value expected).
func TestHasValidValueTypeAllowsNoneForAction(t *testing.T) {
	t.Parallel()
	actionCfg := Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "ABC:1",
			Parameter:      "PRESS_SHORT",
		},
		Kind: KindAction,
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}
	dp := NewDataPoint[bool](actionCfg)
	if !dp.HasValidValueType() {
		t.Fatal("ACTION DP must have HasValidValueType() = true even unobserved")
	}
}
