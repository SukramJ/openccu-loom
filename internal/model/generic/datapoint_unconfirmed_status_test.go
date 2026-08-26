// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── _unconfirmed_value separate slot ────────────────────────────────────────

// TestUnconfirmedValueSlotSeparateFromConfirmed verifies that
// WriteUnconfirmedValue stores the value in the unconfirmed slot and does NOT
// update the confirmed (CCU-confirmed) value.
func TestUnconfirmedValueSlotSeparateFromConfirmed(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.OnEvent(0.25) // confirmed value

	dp.WriteUnconfirmedValue(0.75, time.Now())

	// ConfirmedValue must still be the original 0.25.
	confirmed, ok := dp.ConfirmedValue()
	if !ok || confirmed != 0.25 {
		t.Fatalf("ConfirmedValue()=(%v, %v) want (0.25, true)", confirmed, ok)
	}

	// UnconfirmedValue must be 0.75.
	unconfirmed, hasUnconfirmed := dp.UnconfirmedValue()
	if !hasUnconfirmed || unconfirmed != 0.75 {
		t.Fatalf("UnconfirmedValue()=(%v, %v) want (0.75, true)", unconfirmed, hasUnconfirmed)
	}

	// Value() must return the unconfirmed value.
	v, vok := dp.Value()
	if !vok || v != 0.75 {
		t.Fatalf("Value()=(%v, %v) want (0.75, true)", v, vok)
	}
}

// TestUnconfirmedValueClearedOnConfirmedEvent verifies that a CCU- confirmed
// event (OnEvent) clears the unconfirmed slot.
func TestUnconfirmedValueClearedOnConfirmedEvent(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.WriteUnconfirmedValue(0.9, time.Now())

	// Unconfirmed must be set before the confirmed event.
	_, hasUnconfirmed := dp.UnconfirmedValue()
	if !hasUnconfirmed {
		t.Fatal("unconfirmed value must be set before OnEvent")
	}

	dp.OnEvent(0.9) // CCU confirms the same value

	// Unconfirmed slot must be cleared.
	_, hasUnconfirmed = dp.UnconfirmedValue()
	if hasUnconfirmed {
		t.Fatal("unconfirmed value must be cleared after OnEvent")
	}
}

// TestUnconfirmedValueFreshDPHasNone verifies that a freshly constructed
// DataPoint has no unconfirmed value.
func TestUnconfirmedValueFreshDPHasNone(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	_, hasUnconfirmed := dp.UnconfirmedValue()
	if hasUnconfirmed {
		t.Fatal("fresh DataPoint must not have an unconfirmed value")
	}
}

// ─── write_unconfirmed_value writes to unconfirmed slot only ─────────────────

// TestWriteUnconfirmedValueDoesNotEngageOptimistic verifies that
// WriteUnconfirmedValue does NOT engage the optimistic tracker.
func TestWriteUnconfirmedValueDoesNotEngageOptimistic(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.WriteUnconfirmedValue(0.5, time.Now())
	if dp.IsOptimistic() {
		t.Fatal("WriteUnconfirmedValue must not engage the optimistic tracker")
	}
}

// TestWriteUnconfirmedValueFiresCallbacks verifies that WriteUnconfirmedValue
// fires OnUpdate callbacks so subscribers see the new value immediately.
func TestWriteUnconfirmedValueFiresCallbacks(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	var fired bool
	var got float64
	dp.OnUpdate(func(_, next float64) {
		fired = true
		got = next
	})
	dp.WriteUnconfirmedValue(0.6, time.Now())
	if !fired {
		t.Fatal("WriteUnconfirmedValue must fire OnUpdate callbacks")
	}
	if got != 0.6 {
		t.Fatalf("callback got %v, want 0.6", got)
	}
}

// ─── modified_at / refreshed_at blend on DataPoint ───────────────────────────

