// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ---------------------------------------------------------------------------
// IsRefreshed / SubDataPointKeys
// ---------------------------------------------------------------------------

// TestClimateIsRefreshed also pins the availability gate to its primary
// state carrier (ACTUAL_TEMPERATURE); see notes/parity/by_design.md.
func TestClimateIsRefreshed(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	// Before any wire event the aggregate is not refreshed.
	if r.climate.IsRefreshed() {
		t.Error("IsRefreshed() = true before any observation, want false")
	}
	// Drive actual temperature — the aggregate references this DP.
	r.actualTemperature.OnEvent(20.0)
	if !r.climate.IsRefreshed() {
		t.Error("IsRefreshed() = false after observation, want true")
	}
}

func TestClimateSubDataPointKeys(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	keys := r.climate.SubDataPointKeys()
	if len(keys) == 0 {
		t.Error("SubDataPointKeys() returned empty slice, want ≥1 key")
	}
}

// ---------------------------------------------------------------------------
// CurrentTemperature / Setpoint nil-guard paths
// ---------------------------------------------------------------------------

func TestClimateCurrentTemperatureNilGuard(t *testing.T) {
	c := &Climate{}
	v, ok := c.CurrentTemperature()
	if ok || v != 0 {
		t.Errorf("nil guard: got (%v, %v), want (0, false)", v, ok)
	}
}

func TestClimateSetpointNilGuard(t *testing.T) {
	c := &Climate{}
	v, ok := c.Setpoint()
	if ok || v != 0 {
		t.Errorf("nil guard: got (%v, %v), want (0, false)", v, ok)
	}
}

// ---------------------------------------------------------------------------
// Modes – only SupportsAuto / Heat / Cool / Off subsets
// ---------------------------------------------------------------------------

func TestClimateModesNoCapabilities(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if got := r.climate.Modes(); len(got) != 0 {
		t.Errorf("Modes() with no capability = %v, want empty", got)
	}
}

func TestClimateModesAllCapabilities(t *testing.T) {
	caps := custom.ClimateCapabilities{
		SupportsAuto: true,
		SupportsHeat: true,
		SupportsCool: true,
		SupportsOff:  true,
	}
	r := newRig(t, "x", KindIP, &stubWriter{}, caps)
	got := r.climate.Modes()
	if len(got) != 4 {
		t.Errorf("Modes() = %v, want 4 modes", got)
	}
}

// ---------------------------------------------------------------------------
// Profiles – mode-aware (AUTO vs. MANU)
// ---------------------------------------------------------------------------

func TestClimateProfilesBoostCapabilityInManuMode(t *testing.T) {
	caps := custom.ClimateCapabilities{SupportsProfile: true, SupportsBoost: true}
	r := newRig(t, "x", KindIP, &stubWriter{}, caps)
	r.climate.OnMode(ModeHeat)
	profiles := r.climate.Profiles()
	found := false
	for _, p := range profiles {
		if p == ProfileBoost {
			found = true
		}
	}
	if !found {
		t.Error("Profiles() should contain ProfileBoost when SupportsBoost=true")
	}
}

func TestClimateProfilesComfortEco(t *testing.T) {
	caps := custom.ClimateCapabilities{
		SupportsProfile: true,
		SupportsComfort: true,
		SupportsEco:     true,
		SupportsAuto:    true,
	}
	r := newRig(t, "x", KindRF, &stubWriter{}, caps)
	r.climate.OnMode(ModeAuto)
	profiles := r.climate.Profiles()
	hasComfort, hasEco := false, false
	for _, p := range profiles {
		if p == ProfileComfort {
			hasComfort = true
		}
		if p == ProfileEco {
			hasEco = true
		}
	}
	if !hasComfort {
		t.Error("Profiles() missing ProfileComfort when SupportsComfort=true")
	}
	if !hasEco {
		t.Error("Profiles() missing ProfileEco when SupportsEco=true")
	}
}

// ---------------------------------------------------------------------------
// SetTemperatureOffset
// ---------------------------------------------------------------------------

func TestClimateSetTemperatureOffset(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{})
	if err := r.climate.SetTemperatureOffset(context.Background(), "0.5", hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperatureOffset: %v", err)
	}
	v, ok := r.climate.TemperatureOffset()
	if !ok || v != "0.5" {
		t.Errorf("TemperatureOffset() = (%v, %v), want (\"0.5\", true)", v, ok)
	}
	if got := w.last(); got.param != hmenum.ParameterTemperatureOffset {
		t.Errorf("SetTemperatureOffset wrote param %v, want TEMPERATURE_OFFSET", got.param)
	}
}

func TestClimateSetTemperatureOffsetNoWriter(t *testing.T) {
	c := &Climate{}
	if err := c.SetTemperatureOffset(context.Background(), "1.0", hmenum.CommandPriorityLow); err == nil {
		t.Error("SetTemperatureOffset without writer should return error")
	}
}

// ---------------------------------------------------------------------------
// OnActiveProfile
// ---------------------------------------------------------------------------

func TestOnActiveProfileBase0(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnMode(ModeAuto)
	// RF wire delivers 0-based: index 0 → ProfileWeekProgram1.
	r.climate.OnActiveProfile(0, true)
	p, ok := r.climate.Profile()
	if !ok || p != ProfileWeekProgram1 {
		t.Errorf("Profile() = (%v, %v), want (week_program_1, true)", p, ok)
	}
}

func TestOnActiveProfileBase1(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnMode(ModeAuto)
	// IP wire delivers 1-based: index 3 → ProfileWeekProgram3.
	r.climate.OnActiveProfile(3, false)
	p, ok := r.climate.Profile()
	if !ok || p != ProfileWeekProgram3 {
		t.Errorf("Profile() = (%v, %v), want (week_program_3, true)", p, ok)
	}
}

func TestOnActiveProfileDoesNotOverrideBoost(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnMode(ModeAuto)
	r.climate.OnProfile(ProfileBoost)
	r.climate.OnActiveProfile(2, false)
	p, _ := r.climate.Profile()
	if p != ProfileBoost {
		t.Errorf("Profile() = %v, want ProfileBoost (no override when boost active)", p)
	}
}

func TestOnActiveProfileDoesNotOverrideAway(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnMode(ModeAuto)
	r.climate.OnProfile(ProfileAway)
	r.climate.OnActiveProfile(1, false)
	p, _ := r.climate.Profile()
	if p != ProfileAway {
		t.Errorf("Profile() = %v, want ProfileAway (no override when away active)", p)
	}
}

func TestOnActiveProfileNotAutoMode(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnMode(ModeHeat)
	r.climate.OnActiveProfile(2, false)
	// Mode is HEAT, not AUTO — profile must not be updated.
	_, ok := r.climate.Profile()
	// The profile has not been set at all in this test (no prior OnProfile call),
	// so ok should remain false.
	if ok {
		p, _ := r.climate.Profile()
		if p == ProfileWeekProgram2 {
			t.Error("OnActiveProfile should not set profile when mode != AUTO")
		}
	}
}

// ---------------------------------------------------------------------------
// OnControlMode (RF CONTROL_MODE ingestion)
// ---------------------------------------------------------------------------

func TestOnControlModeAutoMode(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnControlMode("AUTO-MODE")
	m, ok := r.climate.Mode()
	if !ok || m != ModeAuto {
		t.Errorf("Mode() = (%v, %v), want (auto, true)", m, ok)
	}
}

func TestOnControlModeManuMode(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnControlMode("MANU-MODE")
	m, ok := r.climate.Mode()
	if !ok || m != ModeHeat {
		t.Errorf("Mode() = (%v, %v), want (heat, true)", m, ok)
	}
	p, ok := r.climate.Profile()
	if !ok || p != ProfileNone {
		t.Errorf("Profile() = (%v, %v), want (none, true)", p, ok)
	}
}

func TestOnControlModePartyMode(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnControlMode("PARTY-MODE")
	m, _ := r.climate.Mode()
	p, _ := r.climate.Profile()
	if m != ModeAuto || p != ProfileAway {
		t.Errorf("PARTY-MODE: mode=%v profile=%v, want auto/away", m, p)
	}
}

func TestOnControlModeBoostMode(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnControlMode("BOOST-MODE")
	m, _ := r.climate.Mode()
	p, _ := r.climate.Profile()
	if m != ModeAuto || p != ProfileBoost {
		t.Errorf("BOOST-MODE: mode=%v profile=%v, want auto/boost", m, p)
	}
}

func TestOnControlModeIntegerInput(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnControlMode(int(0)) // "AUTO-MODE"
	m, ok := r.climate.Mode()
	if !ok || m != ModeAuto {
		t.Errorf("int(0) → Mode() = (%v, %v), want (auto, true)", m, ok)
	}
}

func TestOnControlModeInt32Input(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnControlMode(int32(1)) // "MANU-MODE"
	m, ok := r.climate.Mode()
	if !ok || m != ModeHeat {
		t.Errorf("int32(1) → Mode() = (%v, %v), want (heat, true)", m, ok)
	}
}

