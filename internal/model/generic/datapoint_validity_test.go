// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─── IsUnitFixed ────────────────────────────────────────────────────────

// TestIsUnitFixedRawEqualsClean verifies that a parameter whose raw CCU unit
// is already the canonical form reports IsUnitFixed() = false.
func TestIsUnitFixedRawEqualsClean(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Descriptor.Unit = "°C"
	dp := NewDataPoint[float64](cfg)

	if dp.IsUnitFixed() {
		t.Fatal("IsUnitFixed() must be false when raw unit matches cleaned unit")
	}
}

// TestIsUnitFixedRawDiffersFromClean verifies that a parameter whose raw CCU
// unit differs from the cleaned-up form reports IsUnitFixed() = true.
// "100%" is cleaned to "%" by CleanupUnit; the raw and clean units differ.
func TestIsUnitFixedRawDiffersFromClean(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Descriptor.Unit = "100%"
	dp := NewDataPoint[float64](cfg)

	if !dp.IsUnitFixed() {
		t.Fatal("IsUnitFixed() must be true when raw unit '100%' is cleaned to '%'")
	}
}

// ─── HasValidValueType ───────────────────────────────────────────────────

// TestHasValidValueTypeUnrefreshed verifies that a freshly constructed
// DataPoint[T] (no events yet) reports HasValidValueType() = false.
func TestHasValidValueTypeUnrefreshed(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if dp.HasValidValueType() {
		t.Fatal("HasValidValueType() must be false before first observation")
	}
}

// TestHasValidValueTypeAfterObservation verifies that after OnEvent the type
// check passes.
func TestHasValidValueTypeAfterObservation(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.OnEvent(0.75)
	if !dp.HasValidValueType() {
		t.Fatal("HasValidValueType() must be true after first observation")
	}
}

// ─── HasStatusParameter ──────────────────────────────────────────────────────
//
// HasStatusParameter() mirrors the Python semantics — it checks whether a
// status-parameter *name* has been registered via SetStatusParameter(), NOT
// whether a status event was received via UpdateStatus().

// TestHasStatusParameterNoNameRegistered verifies that a freshly constructed
// DataPoint reports HasStatusParameter() = false when no status parameter
// name has been registered.
func TestHasStatusParameterNoNameRegistered(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if dp.HasStatusParameter() {
		t.Fatal("HasStatusParameter() must be false when no status parameter name has been registered")
	}
}

// TestHasStatusParameterAfterUpdateStatusStillFalse verifies that receiving
// a status event via UpdateStatus does NOT set HasStatusParameter() = true.
// Mirrors the fix: the old event-driven behaviour was incorrect —
// HasStatusParameter answers "is a paired STATUS param registered?", not
// "has a STATUS event arrived?".
func TestHasStatusParameterAfterUpdateStatusStillFalse(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.UpdateStatus(hmenum.ParameterStatusNormal)
	if dp.HasStatusParameter() {
		t.Fatal("HasStatusParameter() must remain false after UpdateStatus — only SetStatusParameter sets it")
	}
}

// TestHasStatusParameterAfterSetStatusParameter verifies that calling
// SetStatusParameter with a non-empty name sets HasStatusParameter() = true.
// This is the correct way to register a paired status parameter.
func TestHasStatusParameterAfterSetStatusParameter(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.SetStatusParameter("LEVEL_STATUS", nil)
	if !dp.HasStatusParameter() {
		t.Fatal("HasStatusParameter() must be true after SetStatusParameter with a non-empty name")
	}
}

// TestHasStatusParameterClearedByEmptyName verifies that calling
// SetStatusParameter with an empty name clears HasStatusParameter().
func TestHasStatusParameterClearedByEmptyName(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.SetStatusParameter("LEVEL_STATUS", nil)
	dp.SetStatusParameter("", nil)
	if dp.HasStatusParameter() {
		t.Fatal("HasStatusParameter() must be false after SetStatusParameter with an empty name")
	}
}

// ─── RequiresPolling ─────────────────────────────────────────────────────────
//
// RequiresPolling() mirrors the two-part Python condition (data_point.py:1033).