// TestDataPointModifiedAtBlendsUnconfirmed verifies that DataPoint.ModifiedAt
// returns the unconfirmed modified timestamp when it is more recent
// than the confirmed one.
//
// We use WriteUnconfirmedValue with a known future timestamp to stage the
// unconfirmed modified time, then verify that ModifiedAt returns it.
// The confirmed modifiedAt is zero at this point (no OnEvent yet).
func TestDataPointModifiedAtBlendsUnconfirmed(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	future := time.Date(2026, 4, 28, 11, 0, 0, 0, time.UTC)
	// Write an unconfirmed value with a specific timestamp.
	dp.WriteUnconfirmedValue(0.5, future)

	// ModifiedAt must blend to the unconfirmed timestamp (confirmed is zero).
	if got := dp.ModifiedAt(); !got.Equal(future) {
		t.Fatalf("ModifiedAt()=%v want unconfirmed %v", got, future)
	}
}

// TestDataPointModifiedAtReturnsZeroAfterConfirmedEventClearsUnconfirmed
// verifies that after OnEvent (which resets the unconfirmed slot), ModifiedAt
// reflects the confirmed modifiedAt (confirmed takes over).
func TestDataPointModifiedAtReturnsConfirmedAfterReset(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	future := time.Date(2026, 4, 28, 11, 0, 0, 0, time.UTC)

	// Stage an unconfirmed write with a future timestamp.
	dp.WriteUnconfirmedValue(0.5, future)
	// Verify unconfirmed wins before confirmed event.
	if got := dp.ModifiedAt(); !got.Equal(future) {
		t.Fatalf("expected unconfirmed %v before OnEvent, got %v", future, got)
	}

	// A CCU-confirmed event clears the unconfirmed slot. modifiedAt is
	// now set to time.Now() which is >= future (future is in the past).
	before := time.Now()
	dp.OnEvent(0.5)
	after := time.Now()

	// ModifiedAt should now be the confirmed timestamp (≈ now, not future).
	got := dp.ModifiedAt()
	if got.Before(before) || got.After(after) {
		t.Fatalf("after OnEvent ModifiedAt()=%v want in range [%v, %v]", got, before, after)
	}
}

// TestWriteUnconfirmedValueModifiedAtBlended verifies that a call to
// WriteUnconfirmedValue with a new value stamps the unconfirmed modified
// timestamp and causes ModifiedAt() to blend to the new stamp.
func TestWriteUnconfirmedValueModifiedAtBlended(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	stamp := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	dp.WriteUnconfirmedValue(0.5, stamp)
	if got := dp.ModifiedAt(); !got.Equal(stamp) {
		t.Fatalf("ModifiedAt()=%v want %v", got, stamp)
	}
}

// ─── last_non_default_value ───────────────────────────────────────────────────

// TestLastNonDefaultValueTrackedByOnEvent verifies that OnEvent
// updates the lastNonDefaultValue slot when a CCU event arrives.
func TestLastNonDefaultValueTrackedByOnEvent(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	_, hasLast := dp.LastNonDefaultValue()
	if hasLast {
		t.Fatal("fresh DataPoint must not have a last non-default value")
	}

	dp.OnEvent(0.8)
	v, ok := dp.LastNonDefaultValue()
	if !ok || v != 0.8 {
		t.Fatalf("LastNonDefaultValue()=(%v, %v) want (0.8, true)", v, ok)
	}
}

// TestSetLastNonDefaultValueExplicit verifies that SetLastNonDefaultValue
// installs a value directly. Used by cache-restore paths.
func TestSetLastNonDefaultValueExplicit(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.SetLastNonDefaultValue(0.42)
	v, ok := dp.LastNonDefaultValue()
	if !ok || v != 0.42 {
		t.Fatalf("LastNonDefaultValue()=(%v, %v) want (0.42, true)", v, ok)
	}
}

// TestLastNonDefaultValueUpdatedOnMultipleEvents verifies that
// LastNonDefaultValue tracks the most recent non-default event value.
func TestLastNonDefaultValueUpdatedOnMultipleEvents(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.OnEvent(0.3)
	dp.OnEvent(0.7)
	v, ok := dp.LastNonDefaultValue()
	if !ok || v != 0.7 {
		t.Fatalf("after second event LastNonDefaultValue()=(%v, %v) want (0.7, true)", v, ok)
	}
}

// ─── status parameter auto-detection slots ───────────────────────────────────