func TestOnControlModeInt64Input(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnControlMode(int64(2)) // "PARTY-MODE"
	p, _ := r.climate.Profile()
	if p != ProfileAway {
		t.Errorf("int64(2) → Profile() = %v, want away", p)
	}
}

func TestOnControlModeFloat64Input(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnControlMode(float64(3)) // "BOOST-MODE"
	p, _ := r.climate.Profile()
	if p != ProfileBoost {
		t.Errorf("float64(3) → Profile() = %v, want boost", p)
	}
}

func TestOnControlModeUnknownTypeIgnored(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	// Unknown type must be silently ignored (no panic).
	r.climate.OnControlMode([]byte("invalid"))
	_, ok := r.climate.Mode()
	if ok {
		t.Error("Mode() should not be set after unknown type input")
	}
}

func TestOnControlModeAutoRecoveryWithCachedProfile(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	// Pre-load a cached active profile index (2 → week_program_2).
	r.climate.OnActiveProfile(1, true) // base0=true → wpIdx=2
	// Inject AUTO-MODE — should recover ProfileWeekProgram2 from cache.
	r.climate.OnControlMode("AUTO-MODE")
	p, ok := r.climate.Profile()
	if !ok || p != ProfileWeekProgram2 {
		t.Errorf("Auto recovery: Profile() = (%v, %v), want (week_program_2, true)", p, ok)
	}
}

// ---------------------------------------------------------------------------
// OnSetPointMode (IP SET_POINT_MODE ingestion)
// ---------------------------------------------------------------------------

func TestOnSetPointModeAuto(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnSetPointMode(int32(0))
	m, ok := r.climate.Mode()
	if !ok || m != ModeAuto {
		t.Errorf("SET_POINT_MODE=0 → Mode() = (%v, %v), want (auto, true)", m, ok)
	}
}

func TestOnSetPointModeManu(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnSetPointMode(int32(1))
	m, ok := r.climate.Mode()
	if !ok || m != ModeHeat {
		t.Errorf("SET_POINT_MODE=1 → Mode() = (%v, %v), want (heat, true)", m, ok)
	}
}

func TestOnSetPointModeManuCooling(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnHeatingCooling("COOLING")
	r.climate.OnSetPointMode(int32(1))
	m, _ := r.climate.Mode()
	if m != ModeCool {
		t.Errorf("SET_POINT_MODE=1 with COOLING → Mode() = %v, want cool", m)
	}
}

func TestOnSetPointModeAway(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnSetPointMode(int32(2))
	m, _ := r.climate.Mode()
	p, _ := r.climate.Profile()
	if m != ModeAuto || p != ProfileAway {
		t.Errorf("SET_POINT_MODE=2 → mode=%v profile=%v, want auto/away", m, p)
	}
}

func TestOnSetPointModeAutoRecoveryWithCachedProfile(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	// Pre-load cached ACTIVE_PROFILE = 3 (1-based → ProfileWeekProgram3).
	r.climate.OnActiveProfile(3, false)
	r.climate.OnSetPointMode(int32(0))
	p, ok := r.climate.Profile()
	if !ok || p != ProfileWeekProgram3 {
		t.Errorf("AUTO recovery: Profile() = (%v, %v), want (week_program_3, true)", p, ok)
	}
}

func TestOnSetPointModeUnknownTypeIgnored(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnSetPointMode("not-an-int")
	_, ok := r.climate.Mode()
	if ok {
		t.Error("OnSetPointMode with string input should be ignored")
	}
}

// ---------------------------------------------------------------------------
// toInt helper (via OnSetPointMode)
// ---------------------------------------------------------------------------

func TestToIntVariants(t *testing.T) {
	cases := []struct {
		name  string
		input any
		wantM Mode
	}{
		{"int", int(0), ModeAuto},
		{"int32", int32(0), ModeAuto},
		{"int64", int64(0), ModeAuto},
		{"float64", float64(0), ModeAuto},
		{"float32", float32(0), ModeAuto},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
			r.climate.OnSetPointMode(tc.input)
			m, ok := r.climate.Mode()
			if !ok || m != tc.wantM {
				t.Errorf("input=%T(%v): Mode()=(%v,%v), want (%v,true)", tc.input, tc.input, m, ok, tc.wantM)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DisableBoost
// ---------------------------------------------------------------------------

func TestClimateDisableBoost(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsBoost: true})
	if err := r.climate.DisableBoost(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("DisableBoost: %v", err)
	}
	if got := w.last(); got.param != hmenum.ParameterBoostMode || got.value != false {
		t.Errorf("DisableBoost wrote %+v, want BOOST_MODE=false", got)
	}
}

// ---------------------------------------------------------------------------
// SetAway RF path + SetAwayForDuration
// ---------------------------------------------------------------------------

func TestClimateSetAwayRF(t *testing.T) {
	w := &putWriter{}
	r := newRig(t, "x", KindRF, w, custom.ClimateCapabilities{SupportsAway: true})
	end := time.Now().Add(2 * time.Hour)
	if err := r.climate.SetAway(context.Background(), end, 15.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetAway RF: %v", err)
	}
	p, ok := r.climate.Profile()
	if !ok || p != ProfileAway {
		t.Errorf("Profile() = (%v, %v), want (away, true)", p, ok)
	}
	until, ok := r.climate.AwayUntil()
	if !ok || until.IsZero() {
		t.Error("AwayUntil() should be set after SetAway")
	}
}

func TestClimateSetAwayUnsupportedKind(t *testing.T) {
	r := newRig(t, "x", KindSimpleRF, &stubWriter{}, custom.ClimateCapabilities{SupportsAway: true})
	if err := r.climate.SetAway(context.Background(), time.Now().Add(time.Hour), 15.0, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetAway on SimpleRF should return ErrModeNotSupported")
	}
}

func TestClimateSetAwayNoCapability(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if err := r.climate.SetAway(context.Background(), time.Now().Add(time.Hour), 15.0, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetAway without SupportsAway should return ErrModeNotSupported")
	}
}

func TestClimateSetAwayForDuration(t *testing.T) {
	w := &putWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsAway: true, MinTemperature: 4.5, MaxTemperature: 30.5})
	if err := r.climate.SetAwayForDuration(context.Background(), 3*time.Hour, 17.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetAwayForDuration: %v", err)
	}
	p, ok := r.climate.Profile()
	if !ok || p != ProfileAway {
		t.Errorf("Profile() = (%v, %v), want (away, true)", p, ok)
	}
}

// ---------------------------------------------------------------------------
// DisableAway
// ---------------------------------------------------------------------------

func TestClimateDisableAwayIP(t *testing.T) {
	w := &putWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsAway: true})
	if err := r.climate.DisableAway(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("DisableAway IP: %v", err)
	}
	p, ok := r.climate.Profile()
	if !ok || p != ProfileNone {
		t.Errorf("Profile() = (%v, %v), want (none, true)", p, ok)
	}
}

// TestClimateDisableAwayRF pins that leaving away/party mode on a classic
// RF thermostat submits a past-window party code — mirroring
// disable_away_mode's RF override (climate.py:555-565), which cancels an
// active party by submitting a code whose window already ended, not by
// writing PARTY_MODE_SUBMIT="" (the DP's power-on default, which encodes
// no window at all and leaves the firmware's active party running).
func TestClimateDisableAwayRF(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindRF, w, custom.ClimateCapabilities{SupportsAway: true})
	if err := r.climate.DisableAway(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("DisableAway RF: %v", err)
	}
	got := w.last()
	if got.param != hmenum.ParameterPartyModeSubmit {
		t.Errorf("DisableAway RF wrote param=%v, want PARTY_MODE_SUBMIT", got.param)
	}
	code, ok := got.value.(string)
	if !ok || code == "" {
		t.Fatalf("DisableAway RF wrote PARTY_MODE_SUBMIT=%q, want a non-empty past-window code", got.value)
	}
	parts := strings.Split(code, ",")
	if len(parts) != 9 {
		t.Fatalf("PARTY_MODE_SUBMIT=%q: expected 9 comma-separated fields, got %d", code, len(parts))
	}
	if parts[0] != fmt.Sprintf("%.1f", 12.0) {
		t.Errorf("PARTY_MODE_SUBMIT temperature field=%q, want %q", parts[0], fmt.Sprintf("%.1f", 12.0))
	}
}

func TestClimateDisableAwayNoCapability(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if err := r.climate.DisableAway(context.Background(), hmenum.CommandPriorityLow); err == nil {
		t.Error("DisableAway without SupportsAway should return error")
	}
}

func TestClimateDisableAwaySimpleRF(t *testing.T) {
	r := newRig(t, "x", KindSimpleRF, &stubWriter{}, custom.ClimateCapabilities{SupportsAway: true})
	if err := r.climate.DisableAway(context.Background(), hmenum.CommandPriorityLow); err == nil {
		t.Error("DisableAway on SimpleRF should return ErrModeNotSupported")
	}
}

// ---------------------------------------------------------------------------
// AwayUntil
// ---------------------------------------------------------------------------