// TestRequiresPolllingValuesParamsetReturnsFalse verifies that a VALUES
// paramset DP on a push-capable interface does not require polling — VALUES
// parameters are pushed via CCU callbacks.
func TestRequiresPolllingValuesParamsetReturnsFalse(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	// baseCfg sets ParamsetKeyValues by default; NoPushUpdates defaults to false.
	if dp.RequiresPolling() {
		t.Fatal("RequiresPolling() must be false for VALUES paramset on a push-capable interface")
	}
}

// TestRequiresPolllingHMBidCosRFMasterReturnsTrue verifies that a MASTER
// paramset DP on a BidCos-RF (HM) interface requires polling — the CCU does
// not push MASTER changes for classic HomeMatic devices.
func TestRequiresPolllingHMBidCosRFMasterReturnsTrue(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Key = hmtypes.DataPointKey{
		InterfaceID:    string(hmenum.ProductGroupHM), // "BidCos-RF"
		ChannelAddress: "A:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
		Parameter:      string(hmenum.ParameterLevel),
	}
	dp := NewDataPoint[float64](cfg)
	if !dp.RequiresPolling() {
		t.Fatal("RequiresPolling() must be true for MASTER paramset on BidCos-RF (HM)")
	}
}

// TestRequiresPolllingHMWBidCosWiredMasterReturnsTrue verifies that a MASTER
// paramset DP on a BidCos-Wired (HMW) interface requires polling.
func TestRequiresPolllingHMWBidCosWiredMasterReturnsTrue(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Key = hmtypes.DataPointKey{
		InterfaceID:    string(hmenum.ProductGroupHmW), // "BidCos-Wired"
		ChannelAddress: "A:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
		Parameter:      string(hmenum.ParameterLevel),
	}
	dp := NewDataPoint[float64](cfg)
	if !dp.RequiresPolling() {
		t.Fatal("RequiresPolling() must be true for MASTER paramset on BidCos-Wired (HMW)")
	}
}

// TestRequiresPolllingHmIPMasterReturnsFalse verifies that a MASTER paramset
// DP on an HmIP-RF interface does NOT require polling (only HM/HMW MASTER
// needs polling; HmIP-RF pushes MASTER changes).
func TestRequiresPolllingHmIPMasterReturnsFalse(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Key = hmtypes.DataPointKey{
		InterfaceID:    string(hmenum.ProductGroupHmIP), // "HmIP-RF"
		ChannelAddress: "A:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
		Parameter:      string(hmenum.ParameterLevel),
	}
	dp := NewDataPoint[float64](cfg)
	if dp.RequiresPolling() {
		t.Fatal("RequiresPolling() must be false for MASTER paramset on HmIP-RF (push-capable, non-HM/HMW)")
	}
}

// TestRequiresPolllingNoPushUpdatesMakesValuesRequirePolling verifies that
// when Config.NoPushUpdates is true every parameter (including VALUES)
// requires polling — the interface cannot push.
func TestRequiresPolllingNoPushUpdatesMakesValuesRequirePolling(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.NoPushUpdates = true
	// ParamsetKey is VALUES (the default) — would normally return false.
	dp := NewDataPoint[float64](cfg)
	if !dp.RequiresPolling() {
		t.Fatal("RequiresPolling() must be true for VALUES paramset when NoPushUpdates=true")
	}
}

// TestRequiresPolllingNoPushUpdatesAndMasterAlsoRequiresPolling verifies
// that when NoPushUpdates is true a MASTER DP also requires polling
// (condition 1 short-circuits regardless of paramset or interface).
func TestRequiresPolllingNoPushUpdatesAndMasterAlsoRequiresPolling(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.NoPushUpdates = true
	cfg.Key = hmtypes.DataPointKey{
		InterfaceID:    string(hmenum.ProductGroupHmIP),
		ChannelAddress: "A:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
		Parameter:      string(hmenum.ParameterLevel),
	}
	dp := NewDataPoint[float64](cfg)
	if !dp.RequiresPolling() {
		t.Fatal("RequiresPolling() must be true when NoPushUpdates=true regardless of paramset")
	}
}