// TestSetStatusParameterStores verifies that SetStatusParameter
// stores the parameter name and value list.
func TestSetStatusParameterStores(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	// Initially no status parameter is set.
	if name, set := dp.StatusParameter(); set || name != "" {
		t.Fatalf("fresh DP must have no status parameter: name=%q set=%v", name, set)
	}

	dp.SetStatusParameter("LEVEL_STATUS", []string{"NORMAL", "OVERFLOW"})

	name, set := dp.StatusParameter()
	if !set || name != "LEVEL_STATUS" {
		t.Fatalf("StatusParameter()=(%q, %v) want (LEVEL_STATUS, true)", name, set)
	}

	vl := dp.StatusValueList()
	if len(vl) != 2 || vl[0] != "NORMAL" || vl[1] != "OVERFLOW" {
		t.Fatalf("StatusValueList()=%v want [NORMAL OVERFLOW]", vl)
	}
}

// TestSetStatusParameterClearsWhenEmpty verifies that passing an empty
// string to SetStatusParameter removes the status parameter.
func TestSetStatusParameterClearsWhenEmpty(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.SetStatusParameter("LEVEL_STATUS", []string{"NORMAL"})
	dp.SetStatusParameter("", nil)

	_, set := dp.StatusParameter()
	if set {
		t.Fatal("after SetStatusParameter(\"\"), StatusParameter() must be (_, false)")
	}
	if dp.StatusValueList() != nil {
		t.Fatal("StatusValueList() must be nil after clearing status parameter")
	}
}

// TestUpdateStatusFromWireIntIndex verifies that UpdateStatusFromWire maps
// integer CCU-index values to ParameterStatus strings via the cached
// VALUE_LIST.
func TestUpdateStatusFromWireIntIndex(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.SetStatusParameter("LEVEL_STATUS", []string{"NORMAL", "OVERFLOW"})
	dp.UpdateStatusFromWire(int(1)) // index 1 → "OVERFLOW"

	status, ok := dp.Status()
	if !ok || status != hmenum.ParameterStatusOverflow {
		t.Fatalf("Status()=(%v, %v) want (OVERFLOW, true)", status, ok)
	}
}

// TestUpdateStatusFromWireStringValue verifies that UpdateStatusFromWire
// accepts string status values directly.
func TestUpdateStatusFromWireStringValue(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.UpdateStatusFromWire("NORMAL")

	status, ok := dp.Status()
	if !ok || status != hmenum.ParameterStatusNormal {
		t.Fatalf("Status()=(%v, %v) want (NORMAL, true)", status, ok)
	}
}

// TestUpdateStatusFromWireUnknownIntIgnored verifies that an out-of-range
// integer index is silently ignored and does not clobber the status.
func TestUpdateStatusFromWireUnknownIntIgnored(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	dp.SetStatusParameter("LEVEL_STATUS", []string{"NORMAL"})
	dp.UpdateStatusFromWire(int(99)) // out of range

	_, ok := dp.Status()
	if ok {
		t.Fatal("out-of-range integer index must not set a status")
	}
}

// ─── DetectStatusParameter at init ───────────────────────────────────────────

// TestDetectStatusParameterFoundWhenPresent verifies that
// DetectStatusParameter returns the status parameter name when it exists
// in the provided paramset key set.
func TestDetectStatusParameterFoundWhenPresent(t *testing.T) {
	t.Parallel()

	paramset := map[string]struct{}{
		"LEVEL":        {},
		"LEVEL_STATUS": {},
		"STATE":        {},
	}
	name, ok := DetectStatusParameter("LEVEL", paramset)
	if !ok || name != "LEVEL_STATUS" {
		t.Fatalf("DetectStatusParameter()=(%q, %v) want (LEVEL_STATUS, true)", name, ok)
	}
}

// TestDetectStatusParameterNotFoundWhenAbsent verifies that
// DetectStatusParameter returns ("", false) when no *_STATUS parameter
// exists in the paramset.
func TestDetectStatusParameterNotFoundWhenAbsent(t *testing.T) {
	t.Parallel()

	paramset := map[string]struct{}{
		"LEVEL": {},
		"STATE": {},
	}
	name, ok := DetectStatusParameter("LEVEL", paramset)
	if ok || name != "" {
		t.Fatalf("DetectStatusParameter()=(%q, %v) want (\"\", false)", name, ok)
	}
}