func TestClimateAwayUntilNotSet(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	_, ok := r.climate.AwayUntil()
	if ok {
		t.Error("AwayUntil() should return false when no away period is active")
	}
}

// ---------------------------------------------------------------------------
// IsHeating
// ---------------------------------------------------------------------------

func TestClimateIsHeatingDefault(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if !r.climate.IsHeating() {
		t.Error("IsHeating() should default to true before HEATING_COOLING is observed")
	}
}

func TestClimateIsHeatingCooling(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnHeatingCooling("COOLING")
	if r.climate.IsHeating() {
		t.Error("IsHeating() should return false when COOLING is active")
	}
}

// ---------------------------------------------------------------------------
// MinMaxValueNotRelevantForManuMode
// ---------------------------------------------------------------------------

func TestClimateMinMaxNotRelevantDefault(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if r.climate.MinMaxValueNotRelevantForManuMode() {
		t.Error("MinMaxValueNotRelevantForManuMode() should default to false")
	}
}

func TestClimateOnMinMaxNotRelevant(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnMinMaxValueNotRelevantForManuMode(true)
	if !r.climate.MinMaxValueNotRelevantForManuMode() {
		t.Error("MinMaxValueNotRelevantForManuMode() should be true after OnMinMaxValueNotRelevantForManuMode(true)")
	}
}

// ---------------------------------------------------------------------------
// setIPMode — ModeHeat / ModeCool path
// ---------------------------------------------------------------------------

func TestClimateSetModeIPHeat(t *testing.T) {
	w := &putWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{MinTemperature: 5.0, MaxTemperature: 30.0})
	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetMode HEAT: %v", err)
	}
	// ModeHeat on IP: PutParamset with CONTROL_MODE=1 + SET_POINT_TEMPERATURE.
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	if w.puts[0][string(hmenum.ParameterControlMode)].(int32) != 1 {
		t.Errorf("CONTROL_MODE=%v, want 1", w.puts[0][string(hmenum.ParameterControlMode)])
	}
}

func TestClimateSetModeIPUnknownMode(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if err := r.climate.SetMode(context.Background(), Mode("unknown"), hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetMode with unknown mode should return ErrModeNotSupported")
	}
}

func TestClimateSetModeSimpleRFHeat(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindSimpleRF, w, custom.ClimateCapabilities{MinTemperature: 6.0, MaxTemperature: 30.0})
	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SimpleRF SetMode HEAT: %v", err)
	}
	// SimpleRF HEAT: SetTemperature(MaxTemperature).
	if got := w.last(); got.param != hmenum.ParameterSetTemperature {
		t.Errorf("SimpleRF HEAT wrote param=%v, want SET_TEMPERATURE", got.param)
	}
}

func TestClimateSetModeSimpleRFOff(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindSimpleRF, w, custom.ClimateCapabilities{MinTemperature: 6.0, MaxTemperature: 30.0})
	if err := r.climate.SetMode(context.Background(), ModeOff, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SimpleRF SetMode OFF: %v", err)
	}
	// SimpleRF OFF: SetTemperature(MinTemperature).
	if got := w.last(); got.param != hmenum.ParameterSetTemperature {
		t.Errorf("SimpleRF OFF wrote param=%v, want SET_TEMPERATURE", got.param)
	}
}

func TestClimateSetModeSimpleRFAutoUnsupported(t *testing.T) {
	r := newRig(t, "x", KindSimpleRF, &stubWriter{}, custom.ClimateCapabilities{})
	if err := r.climate.SetMode(context.Background(), ModeAuto, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SimpleRF AUTO should return ErrModeNotSupported")
	}
}

func TestClimateSetModeRFAuto(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindRF, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
	if err := r.climate.SetMode(context.Background(), ModeAuto, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("RF SetMode AUTO: %v", err)
	}
	if got := w.last(); got.param != hmenum.ParameterAutoMode || got.value != true {
		t.Errorf("RF AUTO wrote %+v, want AUTO_MODE=true", got)
	}
}

func TestClimateSetModeRFUnknown(t *testing.T) {
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	if err := r.climate.SetMode(context.Background(), Mode("unsupported"), hmenum.CommandPriorityHigh); err == nil {
		t.Error("RF unknown mode should return ErrModeNotSupported")
	}
}

func TestClimateSetModeUnknownKind(t *testing.T) {
	// Kind(99) is not a valid Kind constant — falls through to the default case.
	r := newRig(t, "x", Kind(99), &stubWriter{}, custom.ClimateCapabilities{})
	if err := r.climate.SetMode(context.Background(), ModeAuto, hmenum.CommandPriorityHigh); err == nil {
		t.Error("Unknown Kind should return ErrModeNotSupported")
	}
}

// ---------------------------------------------------------------------------
// SetProfile edge cases
// ---------------------------------------------------------------------------

func TestClimateSetProfileSimpleRFMapsToWeekProgramPointer(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindSimpleRF, w, custom.ClimateCapabilities{SupportsProfile: true})
	if err := r.climate.SetProfile(context.Background(), ProfileWeekProgram2, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetProfile SimpleRF: %v", err)
	}
	if got := w.last(); got.param != hmenum.ParameterWeekProgramPointer {
		t.Errorf("SimpleRF SetProfile param=%v, want WEEK_PROGRAM_POINTER", got.param)
	}
}

func TestClimateSetProfileNoCapability(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if err := r.climate.SetProfile(context.Background(), ProfileWeekProgram1, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetProfile without SupportsProfile should return ErrModeNotSupported")
	}
}

// ---------------------------------------------------------------------------
// IsStateChange
// ---------------------------------------------------------------------------

func TestIsStateChangeTemperature(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.setpoint.OnEvent(21.0)
	same := 21.0
	diff := 22.0
	if r.climate.IsStateChange(&same, nil, nil) {
		t.Error("IsStateChange with same temperature should return false")
	}
	if !r.climate.IsStateChange(&diff, nil, nil) {
		t.Error("IsStateChange with different temperature should return true")
	}
}

func TestIsStateChangeMode(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnMode(ModeAuto)
	auto := ModeAuto
	heat := ModeHeat
	if r.climate.IsStateChange(nil, &auto, nil) {
		t.Error("IsStateChange with same mode should return false")
	}
	if !r.climate.IsStateChange(nil, &heat, nil) {
		t.Error("IsStateChange with different mode should return true")
	}
}

func TestIsStateChangeProfile(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnProfile(ProfileWeekProgram1)
	p1 := ProfileWeekProgram1
	p2 := ProfileWeekProgram2
	if r.climate.IsStateChange(nil, nil, &p1) {
		t.Error("IsStateChange with same profile should return false")
	}
	if !r.climate.IsStateChange(nil, nil, &p2) {
		t.Error("IsStateChange with different profile should return true")
	}
}

func TestIsStateChangeUnobservedAlwaysTrue(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	auto := ModeAuto
	if !r.climate.IsStateChange(nil, &auto, nil) {
		t.Error("IsStateChange should return true when mode not yet observed")
	}
}

// ---------------------------------------------------------------------------
// RefreshLinkPeerActivitySources
// ---------------------------------------------------------------------------

func TestClimateRefreshLinkPeerActivitySourcesNilPeers(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	cancel := r.climate.RefreshLinkPeerActivitySources(nil)
	if cancel == nil {
		t.Error("RefreshLinkPeerActivitySources(nil) returned nil cancel func")
	}
	cancel() // must not panic
}

func TestClimateRefreshLinkPeerActivitySourcesNilChannel(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	var nilCh *device.Channel
	cancel := r.climate.RefreshLinkPeerActivitySources([]*device.Channel{nilCh})
	cancel() // must not panic
}

// ---------------------------------------------------------------------------
// payload.go – InfoPayload / kindName / SubDataPointKeysAsStrings
// ---------------------------------------------------------------------------

func TestClimateInfoPayloadNilSafe(t *testing.T) {
	var c *Climate
	if got := c.Info(); got != nil {
		t.Errorf("InfoPayload on nil = %v, want nil", got)
	}
}

func TestClimateInfoPayloadKeys(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	info, ok := r.climate.Info().(*payload.ClimateInfo)
	if !ok || info == nil {
		t.Fatal("InfoPayload did not return *payload.ClimateInfo")
	}
	if info.Address == "" {
		t.Error("InfoPayload missing address")
	}
	if info.Key == "" {
		t.Error("InfoPayload missing key")
	}
	if info.Kind == "" {
		t.Error("InfoPayload missing kind")
	}
	if info.Category == "" {
		t.Error("InfoPayload missing category")
	}
	if len(info.SubDPKeys) == 0 {
		t.Error("InfoPayload missing sub_dp_keys")
	}
	if info.Kind != "ip" {
		t.Errorf("InfoPayload kind=%v, want ip", info.Kind)
	}
}