// ─── InjectSyntheticValue ────────────────────────────────────────────────────

// TestInjectSyntheticValuePinsRename confirms that the formerly named
// ForceToSensor method was renamed to InjectSyntheticValue to avoid
// confusion with [datapoint.BaseDataPointFields.MarkForcedSensor] which
// sets the categorical _is_forced_sensor flag. InjectSyntheticValue must
// still behave like OnEvent — updating the value and triggering callbacks.
func TestInjectSyntheticValuePinsRename(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))

	var got float64
	var fired bool
	dp.OnUpdate(func(_, next float64) {
		got = next
		fired = true
	})

	dp.InjectSyntheticValue(0.42)

	if !fired {
		t.Fatal("InjectSyntheticValue must trigger OnUpdate callbacks")
	}
	if got != 0.42 {
		t.Fatalf("InjectSyntheticValue: callback got %v, want 0.42", got)
	}
	v, observed := dp.Value()
	if !observed || v != 0.42 {
		t.Fatalf("Value() = (%v, %v), want (0.42, true)", v, observed)
	}
}

// ─── IsValid + IsStatusValid ──────────────────────────────────────────────

// TestIsValidFalseBeforeFirstObservation verifies that a fresh data point
// reports IsValid() = false — no CCU value has been received yet.
func TestIsValidFalseBeforeFirstObservation(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if dp.IsValid() {
		t.Fatal("IsValid() must be false before any value observation")
	}
}

// TestIsValidTrueAfterObservation verifies that a data point that has
// received at least one CCU value reports IsValid() = true (all four
// gates pass: refreshed, status valid, type valid, range valid).
// No Min/Max set → bounds check passes vacuously (no constraint).
func TestIsValidTrueAfterObservation(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.OnEvent(0.5)

	if !dp.IsValid() {
		t.Fatal("IsValid() must be true after first OnEvent with no bounds constraint")
	}
}

// TestIsStatusValidTrueWithoutStatusObservation verifies that a data
// point with no status update passes the status gate vacuously.
func TestIsStatusValidTrueWithoutStatusObservation(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if !dp.IsStatusValid() {
		t.Fatal("IsStatusValid() must be true when no status has been observed (vacuous)")
	}
}

// TestIsStatusValidFalseAfterOverflowStatus verifies that after
// UpdateStatus(OVERFLOW) the status gate fails.
func TestIsStatusValidFalseAfterOverflowStatus(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.UpdateStatus(hmenum.ParameterStatusOverflow)
	if dp.IsStatusValid() {
		t.Fatal("IsStatusValid() must be false when status is OVERFLOW")
	}
}

// TestIsStatusValidTrueAfterNormalStatus verifies that after
// UpdateStatus(NORMAL) the status gate passes.
func TestIsStatusValidTrueAfterNormalStatus(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.UpdateStatus(hmenum.ParameterStatusNormal)
	if !dp.IsStatusValid() {
		t.Fatal("IsStatusValid() must be true when status is NORMAL")
	}
}

// TestIsStatusValidTrueAfterUnknownStatus verifies that after
// UpdateStatus(UNKNOWN) the status gate still passes — the init-phase grace
// period before the CCU has reported a definitive quality reading.
func TestIsStatusValidTrueAfterUnknownStatus(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.UpdateStatus(hmenum.ParameterStatusUnknown)
	if !dp.IsStatusValid() {
		t.Fatal("IsStatusValid() must be true when status is UNKNOWN (init-phase, mirrors aiohomematic)")
	}
}

// TestIsValidFalseWhenStatusInvalid verifies that the IsValid() chain
// short-circuits when IsStatusValid() fails.
func TestIsValidFalseWhenStatusInvalid(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.OnEvent(0.5)
	dp.UpdateStatus(hmenum.ParameterStatusOverflow)
	if dp.IsValid() {
		t.Fatal("IsValid() must be false when status is OVERFLOW even after OnEvent")
	}
}

