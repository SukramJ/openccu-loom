// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── Translation ─────────────────────────────────────────────────────────────

// TestTranslationEmpty verifies that a freshly constructed DataPoint returns
// an empty translation when none was set.
func TestTranslationEmpty(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if got := dp.Translation(); got != "" {
		t.Errorf("Translation() = %q, want empty string", got)
	}
}

// TestTranslationSet verifies that Translation() returns the value from Config.
func TestTranslationSet(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Translation = "Aktuelle Temperatur"
	dp := NewDataPoint[float64](cfg)
	if got := dp.Translation(); got != "Aktuelle Temperatur" {
		t.Errorf("Translation() = %q, want %q", got, "Aktuelle Temperatur")
	}
}

// ─── Description ─────────────────────────────────────────────────────────────

// TestDescriptionEmpty verifies that Description() returns empty when not set.
func TestDescriptionEmpty(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if got := dp.Description(); got != "" {
		t.Errorf("Description() = %q, want empty string", got)
	}
}

// TestDescriptionSet verifies that Description() returns the value from Config.
func TestDescriptionSet(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Description = "Current room temperature in degrees Celsius"
	dp := NewDataPoint[float64](cfg)
	if got := dp.Description(); got != cfg.Description {
		t.Errorf("Description() = %q, want %q", got, cfg.Description)
	}
}

// ─── RawUnit ─────────────────────────────────────────────────────────────────

// TestRawUnitReturnsDescriptorUnit verifies that RawUnit() returns the raw
// unit from the descriptor before any cleanup/normalisation.
func TestRawUnitReturnsDescriptorUnit(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Descriptor.Unit = "100%"
	dp := NewDataPoint[float64](cfg)
	if got := dp.RawUnit(); got != "100%" {
		t.Errorf("RawUnit() = %q, want %q", got, "100%")
	}
}

// TestRawUnitDiffersFromCleanedUnit verifies that RawUnit() may differ from
// Unit() when cleanup applies (demonstrating the parity intent).
func TestRawUnitDiffersFromCleanedUnit(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.Descriptor.Unit = "100%"
	dp := NewDataPoint[float64](cfg)

	raw := dp.RawUnit() // "100%"
	clean := dp.Unit()  // "%" after cleanup
	if raw == clean {
		t.Errorf("expected RawUnit() != Unit() for '100%%' input, both returned %q", raw)
	}
}

// ─── ValueTranslations ───────────────────────────────────────────────────────

// TestValueTranslationsNil verifies that ValueTranslations() returns nil when
// not set (non-ENUM parameter).
func TestValueTranslationsNil(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if got := dp.ValueTranslations(); got != nil {
		t.Errorf("ValueTranslations() = %v, want nil", got)
	}
}