func TestClimateInfoPayloadKindNames(t *testing.T) {
	cases := []struct {
		kind Kind
		want string
	}{
		{KindIP, "ip"},
		{KindRF, "rf"},
		{KindSimpleRF, "simple_rf"},
		{Kind(99), "unknown"},
	}
	for _, tc := range cases {
		if got := kindName(tc.kind); got != tc.want {
			t.Errorf("kindName(%d) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestClimateSubDataPointKeysAsStrings(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	ss := r.climate.SubDataPointKeysAsStrings()
	if len(ss) == 0 {
		t.Error("SubDataPointKeysAsStrings() returned empty slice")
	}
	for _, s := range ss {
		if s == "" {
			t.Error("SubDataPointKeysAsStrings() returned empty string entry")
		}
	}
}

// ---------------------------------------------------------------------------
// payload.go – ConfigPayload nil-safe
// ---------------------------------------------------------------------------

func TestClimateConfigPayloadNilSafe(t *testing.T) {
	var c *Climate
	if got := c.Config(); got != nil {
		t.Errorf("ConfigPayload on nil = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// payload.go – StatePayload nil-safe + basic fields
// ---------------------------------------------------------------------------

func TestClimateStatePayloadNilSafe(t *testing.T) {
	var c *Climate
	if got := c.State(); got != nil {
		t.Errorf("StatePayload on nil = %v, want nil", got)
	}
}

func TestClimateStatePayloadKeys(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	sp, ok := r.climate.State().(*payload.ClimateState)
	if !ok || sp == nil {
		t.Fatal("StatePayload did not return *payload.ClimateState")
	}
	if sp.HVACMode == "" {
		t.Error("StatePayload missing hvac_mode")
	}
	if sp.PresetMode == "" {
		t.Error("StatePayload missing preset_mode")
	}
	if sp.Action == "" {
		t.Error("StatePayload missing action")
	}
}

func TestClimateStatePayloadOffAction(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnMode(ModeOff)
	sp, _ := r.climate.State().(*payload.ClimateState)
	if sp == nil || sp.Action != string(ActivityOff) {
		t.Errorf("StatePayload action=%v, want %v when mode=off", sp.Action, ActivityOff)
	}
}

func TestClimateStatePayloadValueStateUncertain(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	sp, _ := r.climate.State().(*payload.ClimateState)
	if sp == nil || sp.StateUncertain {
		t.Errorf("StatePayload state_uncertain=%v, want false", sp.StateUncertain)
	}
}

// ---------------------------------------------------------------------------
// topology.go – HAComponent / TopicSlot / DiscoveryTriggers
// ---------------------------------------------------------------------------

func TestClimateHAComponent(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if got := r.climate.HAComponent(); got != "climate" {
		t.Errorf("HAComponent() = %q, want \"climate\"", got)
	}
}

func TestClimateTopicSlot(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:3", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	slot := r.climate.TopicSlot()
	if slot.Address == "" {
		t.Error("TopicSlot.Address is empty")
	}
	if slot.Parameter != "climate" {
		t.Errorf("TopicSlot.Parameter = %q, want \"climate\"", slot.Parameter)
	}
}

func TestClimateTopicSlotBareAddress(t *testing.T) {
	// When the address has no colon channel suffix TopicSlot must not panic.
	r := newRig(t, "BAREADDR", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	slot := r.climate.TopicSlot()
	if slot.Parameter != "climate" {
		t.Errorf("TopicSlot.Parameter = %q, want \"climate\" (bare address)", slot.Parameter)
	}
}

func TestClimateDiscoveryTriggers(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	triggers := r.climate.DiscoveryTriggers()
	if len(triggers) == 0 {
		t.Error("DiscoveryTriggers() returned empty slice")
	}
}

// ---------------------------------------------------------------------------
// matter.go – MatterWrite / MatterReportable / MatterAttributes on leaf servers
// ---------------------------------------------------------------------------

func TestClimateTempMeasServerMatterWrite(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0402)
	err := srv.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite on TemperatureMeasurement should always return error")
	}
}

func TestClimateTempMeasServerMatterReportable(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0402)
	if got := srv.MatterReportable(); len(got) == 0 {
		t.Error("MatterReportable() returned empty slice for TemperatureMeasurement")
	}
}

func TestClimateTempMeasServerMatterAttributes(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0402)
	lister, ok := srv.(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("TemperatureMeasurement server does not implement MatterClusterAttributeLister")
	}
	if got := lister.MatterAttributes(); len(got) == 0 {
		t.Error("MatterAttributes() returned empty slice for TemperatureMeasurement")
	}
}

func TestClimateHumidityServerMatterWrite(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0405)
	err := srv.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite on RelativeHumidityMeasurement should always return error")
	}
}

func TestClimateHumidityServerMatterReportable(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0405)
	if got := srv.MatterReportable(); len(got) == 0 {
		t.Error("MatterReportable() returned empty slice for RelativeHumidityMeasurement")
	}
}

func TestClimateHumidityServerMatterAttributes(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0405)
	lister, ok := srv.(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("RelativeHumidityMeasurement server does not implement MatterClusterAttributeLister")
	}
	if got := lister.MatterAttributes(); len(got) == 0 {
		t.Error("MatterAttributes() returned empty slice for RelativeHumidityMeasurement")
	}
}

func TestClimateTempMeasServerReadUnknownAttr(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0402)
	_, ok := srv.MatterRead(0xFFFF)
	if ok {
		t.Error("MatterRead of unknown attr should return ok=false")
	}
}

func TestClimateHumidityServerReadUnknownAttr(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0405)
	_, ok := srv.MatterRead(0xFFFF)
	if ok {
		t.Error("MatterRead of unknown attr should return ok=false on humidity server")
	}
}

func TestClimateHumidityServerReadUnobserved(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0405)
	// No humidity observation yet — should return (nil, true) for null-on-unknown.
	v, ok := srv.MatterRead(0x0000)
	if !ok {
		t.Error("MatterRead(0x0000) on unobserved humidity should return ok=true (null)")
	}
	if v != nil {
		t.Errorf("MatterRead(0x0000) on unobserved humidity = %v, want nil", v)
	}
}

// ---------------------------------------------------------------------------
// extractSetpointRaiseLower helper
// ---------------------------------------------------------------------------

func TestExtractSetpointRaiseLowerSuccess(t *testing.T) {
	fields := map[string]any{
		"mode":   uint8(0),
		"amount": int8(10),
	}
	mode, amount, err := extractSetpointRaiseLower(fields)
	if err != nil {
		t.Fatalf("extractSetpointRaiseLower: %v", err)
	}
	if mode != 0 || amount != 10 {
		t.Errorf("got mode=%d amount=%d, want 0/10", mode, amount)
	}
}

func TestExtractSetpointRaiseLowerMissingMode(t *testing.T) {
	_, _, err := extractSetpointRaiseLower(map[string]any{"amount": int8(5)})
	if err == nil {
		t.Error("missing mode should return error")
	}
}

func TestExtractSetpointRaiseLowerBadModeType(t *testing.T) {
	_, _, err := extractSetpointRaiseLower(map[string]any{"mode": "bad", "amount": int8(5)})
	if err == nil {
		t.Error("wrong mode type should return error")
	}
}

func TestExtractSetpointRaiseLowerMissingAmount(t *testing.T) {
	_, _, err := extractSetpointRaiseLower(map[string]any{"mode": uint8(0)})
	if err == nil {
		t.Error("missing amount should return error")
	}
}

func TestExtractSetpointRaiseLowerBadAmountType(t *testing.T) {
	_, _, err := extractSetpointRaiseLower(map[string]any{"mode": uint8(0), "amount": "bad"})
	if err == nil {
		t.Error("wrong amount type should return error")
	}
}

func TestExtractSetpointRaiseLowerWrongType(t *testing.T) {
	_, _, err := extractSetpointRaiseLower("not-a-map")
	if err == nil {
		t.Error("non-map input should return error")
	}
}

// ---------------------------------------------------------------------------
// toFloat helper
// ---------------------------------------------------------------------------

func TestToFloatVariants(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  float64
	}{
		{"float64", float64(3.14), 3.14},
		{"float32", float32(2.0), 2.0},
		{"int", int(5), 5.0},
		{"int32", int32(7), 7.0},
		{"int64", int64(9), 9.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toFloat(tc.input)
			if !ok || got != tc.want {
				t.Errorf("toFloat(%T(%v)) = (%v, %v), want (%v, true)", tc.input, tc.input, got, ok, tc.want)
			}
		})
	}
}

func TestToFloatUnknownTypeIgnored(t *testing.T) {
	_, ok := toFloat("not-a-float")
	if ok {
		t.Error("toFloat(string) should return ok=false")
	}
}

// ---------------------------------------------------------------------------
// RefreshLinkPeerActivitySources with real peer channels
// ---------------------------------------------------------------------------