// ─── DataPointType ────────────────────────────────────────────────────────────

// TestDataPointTypeSensorKind verifies that a Sensor-kind DP returns
// DataPointTypeSensor.
func TestDataPointTypeSensorKind(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Kind = KindSensor
	dp := NewDataPoint[float64](cfg)
	if got, want := dp.DataPointType(), hmenum.DataPointTypeSensor; got != want {
		t.Fatalf("DataPointType() = %q, want %q", got, want)
	}
}

// TestDataPointTypeSwitchKind verifies Switch kind → DataPointTypeSwitch.
func TestDataPointTypeSwitchKind(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Kind = KindSwitch
	dp := NewDataPoint[bool](cfg)
	if got, want := dp.DataPointType(), hmenum.DataPointTypeSwitch; got != want {
		t.Fatalf("DataPointType() = %q, want %q", got, want)
	}
}

// TestDataPointTypeUnknownKind verifies that unknown kind returns empty.
func TestDataPointTypeUnknownKind(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	// baseCfg sets KindUnknown by default.
	if got := dp.DataPointType(); got != "" {
		t.Fatalf("DataPointType() with unknown kind = %q, want empty", got)
	}
}

// ─── IsStateChange ────────────────────────────────────────────────────────────

// TestIsStateChangeSameValueConfirmed verifies that sending the same value as
// the current confirmed value is not a state change (validateStateChange=true
// for non-action kinds).
func TestIsStateChangeSameValueConfirmed(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Kind = KindSensor
	dp := NewDataPoint[float64](cfg)
	dp.OnEvent(0.5)

	if dp.IsStateChange(0.5) {
		t.Fatal("IsStateChange(0.5) must be false when confirmed value is already 0.5")
	}
}

// TestIsStateChangeDifferentValue verifies that a different value is a state change.
func TestIsStateChangeDifferentValue(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Kind = KindSensor
	dp := NewDataPoint[float64](cfg)
	dp.OnEvent(0.5)

	if !dp.IsStateChange(0.9) {
		t.Fatal("IsStateChange(0.9) must be true when confirmed value is 0.5")
	}
}

// TestIsStateChangeNeverObserved verifies that an unobserved DP always
// reports a state change.
func TestIsStateChangeNeverObserved(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if !dp.IsStateChange(0.0) {
		t.Fatal("IsStateChange must be true when no value has been observed yet")
	}
}

// TestIsStateChangeActionKindAlwaysTrue verifies that Action/Button kinds
// always report IsStateChange=true (they skip state-change validation).
func TestIsStateChangeActionKindAlwaysTrue(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Kind = KindAction
	dp := NewDataPoint[bool](cfg)
	dp.OnEvent(false)

	// Even for the same value, action must report state change.
	if !dp.IsStateChange(false) {
		t.Fatal("IsStateChange must always be true for Action kind")
	}
}

// ─── IsRetryable ──────────────────────────────────────────────────────────────

// TestIsRetryableSensorKindTrue verifies non-action kinds are retryable.
func TestIsRetryableSensorKindTrue(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Kind = KindSensor
	dp := NewDataPoint[float64](cfg)
	if !dp.IsRetryable() {
		t.Fatal("Sensor kind must be retryable by default")
	}
}

// TestIsRetryableActionKindFalse verifies action kinds are not retryable.
func TestIsRetryableActionKindFalse(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Kind = KindAction
	dp := NewDataPoint[bool](cfg)
	if dp.IsRetryable() {
		t.Fatal("Action kind must not be retryable by default")
	}
}

// TestIsRetryableButtonKindFalse verifies button kinds are not retryable.
func TestIsRetryableButtonKindFalse(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Kind = KindButton
	dp := NewDataPoint[bool](cfg)
	if dp.IsRetryable() {
		t.Fatal("Button kind must not be retryable by default")
	}
}

// ─── ValidatesStateChange ────────────────────────────────────────────────────