// TestValueTranslationsSet verifies that ValueTranslations() returns the map
// from Config when set.
func TestValueTranslationsSet(t *testing.T) {
	t.Parallel()

	cfg := baseCfg("MODE", hmenum.ParameterTypeEnum, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Descriptor.ValueList = []string{"AUTO", "MANUAL", "BOOST"}
	cfg.ValueTranslations = map[string]string{
		"AUTO":   "Automatik",
		"MANUAL": "Manuell",
		"BOOST":  "Boost",
	}
	dp := NewDataPoint[string](cfg)
	got := dp.ValueTranslations()
	if len(got) != 3 {
		t.Fatalf("ValueTranslations() len = %d, want 3", len(got))
	}
	if got["AUTO"] != "Automatik" {
		t.Errorf("ValueTranslations()[AUTO] = %q, want %q", got["AUTO"], "Automatik")
	}
}

// ─── IsHmtype ────────────────────────────────────────────────────────────────

// TestIsHmtypeFalseByDefault verifies that a freshly constructed DataPoint
// is not an HM-type DP (IsHmtype = false by default).
func TestIsHmtypeFalseByDefault(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	if dp.IsHmtype() {
		t.Fatal("IsHmtype() must be false by default")
	}
}

// TestIsHmtypeTrue verifies that IsHmtype() returns true when set via Config.
func TestIsHmtypeTrue(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.IsHmtype = true
	dp := NewDataPoint[bool](cfg)
	if !dp.IsHmtype() {
		t.Fatal("IsHmtype() must be true when Spec.IsHmtype is set")
	}
}

// ─── Service ─────────────────────────────────────────────────────────────────

// TestServiceFlagFalseWhenNotSet verifies that Service() returns false when the
// SERVICE bit is absent in the descriptor FLAGS.
func TestServiceFlagFalseWhenNotSet(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	if dp.Service() {
		t.Fatal("Service() must be false when FLAGS.SERVICE is not set")
	}
}

// TestServiceFlagTrue verifies that Service() returns true when the SERVICE
// bit is set in the descriptor FLAGS.
func TestServiceFlagTrue(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.Descriptor.Flags = hmenum.FlagService
	dp := NewDataPoint[bool](cfg)
	if !dp.Service() {
		t.Fatal("Service() must be true when FLAGS.SERVICE is set")
	}
}

// ─── StatusDPK ───────────────────────────────────────────────────────────────

// TestStatusDPKNoneWhenNoStatusParameter verifies that StatusDPK() returns
// (zero, false) when no STATUS parameter has been set.
func TestStatusDPKNoneWhenNoStatusParameter(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	_, ok := dp.StatusDPK()
	if ok {
		t.Fatal("StatusDPK() ok must be false when no STATUS parameter is set")
	}
}

// TestStatusDPKReturnedWhenStatusParameterSet verifies that StatusDPK() returns
// the correct DPK after a STATUS parameter has been set.
func TestStatusDPKReturnedWhenStatusParameterSet(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	dp := NewDataPoint[float64](cfg)

	// Simulate what the pipeline does: set the status parameter.
	paramset := map[string]struct{}{
		"ACTUAL_TEMPERATURE_STATUS": {},
	}
	statusParam, found := DetectStatusParameter(cfg.Key.Parameter, paramset)
	if !found {
		t.Fatal("DetectStatusParameter should find ACTUAL_TEMPERATURE_STATUS")
	}
	dp.SetStatusParameter(statusParam, nil)

	dpk, ok := dp.StatusDPK()
	if !ok {
		t.Fatal("StatusDPK() ok must be true after SetStatusParameter")
	}
	if dpk.InterfaceID != cfg.Key.InterfaceID {
		t.Errorf("StatusDPK().InterfaceID = %q, want %q", dpk.InterfaceID, cfg.Key.InterfaceID)
	}
	if dpk.ChannelAddress != cfg.Key.ChannelAddress {
		t.Errorf("StatusDPK().ChannelAddress = %q, want %q", dpk.ChannelAddress, cfg.Key.ChannelAddress)
	}
	if dpk.Parameter != statusParam {
		t.Errorf("StatusDPK().Parameter = %q, want %q", dpk.Parameter, statusParam)
	}
}

// ─── Signature ───────────────────────────────────────────────────────────────

// TestSignatureFormat verifies the `<category>/<model>/<param>` format.
func TestSignatureFormat(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterActualTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.DeviceModel = "HmIP-eTRV"
	cfg.Kind = KindSensor
	dp := NewDataPoint[float64](cfg)

	got := dp.Signature()
	// Category for a sensor kind is "sensor"; DeviceModel and param follow.
	want := "sensor/HmIP-eTRV/ACTUAL_TEMPERATURE"
	if got != want {
		t.Errorf("Signature() = %q, want %q", got, want)
	}
}

// TestSignatureEmptyModel verifies that Signature() produces a well-formed
// string even when DeviceModel is empty.
func TestSignatureEmptyModel(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.Kind = KindBinarySensor
	dp := NewDataPoint[bool](cfg)

	got := dp.Signature()
	want := "binary_sensor//STATE"
	if got != want {
		t.Errorf("Signature() = %q, want %q", got, want)
	}
}

// ─── EnabledByChannelOperationMode ───────────────────────────────────────────

// TestEnabledByChannelOperationModeNotSet verifies the nil (no constraint)
// tri-state: (false, false).
func TestEnabledByChannelOperationModeNotSet(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	enabled, ok := dp.EnabledByChannelOperationMode()
	if ok {
		t.Fatalf("EnabledByChannelOperationMode() ok = true, want false (not set); enabled=%v", enabled)
	}
}

// TestEnabledByChannelOperationModeTrue verifies the "included" tri-state:
// (true, true).
func TestEnabledByChannelOperationModeTrue(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	dp.SetOperationModeAllowed(true)

	enabled, ok := dp.EnabledByChannelOperationMode()
	if !ok {
		t.Fatal("EnabledByChannelOperationMode() ok = false after SetOperationModeAllowed(true)")
	}
	if !enabled {
		t.Fatal("EnabledByChannelOperationMode() enabled = false, want true")
	}
}

// TestEnabledByChannelOperationModeFalse verifies the "excluded" tri-state:
// (false, true).
func TestEnabledByChannelOperationModeFalse(t *testing.T) {
	t.Parallel()

	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	dp.SetOperationModeAllowed(false)

	enabled, ok := dp.EnabledByChannelOperationMode()
	if !ok {
		t.Fatal("EnabledByChannelOperationMode() ok = false after SetOperationModeAllowed(false)")
	}
	if enabled {
		t.Fatal("EnabledByChannelOperationMode() enabled = true, want false")
	}
}

// ─── Device / Channel — IseID ────────────────────────────────────────────────
// (Device and Channel IseID tests live in device package; here we just verify
// the Config fields compile and flow through to the struct correctly.)