func TestRefreshLinkPeerActivitySourcesLevelPeer(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{})

	// Build a peer channel with a LEVEL DP.
	peerDev := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "PEER0001"})
	peerCh := peerDev.AddChannel("PEER0001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	level := putFloatDPValues(peerCh, hmenum.ParameterLevel)

	cancel := r.climate.RefreshLinkPeerActivitySources([]*device.Channel{peerCh})
	defer cancel()

	level.OnEvent(1.0)
	a, ok := r.climate.Activity()
	if !ok || a != ActivityHeating {
		t.Errorf("peer LEVEL>0 → Activity() = (%v, %v), want (heating, true)", a, ok)
	}

	level.OnEvent(0.0)
	a, _ = r.climate.Activity()
	if a != ActivityIdle {
		t.Errorf("peer LEVEL=0 → Activity() = %v, want idle", a)
	}
}

func TestRefreshLinkPeerActivitySourcesStatePeer(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{})

	peerDev := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "PEER0002"})
	peerCh := peerDev.AddChannel("PEER0002:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	state := putBoolDP(peerCh, hmenum.ParameterState)

	cancel := r.climate.RefreshLinkPeerActivitySources([]*device.Channel{peerCh})
	defer cancel()

	state.OnEvent(true)
	a, ok := r.climate.Activity()
	if !ok || a != ActivityHeating {
		t.Errorf("peer STATE=true → Activity() = (%v, %v), want (heating, true)", a, ok)
	}

	state.OnEvent(false)
	a, _ = r.climate.Activity()
	if a != ActivityIdle {
		t.Errorf("peer STATE=false → Activity() = %v, want idle", a)
	}
}

func TestRefreshLinkPeerActivitySourcesCoolingPeer(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{})
	r.climate.OnHeatingCooling("COOLING")

	peerDev := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "PEER0003"})
	peerCh := peerDev.AddChannel("PEER0003:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	level := putFloatDPValues(peerCh, hmenum.ParameterLevel)

	cancel := r.climate.RefreshLinkPeerActivitySources([]*device.Channel{peerCh})
	defer cancel()

	level.OnEvent(0.5)
	a, ok := r.climate.Activity()
	if !ok || a != ActivityCooling {
		t.Errorf("cooling peer LEVEL>0 → Activity() = (%v, %v), want (cooling, true)", a, ok)
	}
}

// ---------------------------------------------------------------------------
// profileForWeekIndex edge cases (index ≤0 and >6)
// ---------------------------------------------------------------------------

func TestProfileForWeekIndexOutOfRange(t *testing.T) {
	// Exercise the out-of-range default (returns "", false).
	// Drive via OnActiveProfile(7, false) — 1-based idx=7 is out of range.
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnMode(ModeAuto)
	prev := ProfileWeekProgram1
	r.climate.OnProfile(prev)
	r.climate.OnActiveProfile(7, false)
	// Profile should remain unchanged because profileForWeekIndex(7) returns false.
	p, ok := r.climate.Profile()
	if !ok || p != prev {
		t.Errorf("out-of-range idx: Profile() = (%v, %v), want (%v, true)", p, ok, prev)
	}
}

// ---------------------------------------------------------------------------
// matter server – ThermostatUI (0x0204) coverage
// ---------------------------------------------------------------------------

func TestClimateThermostatUIServerRead(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0204) // ThermostatUserInterfaceConfiguration

	v, ok := srv.MatterRead(0x0000) // TemperatureDisplayMode
	if !ok {
		t.Error("MatterRead(0x0000) on UI cluster should return ok=true")
	}
	if v == nil {
		t.Error("MatterRead(0x0000) on UI cluster should return non-nil value")
	}
}

func TestClimateThermostatUIServerReadUnknownAttr(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0204)
	_, ok := srv.MatterRead(0xFFFF)
	if ok {
		t.Error("MatterRead(0xFFFF) on UI cluster should return ok=false")
	}
}

func TestClimateThermostatUIServerMatterWrite(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0204)
	err := srv.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite on UI cluster should return error")
	}
}

func TestClimateThermostatUIServerMatterReportable(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0204)
	// Reportable is nil for the UI cluster.
	_ = srv.MatterReportable()
}

func TestClimateThermostatUIServerMatterAttributes(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0204)
	lister, ok := srv.(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("ThermostatUI server does not implement MatterClusterAttributeLister")
	}
	if got := lister.MatterAttributes(); len(got) == 0 {
		t.Error("MatterAttributes() returned empty slice for ThermostatUI")
	}
}

// ---------------------------------------------------------------------------
// matter server – Thermostat (0x0201) additional coverage
// ---------------------------------------------------------------------------

func TestClimateThermostatServerMatterAttributes(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	lister, ok := srv.(interfaces.MatterClusterAttributeLister)
	if !ok {
		t.Fatal("Thermostat server does not implement MatterClusterAttributeLister")
	}
	attrs := lister.MatterAttributes()
	if len(attrs) == 0 {
		t.Error("MatterAttributes() returned empty slice for Thermostat")
	}
}

func TestClimateThermostatServerMatterReportable(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	if got := srv.MatterReportable(); len(got) == 0 {
		t.Error("MatterReportable() returned empty slice for Thermostat")
	}
}

func TestClimateThermostatServerReadMinMaxSetpoints(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{},
		custom.ClimateCapabilities{MinTemperature: 5.0, MaxTemperature: 30.0})
	srv := findCluster(t, r.climate, 0x0201)

	// MinHeatSP = 0x0015, MaxHeatSP = 0x0016 (from matter.go constants).
	_, ok := srv.MatterRead(matterAttrThermMinHeatSp)
	if !ok {
		t.Error("MatterRead(MinHeatSP) should return ok=true")
	}
	_, ok = srv.MatterRead(matterAttrThermMaxHeatSp)
	if !ok {
		t.Error("MatterRead(MaxHeatSP) should return ok=true")
	}
}

func TestClimateThermostatServerReadControlSeq(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	_, ok := srv.MatterRead(matterAttrThermControlSeq)
	if !ok {
		t.Error("MatterRead(ControlSeq) should return ok=true")
	}
	// 0x0030 is SetpointChangeSource in matter.js (not LocalTemperatureNotExposed,
	// which is FeatureMap bit 6). The bridge must not advertise a fabricated
	// attribute at 0x0030.
	if _, ok := srv.MatterRead(0x0030); ok {
		t.Error("MatterRead(0x0030) must return ok=false — no fabricated LTNE attribute")
	}
}

func TestClimateThermostatServerReadSystemMode(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)

	// Unobserved: (nil, true).
	v, ok := srv.MatterRead(matterAttrThermSystemMode)
	if !ok || v != nil {
		t.Errorf("unobserved SystemMode = (%v, %v), want (nil, true)", v, ok)
	}

	// Observed AUTO: uint8.
	r.climate.OnMode(ModeAuto)
	v, ok = srv.MatterRead(matterAttrThermSystemMode)
	if !ok || v == nil {
		t.Errorf("observed SystemMode = (%v, %v), want non-nil", v, ok)
	}
}

func TestClimateThermostatServerMatterWriteSetpoint(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{MinTemperature: 5.0, MaxTemperature: 30.0})
	srv := findCluster(t, r.climate, 0x0201)

	// Write 2200 → 22.00°C via OccupiedHeatSetpoint.
	err := srv.MatterWrite(context.Background(), matterAttrThermOccupiedHeatSp, int16(2200), hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MatterWrite setpoint: %v", err)
	}
}

func TestClimateThermostatServerMatterWriteSetpointBadType(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	err := srv.MatterWrite(context.Background(), matterAttrThermOccupiedHeatSp, "not-int16", hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite with bad type should return error")
	}
}

// TestClimateThermostatServerMatterWriteSystemMode asserts that
// SystemMode=Auto is rejected on a heating-only Climate: Auto conformance
// requires the AUTO feature (thermostat-cluster.element.ts:558), and
// every climate profile registered in init.go reports HEAT only
// (Capabilities.SupportsCool is false), so FeatureMap never advertises
// AUTO. The rejection is a ConstraintError, matching matter.js
// ThermostatServer.ts:#assertSystemModeChanging for an analogous
// SystemMode/feature mismatch.
func TestClimateThermostatServerMatterWriteSystemMode(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	err := srv.MatterWrite(context.Background(), matterAttrThermSystemMode, matterSysModeAuto, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("MatterWrite SystemMode=Auto on a heating-only device should be rejected")
	}
	sc, ok := err.(interface{ MatterStatusCode() im.StatusCode })
	if !ok {
		t.Fatalf("error %v does not implement MatterStatusCode()", err)
	}
	if sc.MatterStatusCode() != im.StatusConstraintError {
		t.Errorf("MatterStatusCode() = 0x%02X, want StatusConstraintError (0x87)", sc.MatterStatusCode())
	}
}

func TestClimateThermostatServerMatterWriteSystemModeBadType(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	err := srv.MatterWrite(context.Background(), matterAttrThermSystemMode, "not-uint8", hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite SystemMode with bad type should return error")
	}
}

func TestClimateThermostatServerMatterWriteUnknownAttr(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	err := srv.MatterWrite(context.Background(), 0xFFF0, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite unknown attr should return error")
	}
}