// TestValidatesStateChangeSensorTrue verifies that sensor kinds validate.
func TestValidatesStateChangeSensorTrue(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Kind = KindSensor
	dp := NewDataPoint[float64](cfg)
	if !dp.ValidatesStateChange() {
		t.Fatal("Sensor kind must validate state change")
	}
}

// TestValidatesStateChangeActionFalse verifies that Action kinds skip
// state-change validation.
func TestValidatesStateChangeActionFalse(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Kind = KindAction
	dp := NewDataPoint[bool](cfg)
	if dp.ValidatesStateChange() {
		t.Fatal("Action kind must not validate state change")
	}
}

// ─── GetCommandPriority ──────────────────────────────────────────────────────

// TestGetCommandPriorityReturnsHigh verifies that generic data points
// return CommandPriorityHigh.
func TestGetCommandPriorityReturnsHigh(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if got, want := dp.GetCommandPriority(), hmenum.CommandPriorityHigh; got != want {
		t.Fatalf("GetCommandPriority() = %d, want %d", got, want)
	}
}

// ─── GetEventData ─────────────────────────────────────────────────────────────

// TestGetEventDataNoObservationReturnsNilValue verifies that GetEventData
// returns nil value when the data point has not been observed.
func TestGetEventDataNoObservationReturnsNilValue(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	ed := dp.GetEventData()
	if ed.Value != nil {
		t.Fatalf("GetEventData().Value = %v, want nil when unobserved", ed.Value)
	}
	if ed.Parameter != string(hmenum.ParameterLevel) {
		t.Fatalf("GetEventData().Parameter = %q, want %q", ed.Parameter, hmenum.ParameterLevel)
	}
}

// TestGetEventDataAfterObservationReturnsValue verifies that after OnEvent
// the event data carries the observed value.
func TestGetEventDataAfterObservationReturnsValue(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.OnEvent(0.75)
	ed := dp.GetEventData()
	v, ok := ed.Value.(float64)
	if !ok || v != 0.75 {
		t.Fatalf("GetEventData().Value = %v (%T), want float64(0.75)", ed.Value, ed.Value)
	}
}

// ─── AllowedInternalParameters ───────────────────────────────────────────────

// TestAllowedInternalParametersContainsCOP verifies that
// CHANNEL_OPERATION_MODE is in the allowed set — it is required for
// operation-mode gating logic.
func TestAllowedInternalParametersContainsCOP(t *testing.T) {
	t.Parallel()

	if _, ok := AllowedInternalParameters["CHANNEL_OPERATION_MODE"]; !ok {
		t.Fatal("AllowedInternalParameters must contain CHANNEL_OPERATION_MODE")
	}
}

// ─── SetValidityGate ─────────────────────────────────────────────────────────

// TestValidityGateVetoesOtherwiseValidDataPoint verifies that an installed
// gate can invalidate a data point whose four intrinsic checks all pass. The
// seam exists for derived data points: a calculated sensor's own value has no
// descriptor to validate against, so its validity is decided by the sources it
// was computed from.
func TestValidityGateVetoesOtherwiseValidDataPoint(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.OnEvent(20.0)
	if !dp.IsValid() {
		t.Fatal("precondition: an observed in-range value must be valid")
	}

	sourcesUsable := false
	dp.SetValidityGate(func() bool { return sourcesUsable })
	if dp.IsValid() {
		t.Fatal("a gate returning false must invalidate the data point")
	}

	sourcesUsable = true
	if !dp.IsValid() {
		t.Fatal("a gate returning true must leave the intrinsic checks in charge")
	}

	dp.SetValidityGate(nil)
	if !dp.IsValid() {
		t.Fatal("clearing the gate must restore the ungated verdict")
	}
}

// TestValidityGateDoesNotRescueAnInvalidDataPoint verifies the gate is an
// additional condition, not an override: a passing gate cannot make an
// unobserved data point valid.
func TestValidityGateDoesNotRescueAnInvalidDataPoint(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.SetValidityGate(func() bool { return true })

	if dp.IsValid() {
		t.Fatal("an unobserved data point must stay invalid regardless of the gate")
	}
}