// TestDetectStatusParameterEmptyParamset verifies that an empty paramset
// returns ("", false) safely.
func TestDetectStatusParameterEmptyParamset(t *testing.T) {
	t.Parallel()

	name, ok := DetectStatusParameter("LEVEL", nil)
	if ok || name != "" {
		t.Fatalf("DetectStatusParameter()=(%q, %v) want (\"\", false)", name, ok)
	}
}

// ─── _enum_value_is_index cached at construction ─────────────────────────────

// TestEnumValueIsIndexTrueForIntMinEnum verifies that EnumValueIsIndex()
// returns true when the descriptor is an ENUM with a ValueList and an
// integer-typed MIN (HM device style).
func TestEnumValueIsIndexTrueForIntMinEnum(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterControlMode, hmenum.ParameterTypeEnum, hmenum.OperationsRead)
	cfg.Descriptor.ValueList = []string{"AUTO", "MANUAL", "BOOST"}
	cfg.Descriptor.Min = json.RawMessage(`0`)
	dp := NewDataPoint[int32](cfg)
	if !dp.EnumValueIsIndex() {
		t.Fatal("EnumValueIsIndex() must be true for ENUM with int-typed MIN")
	}
}

// TestEnumValueIsIndexFalseForStringMinEnum verifies that EnumValueIsIndex()
// returns false when MIN is a string (HmIP device style).
func TestEnumValueIsIndexFalseForStringMinEnum(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterControlMode, hmenum.ParameterTypeEnum, hmenum.OperationsRead)
	cfg.Descriptor.ValueList = []string{"AUTO", "MANUAL", "BOOST"}
	cfg.Descriptor.Min = json.RawMessage(`"AUTO"`)
	dp := NewDataPoint[string](cfg)
	if dp.EnumValueIsIndex() {
		t.Fatal("EnumValueIsIndex() must be false when MIN is a string")
	}
}

// TestEnumValueIsIndexFalseForNonEnum verifies that EnumValueIsIndex()
// returns false for non-ENUM parameters.
func TestEnumValueIsIndexFalseForNonEnum(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if dp.EnumValueIsIndex() {
		t.Fatal("EnumValueIsIndex() must be false for FLOAT parameter")
	}
}

// TestEnumValueIsIndexFalseForEnumWithoutValueList verifies that
// EnumValueIsIndex() returns false for an ENUM with no ValueList.
func TestEnumValueIsIndexFalseForEnumWithoutValueList(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterControlMode, hmenum.ParameterTypeEnum, hmenum.OperationsRead)
	// No ValueList, no Min.
	dp := NewDataPoint[int32](cfg)
	if dp.EnumValueIsIndex() {
		t.Fatal("EnumValueIsIndex() must be false when ValueList is absent")
	}
}

// ─── _ignore_on_initial_load slot cached at construction ─────────────────────

// TestIgnoreOnInitialLoadTrueForLowBat verifies that a LOWBAT parameter is
// marked as ignore-on-initial-load.
func TestIgnoreOnInitialLoadTrueForLowBat(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLowBat, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.Key.Parameter = string(hmenum.ParameterLowBat)
	dp := NewDataPoint[bool](cfg)
	if !dp.IgnoreOnInitialLoad() {
		t.Fatal("LOWBAT must be flagged as ignore-on-initial-load")
	}
}

// TestIgnoreOnInitialLoadFalseForLevel verifies that a LEVEL parameter
// is NOT marked as ignore-on-initial-load.
func TestIgnoreOnInitialLoadFalseForLevel(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if dp.IgnoreOnInitialLoad() {
		t.Fatal("LEVEL must not be flagged as ignore-on-initial-load")
	}
}

// TestIgnoreOnInitialLoadTrueForRSSIDevice verifies that an RSSI_DEVICE
// parameter is flagged as ignore-on-initial-load (RSSI_ prefix).
func TestIgnoreOnInitialLoadTrueForRSSIDevice(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterRSSIDevice, hmenum.ParameterTypeInteger, hmenum.OperationsRead)
	cfg.Key.Parameter = string(hmenum.ParameterRSSIDevice)
	dp := NewDataPoint[int32](cfg)
	if !dp.IgnoreOnInitialLoad() {
		t.Fatal("RSSI_DEVICE must be flagged as ignore-on-initial-load")
	}
}