func TestClimateThermostatServerMatterWriteSystemModeUnknownMode(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	// Mode 0x7F is not mapped by matterToHmMode.
	err := srv.MatterWrite(context.Background(), matterAttrThermSystemMode, uint8(0x7F), hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterWrite with unknown SystemMode value should return error")
	}
}

// ---------------------------------------------------------------------------
// systemModeFromHmMode / matterToHmMode
// ---------------------------------------------------------------------------

func TestSystemModeFromHmModeClampsToFeatureMap(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := climateThermostatServer{c: r.climate}

	heatOnly := matterThermFeatureHeat
	heatCool := matterThermFeatureHeat | matterThermFeatureCool
	withAuto := heatCool | matterThermFeatureAuto

	cases := []struct {
		name string
		mode Mode
		fm   uint32
		want uint8
	}{
		{"auto clamps to Heat without AUTO feature", ModeAuto, heatOnly, matterSysModeHeat},
		{"auto stays Auto with AUTO feature", ModeAuto, withAuto, matterSysModeAuto},
		{"heat maps to Heat", ModeHeat, heatOnly, matterSysModeHeat},
		{"cool maps to Cool with COOL feature", ModeCool, heatCool, matterSysModeCool},
		{"cool clamps to Heat without COOL feature", ModeCool, heatOnly, matterSysModeHeat},
		{"off maps to Off", ModeOff, heatOnly, matterSysModeOff},
		{"unknown mode defaults to Heat", Mode("unknown"), heatOnly, matterSysModeHeat},
	}
	for _, tc := range cases {
		if got := srv.systemModeFromHmMode(tc.mode, tc.fm); got != tc.want {
			t.Errorf("%s: systemModeFromHmMode(%q, 0x%02X) = %d, want %d",
				tc.name, tc.mode, tc.fm, got, tc.want)
		}
	}

	// A cooling-direction hybrid clamps ModeAuto to Cool instead of Heat.
	r.climate.OnHeatingCooling("COOLING")
	if got := srv.systemModeFromHmMode(ModeAuto, heatCool); got != matterSysModeCool {
		t.Errorf("systemModeFromHmMode(ModeAuto, HEAT|COOL) in cooling direction = %d, want %d",
			got, matterSysModeCool)
	}
}

func TestMatterToHmMode(t *testing.T) {
	cases := []struct {
		raw  uint8
		want Mode
	}{
		{matterSysModeOff, ModeOff},
		{matterSysModeAuto, ModeAuto},
		{matterSysModeHeat, ModeHeat},
		{matterSysModeCool, ModeCool},
	}
	for _, tc := range cases {
		got, err := matterToHmMode(tc.raw)
		if err != nil || got != tc.want {
			t.Errorf("matterToHmMode(%d) = (%v, %v), want (%v, nil)", tc.raw, got, err, tc.want)
		}
	}
}

func TestMatterToHmModeUnknown(t *testing.T) {
	_, err := matterToHmMode(0xFF)
	if err == nil {
		t.Error("matterToHmMode(0xFF) should return error")
	}
}

// ---------------------------------------------------------------------------
// celsiusToMatter / humidityToMatter clamping edge cases
// ---------------------------------------------------------------------------

func TestCelsiusToMatterClamping(t *testing.T) {
	// Positive overflow clamps below the Matter NULL sentinel (32767), not
	// at the raw int16 ceiling.
	if got := celsiusToMatter(400.0); got != 32766 {
		t.Errorf("celsiusToMatter(400) = %d, want 32766", got)
	}
	// Negative overflow clamps at the TemperatureMeasurement cluster's
	// absolute-zero constraint floor (-273.15 °C), not at the raw int16
	// floor.
	if got := celsiusToMatter(-400.0); got != -27315 {
		t.Errorf("celsiusToMatter(-400) = %d, want -27315", got)
	}
}

// TestCelsiusToMatterRoundsTenthDegreeReadings pins the encoder against
// the binary64 artefact of the multiplication: a tenth-of-a-degree
// reading whose ×100 product lands just below an exact hundredth used to
// truncate a hundredth away, reporting a temperature every other surface
// showed one step higher. The listed inputs are the ones that reproduce.
func TestCelsiusToMatterRoundsTenthDegreeReadings(t *testing.T) {
	cases := []struct {
		celsius float64
		want    int16
	}{
		{20.4, 2040},
		{16.9, 1690},
		{8.2, 820},
		{4.1, 410},
		{-20.4, -2040},
		{21.5, 2150},
		{0, 0},
	}
	for _, c := range cases {
		if got := celsiusToMatter(c.celsius); got != c.want {
			t.Errorf("celsiusToMatter(%v) = %d, want %d", c.celsius, got, c.want)
		}
	}
}

func TestHumidityToMatterClamping(t *testing.T) {
	if got := humidityToMatter(-1.0); got != 0 {
		t.Errorf("humidityToMatter(-1) = %d, want 0", got)
	}
	if got := humidityToMatter(200.0); got != 10000 {
		t.Errorf("humidityToMatter(200) = %d, want 10000", got)
	}
}

// ---------------------------------------------------------------------------
// matter server – MatterInvoke SetpointRaiseLower
// ---------------------------------------------------------------------------

func TestClimateThermostatServerMatterInvokeSetpointRaiseLower(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-BWTH:1", KindIP, w, custom.ClimateCapabilities{MinTemperature: 5.0, MaxTemperature: 30.0})
	r.setpoint.OnEvent(20.0)
	srv := findCluster(t, r.climate, 0x0201)

	fields := map[string]any{"mode": uint8(0), "amount": int8(10)} // +1.0°C
	resp, err := srv.MatterInvoke(context.Background(), 0x00, fields, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("SetpointRaiseLower +1°C: %v", err)
	}
	_ = resp
}

func TestClimateThermostatServerMatterInvokeUnknownCmd(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	_, err := srv.MatterInvoke(context.Background(), 0xFF, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke with unknown cmd should return error")
	}
}

func TestClimateThermostatServerMatterInvokeNoSetpoint(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	// No setpoint observation — invoke should fail.
	srv := findCluster(t, r.climate, 0x0201)
	fields := map[string]any{"mode": uint8(0), "amount": int8(10)}
	_, err := srv.MatterInvoke(context.Background(), 0x00, fields, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("SetpointRaiseLower without baseline setpoint should return error")
	}
}

// ---------------------------------------------------------------------------
// init.go — group constructors (internal package tests)
// ---------------------------------------------------------------------------

func TestRfThermostatGroupConstructorReturnsDP(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmRF", Address: "GRP001"})
	ch := d.AddChannel("GRP001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileRfThermostatGroup)
	if !ok {
		t.Fatal("constructor for DeviceProfileRfThermostatGroup not registered")
	}
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil || dp == nil {
		t.Fatalf("rfThermostatGroupConstructor returned (nil, %v)", err)
	}
}

func TestIPThermostatGroupConstructorReturnsDP(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "GRP002"})
	ch := d.AddChannel("GRP002:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPThermostatGroup)
	if !ok {
		t.Fatal("constructor for DeviceProfileIPThermostatGroup not registered")
	}
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil || dp == nil {
		t.Fatalf("ipThermostatGroupConstructor returned (nil, %v)", err)
	}
}

// ---------------------------------------------------------------------------
// registerServices (indirectly via Service method)
// ---------------------------------------------------------------------------

func TestRegisterServicesSet(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 5.0,
		MaxTemperature: 30.0,
	})
	// set_temperature service.
	err := r.climate.Invoke(context.Background(), "set_temperature",
		map[string]any{"temperature": 21.0}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_temperature service: %v", err)
	}
}

func TestRegisterServicesSetMode(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{})
	err := r.climate.Invoke(context.Background(), "set_mode",
		map[string]any{"mode": "auto"}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_mode service: %v", err)
	}
}

func TestRegisterServicesSetProfile(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsProfile: true})
	err := r.climate.Invoke(context.Background(), "set_profile",
		map[string]any{"profile": "week_program_1"}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_profile service: %v", err)
	}
}

func TestRegisterServicesEnableBoost(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsBoost: true})
	err := r.climate.Invoke(context.Background(), "enable_boost",
		nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("enable_boost service: %v", err)
	}
}

func TestRegisterServicesDisableBoost(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsBoost: true})
	err := r.climate.Invoke(context.Background(), "disable_boost",
		nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("disable_boost service: %v", err)
	}
}

func TestRegisterServicesSetTemperatureOffset(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{})
	err := r.climate.Invoke(context.Background(), "set_temperature_offset",
		map[string]any{"offset": 0.5}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_temperature_offset service: %v", err)
	}
}

func TestRegisterServicesDisableAway(t *testing.T) {
	w := &putWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsAway: true})
	err := r.climate.Invoke(context.Background(), "disable_away",
		nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("disable_away service: %v", err)
	}
}

// ---------------------------------------------------------------------------
// parseFloat edge cases
// ---------------------------------------------------------------------------

func TestParseFlatBoolTrue(t *testing.T) {
	v, ok := parseFloat([]byte("true"))
	if !ok || v != 1.0 {
		t.Errorf("parseFloat(true) = (%v, %v), want (1.0, true)", v, ok)
	}

	v, ok = parseFloat([]byte("false"))
	if !ok || v != 0.0 {
		t.Errorf("parseFloat(false) = (%v, %v), want (0.0, true)", v, ok)
	}
}

func TestParseFlatInvalidJSON(t *testing.T) {
	_, ok := parseFloat([]byte("{invalid"))
	if ok {
		t.Error("parseFloat with invalid JSON should return ok=false")
	}
}

func TestParseFlatEmpty(t *testing.T) {
	_, ok := parseFloat(nil)
	if ok {
		t.Error("parseFloat with nil should return ok=false")
	}
}

// ---------------------------------------------------------------------------
// SetTemperature — no-setpoint-dp path (writer-only)
// ---------------------------------------------------------------------------

func TestClimateSetTemperatureWriterOnlyPath(t *testing.T) {
	// Build a Climate with NO setpoint DP attached — it falls through to
	// the writer.SetValue path.
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "WONLY01"})
	ch := d.AddChannel("WONLY01:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{MinTemperature: 5.0, MaxTemperature: 30.0}, Kind: KindIP})

	if err := c.SetTemperature(context.Background(), 22.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("SetTemperature writer-only: %v", err)
	}
	if got := w.last(); got.param != hmenum.ParameterSetPointTemperature {
		t.Errorf("writer-only path wrote param=%v, want SET_POINT_TEMPERATURE", got.param)
	}
}

func TestClimateSetTemperatureNoSetpointNoWriter(t *testing.T) {
	c := &Climate{Kind: KindIP}
	if err := c.SetTemperature(context.Background(), 22.0, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetTemperature with no setpoint and no writer should return error")
	}
}

func TestClimateSetTemperatureRFWriterOnlyPath(t *testing.T) {
	w := &stubWriter{}
	d := device.New(device.Config{InterfaceID: "HmRF", Address: "RF_WONLY"})
	ch := d.AddChannel("RF_WONLY:2", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	c := New(Config{Channel: ch, Writer: w, Capabilities: custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5}, Kind: KindRF})

	if err := c.SetTemperature(context.Background(), 18.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("RF SetTemperature writer-only: %v", err)
	}
	if got := w.last(); got.param != hmenum.ParameterSetTemperature {
		t.Errorf("RF writer-only path wrote param=%v, want SET_TEMPERATURE", got.param)
	}
}

// ---------------------------------------------------------------------------
// rfControlModeLabel — covers the empty-string default for index > 3
// ---------------------------------------------------------------------------

func TestRfControlModeLabelOutOfRange(t *testing.T) {
	// Indirectly: OnControlMode with int(99) — rfControlModeLabel returns ""
	// which falls through the switch without changing any state.
	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnControlMode(int(99))
	_, ok := r.climate.Mode()
	if ok {
		t.Error("OnControlMode with out-of-range int should leave mode unset")
	}
}

// ---------------------------------------------------------------------------
// setSimpleRFMode — ModeCool unsupported branch
// ---------------------------------------------------------------------------

func TestClimateSetModeSimpleRFCoolUnsupported(t *testing.T) {
	r := newRig(t, "x", KindSimpleRF, &stubWriter{}, custom.ClimateCapabilities{})
	if err := r.climate.SetMode(context.Background(), ModeCool, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SimpleRF ModeCool should return ErrModeNotSupported")
	}
}

// ---------------------------------------------------------------------------
// numWeekPrograms — zero-width descriptor (lo==hi) and nil channelRef
// ---------------------------------------------------------------------------

func TestNumWeekProgramsKindIPNoChannelRef(t *testing.T) {
	// Climate with no channelRef and KindIP → kind-default of 6.
	c := &Climate{Kind: KindIP}
	if n := c.numWeekPrograms(); n != 6 {
		t.Errorf("numWeekPrograms() without channelRef (KindIP) = %d, want 6", n)
	}
}

func TestNumWeekProgramsKindRFNoChannelRef(t *testing.T) {
	c := &Climate{Kind: KindRF}
	if n := c.numWeekPrograms(); n != 3 {
		t.Errorf("numWeekPrograms() without channelRef (KindRF) = %d, want 3", n)
	}
}

func TestNumWeekProgramsKindSimpleRFNoChannelRef(t *testing.T) {
	c := &Climate{Kind: KindSimpleRF}
	if n := c.numWeekPrograms(); n != 0 {
		t.Errorf("numWeekPrograms() without channelRef (KindSimpleRF) = %d, want 0", n)
	}
}

// ---------------------------------------------------------------------------
// resolveWeekProgramPointer — KindSimpleRF returns nil
// ---------------------------------------------------------------------------

func TestResolveWeekProgramPointerSimpleRF(t *testing.T) {
	d := device.New(device.Config{InterfaceID: "HmRF", Address: "SRFWPP01"})
	ch := d.AddChannel("SRFWPP01:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	if got := resolveWeekProgramPointer(ch, KindSimpleRF); got != nil {
		t.Errorf("resolveWeekProgramPointer SimpleRF = %v, want nil", got)
	}
}

func TestResolveWeekProgramPointerNilChannel(t *testing.T) {
	if got := resolveWeekProgramPointer(nil, KindIP); got != nil {
		t.Errorf("resolveWeekProgramPointer nil channel = %v, want nil", got)
	}
}

// ---------------------------------------------------------------------------
// profileForWeekIndex — all six valid + invalid
// ---------------------------------------------------------------------------

func TestProfileForWeekIndexAllValid(t *testing.T) {
	cases := []struct {
		idx  int
		want Profile
	}{
		{1, ProfileWeekProgram1},
		{2, ProfileWeekProgram2},
		{3, ProfileWeekProgram3},
		{4, ProfileWeekProgram4},
		{5, ProfileWeekProgram5},
		{6, ProfileWeekProgram6},
	}
	for _, tc := range cases {
		got, ok := profileForWeekIndex(tc.idx)
		if !ok || got != tc.want {
			t.Errorf("profileForWeekIndex(%d) = (%v, %v), want (%v, true)", tc.idx, got, ok, tc.want)
		}
	}
}

func TestProfileForWeekIndexInvalid(t *testing.T) {
	_, ok := profileForWeekIndex(0)
	if ok {
		t.Error("profileForWeekIndex(0) should return false")
	}
	_, ok = profileForWeekIndex(7)
	if ok {
		t.Error("profileForWeekIndex(7) should return false")
	}
}

// ---------------------------------------------------------------------------
// parseFloat — string/array type returns false
// ---------------------------------------------------------------------------

func TestParseFlatUnsupportedType(t *testing.T) {
	_, ok := parseFloat([]byte(`"hello"`))
	if ok {
		t.Error("parseFloat(string JSON) should return ok=false")
	}
}

// ---------------------------------------------------------------------------
// toUint16Minutes / toFloat64
// ---------------------------------------------------------------------------

func TestToUint16MinutesVariants(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  uint16
	}{
		{"int", int(720), 720},
		{"int32", int32(60), 60},
		{"int64", int64(1440), 1440},
		{"float64", float64(90), 90},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toUint16Minutes(tc.input)
			if !ok || got != tc.want {
				t.Errorf("toUint16Minutes(%T(%v)) = (%d, %v), want (%d, true)", tc.input, tc.input, got, ok, tc.want)
			}
		})
	}
}

func TestToUint16MinutesOutOfRange(t *testing.T) {
	cases := []any{int(-1), int32(-1), int64(1441), float64(-10)}
	for _, v := range cases {
		_, ok := toUint16Minutes(v)
		if ok {
			t.Errorf("toUint16Minutes(%T(%v)) should return false for out-of-range", v, v)
		}
	}
}

func TestToUint16MinutesUnknownType(t *testing.T) {
	_, ok := toUint16Minutes("invalid")
	if ok {
		t.Error("toUint16Minutes(string) should return false")
	}
}

func TestToFloat64Variants(t *testing.T) {
	cases := []struct {
		name  string
		input any
		want  float64
	}{
		{"float64", float64(21.5), 21.5},
		{"float32", float32(18.0), 18.0},
		{"int", int(20), 20.0},
		{"int32", int32(22), 22.0},
		{"int64", int64(25), 25.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toFloat64(tc.input)
			if !ok || got != tc.want {
				t.Errorf("toFloat64(%T(%v)) = (%v, %v), want (%v, true)", tc.input, tc.input, got, ok, tc.want)
			}
		})
	}
}

func TestToFloat64UnknownType(t *testing.T) {
	_, ok := toFloat64("not-a-float")
	if ok {
		t.Error("toFloat64(string) should return false")
	}
}

// ---------------------------------------------------------------------------
// MatterScheduleEntries — nil / no-profile / non-week-program paths
// ---------------------------------------------------------------------------

func TestMatterScheduleEntriesNilClimate(t *testing.T) {
	var c *Climate
	if got := c.MatterScheduleEntries(); got != nil {
		t.Errorf("nil Climate.MatterScheduleEntries() = %v, want nil", got)
	}
}

func TestMatterScheduleEntriesNoChannelRef(t *testing.T) {
	c := &Climate{}
	if got := c.MatterScheduleEntries(); got != nil {
		t.Errorf("no channelRef: MatterScheduleEntries() = %v, want nil", got)
	}
}

func TestMatterScheduleEntriesNoProfile(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	// No profile observed → returns nil.
	if got := r.climate.MatterScheduleEntries(); got != nil {
		t.Errorf("no profile: MatterScheduleEntries() = %v, want nil", got)
	}
}

func TestMatterScheduleEntriesBoostProfile(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnProfile(ProfileBoost)
	// BOOST has no week-program index → empty.
	if got := r.climate.MatterScheduleEntries(); got != nil {
		t.Errorf("boost profile: MatterScheduleEntries() = %v, want nil", got)
	}
}

func TestMatterScheduleEntriesWeekProfile(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnProfile(ProfileWeekProgram1)
	// No MASTER params attached → empty (but non-panic, and returns a
	// non-nil empty slice because the loop body finds no DPs).
	// The function returns make([]...{}, 0, ...) so it may return empty or nil.
	_ = r.climate.MatterScheduleEntries() // must not panic
}

// ---------------------------------------------------------------------------
// matter.go — ThermostatUI MatterRead FeatureMap/ClusterRevision paths
// ---------------------------------------------------------------------------

func TestClimateThermostatUIServerReadFeatureMap(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0204)
	v, ok := srv.MatterRead(matterAttrFeatureMap)
	if !ok || v == nil {
		t.Errorf("MatterRead(FeatureMap) on UI cluster = (%v, %v), want non-nil/true", v, ok)
	}
}

func TestClimateThermostatUIServerMatterInvoke(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0204)
	_, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke on UI cluster should always return error")
	}
}

// ---------------------------------------------------------------------------
// matter.go — TemperatureMeasurement read FeatureMap + ClusterRevision
// ---------------------------------------------------------------------------

func TestClimateTempMeasServerReadFeatureMap(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0402)
	v, ok := srv.MatterRead(matterAttrFeatureMap)
	if !ok || v == nil {
		t.Errorf("MatterRead(FeatureMap) on TempMeas = (%v, %v), want non-nil/true", v, ok)
	}
}

func TestClimateTempMeasServerMatterInvoke(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0402)
	_, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke on TempMeas should always return error")
	}
}

// ---------------------------------------------------------------------------
// matter.go — HumidityMeasurement observed + ClusterRevision
// ---------------------------------------------------------------------------

func TestClimateHumidityServerReadObserved(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.humidity.OnEvent(62.5)
	srv := findCluster(t, r.climate, 0x0405)
	v, ok := srv.MatterRead(matterAttrMeasuredValue)
	if !ok || v == nil {
		t.Errorf("observed humidity MatterRead = (%v, %v), want non-nil/true", v, ok)
	}
}

func TestClimateHumidityServerReadClusterRevision(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0405)
	v, ok := srv.MatterRead(matterAttrClusterRevision)
	if !ok || v == nil {
		t.Errorf("humidity ClusterRevision = (%v, %v), want non-nil/true", v, ok)
	}
}

func TestClimateHumidityServerMatterInvoke(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0405)
	_, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Error("MatterInvoke on humidity cluster should always return error")
	}
}

// ---------------------------------------------------------------------------
// payload.go — StatePayload + ConfigPayload extended coverage
// ---------------------------------------------------------------------------

func TestClimateStatePayloadWithActivity(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnMode(ModeAuto)
	r.climate.OnActivity(ActivityHeating)
	sp, _ := r.climate.State().(*payload.ClimateState)
	if sp == nil || sp.Action != string(ActivityHeating) {
		t.Errorf("StatePayload action=%v, want heating", sp.Action)
	}
}

func TestClimateStatePayloadTemperatureOffset(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnTemperatureOffset(0.5)
	sp, _ := r.climate.State().(*payload.ClimateState)
	if sp == nil || sp.TemperatureOffset == nil {
		t.Error("StatePayload should contain temperature_offset when observed")
	}
}

func TestClimateStatePayloadOptimumStartStop(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnOptimumStartStop(true)
	sp, _ := r.climate.State().(*payload.ClimateState)
	if sp == nil || sp.OptimumStartStop == nil {
		t.Error("StatePayload should contain optimum_start_stop when observed")
	}
}

func TestClimateConfigPayloadNoModes(t *testing.T) {
	// No modes capability — hvac_modes key absent from config.
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	cp, _ := r.climate.Config().(*payload.ClimateConfig)
	if cp == nil {
		t.Fatal("ConfigPayload should not be nil")
	}
	// When there are no modes, HVACModes should be empty.
	if len(cp.HVACModes) > 0 {
		t.Error("ConfigPayload should not have hvac_modes when no modes are supported")
	}
}

func TestClimateConfigPayloadWithModes(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		SupportsAuto: true,
		SupportsHeat: true,
		SupportsOff:  true,
	})
	cp, _ := r.climate.Config().(*payload.ClimateConfig)
	if cp == nil || len(cp.HVACModes) == 0 {
		t.Error("ConfigPayload should contain hvac_modes when modes are supported")
	}
}

func TestClimateConfigPayloadWithProfilesNoneFiltered(t *testing.T) {
	// Profiles list includes ProfileNone — it must NOT appear in preset_modes.
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{
		SupportsProfile: true,
		SupportsBoost:   true,
		SupportsAuto:    true,
	})
	r.climate.OnMode(ModeAuto)
	cp, _ := r.climate.Config().(*payload.ClimateConfig)
	if cp != nil {
		for _, p := range cp.PresetModes {
			if p == string(ProfileNone) {
				t.Error("ConfigPayload preset_modes should not contain 'none'")
			}
		}
	}
}

// ---------------------------------------------------------------------------
// registerServices — set_away and set_away_for_duration
// ---------------------------------------------------------------------------

func TestRegisterServicesSetAway(t *testing.T) {
	w := &putWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsAway: true, MinTemperature: 5.0, MaxTemperature: 30.0})
	err := r.climate.Invoke(context.Background(), "set_away",
		map[string]any{
			"until":            "2026-12-01T12:00:00Z",
			"away_temperature": 15.0,
		}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_away service: %v", err)
	}
}

func TestRegisterServicesSetAwayForDuration(t *testing.T) {
	w := &putWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsAway: true, MinTemperature: 5.0, MaxTemperature: 30.0})
	err := r.climate.Invoke(context.Background(), "set_away_for_duration",
		map[string]any{
			"hours":            2.0,
			"away_temperature": 14.0,
		}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_away_for_duration service: %v", err)
	}
}

// ---------------------------------------------------------------------------
// MatterRead on Thermostat cluster — RunningMode path
// ---------------------------------------------------------------------------

// TestClimateThermostatServerReadRunningMode confirms
// ThermostatRunningMode (conformance "TEVT & AUTO, [AUTO]") reads as
// not-present on a heating-only Climate — every profile registered in
// init.go reports HEAT only, so FeatureMap never advertises AUTO and the
// attribute must not appear, observed or not.
func TestClimateThermostatServerReadRunningMode(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)

	// Unobserved.
	if _, ok := srv.MatterRead(matterAttrThermRunningMode); ok {
		t.Error("RunningMode must read as not-present without the AUTO feature")
	}

	r.climate.OnMode(ModeHeat)
	if _, ok := srv.MatterRead(matterAttrThermRunningMode); ok {
		t.Error("RunningMode must remain not-present without the AUTO feature even once observed")
	}
}

// ---------------------------------------------------------------------------
// MatterRead on Thermostat — LocalTemperature unobserved + observed
// ---------------------------------------------------------------------------

func TestClimateThermostatServerReadLocalTempUnobserved(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	v, ok := srv.MatterRead(matterAttrThermLocalTemperature)
	if !ok || v != nil {
		t.Errorf("unobserved LocalTemperature = (%v, %v), want (nil, true)", v, ok)
	}
}

// ---------------------------------------------------------------------------
// MatterRead on Thermostat — FeatureMap and ClusterRevision globals
// ---------------------------------------------------------------------------

func TestClimateThermostatServerReadFeatureMap(t *testing.T) {
	r := newRig(t, "HmIP-BWTH:1", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	srv := findCluster(t, r.climate, 0x0201)
	v, ok := srv.MatterRead(matterAttrFeatureMap)
	if !ok || v == nil {
		t.Errorf("FeatureMap = (%v, %v), want non-nil/true", v, ok)
	}
}

// ---------------------------------------------------------------------------
// IPThermostatGroupConstructorIsRegistered (internal package test)
// ---------------------------------------------------------------------------

func TestIPThermostatGroupConstructorIsRegistered(t *testing.T) {
	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPThermostatGroup)
	if !ok || ctor == nil {
		t.Fatal("constructor for DeviceProfileIPThermostatGroup not registered")
	}
}
