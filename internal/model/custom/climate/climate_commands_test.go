// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package climate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSetTemperatureRejectsOutOfRange verifies that temperatures outside the
// configured range are rejected with ErrTemperatureOutOfRange rather than
// silently clamped.
func TestSetTemperatureRejectsOutOfRange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		kind  Kind
		input float64
	}{
		{KindIP, 40},
		{KindIP, 0},
		{KindRF, 40},
	}
	for _, tc := range cases {
		w := &stubWriter{}
		r := newRig(t, "x", tc.kind, w, custom.ClimateCapabilities{
			MinTemperature: 4.5,
			MaxTemperature: 30.5,
		})
		err := r.climate.SetTemperature(context.Background(), tc.input, hmenum.CommandPriorityHigh)
		if !errors.Is(err, ErrTemperatureOutOfRange) {
			t.Errorf("kind=%v input=%v: got err=%v, want ErrTemperatureOutOfRange", tc.kind, tc.input, err)
		}
	}
}

// TestIPSetModeAutoWritesControlMode verifies that SetMode(ModeAuto) writes
// CONTROL_MODE=0 (write-only ACTION). SET_POINT_MODE is read-only on most
// CCU firmwares — writing it is a no-op the device immediately overrides
// from the active control flow.
func TestIPSetModeAutoWritesControlMode(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
	if err := r.climate.SetMode(context.Background(), ModeAuto, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterControlMode || got.value.(int32) != 0 {
		t.Fatalf("SetMode(Auto) wrote %+v, want CONTROL_MODE=0", got)
	}
}

// TestIPSetModeOffAtomicPutParamset verifies the IP off-mode puts both
// CONTROL_MODE=1 and SET_POINT_TEMPERATURE=4.5 atomically.
func TestIPSetModeOffAtomicPutParamset(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	r := newRig(t, "VCU0000050:4", KindIP, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
	if err := r.climate.SetMode(context.Background(), ModeOff, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d (sets=%d)", len(w.puts), len(w.calls))
	}
	got := w.puts[0]
	if got[string(hmenum.ParameterControlMode)].(int32) != 1 {
		t.Errorf("CONTROL_MODE=%v, want 1 (MANU)", got[string(hmenum.ParameterControlMode)])
	}
	if got[string(hmenum.ParameterSetPointTemperature)].(float64) != 4.5 {
		t.Errorf("SET_POINT_TEMPERATURE=%v, want 4.5", got[string(hmenum.ParameterSetPointTemperature)])
	}
}

// TestRFSetModeOffAtomicPutParamset verifies the RF off-mode bundles
// MANU_MODE + SET_TEMPERATURE atomically.
func TestRFSetModeOffAtomicPutParamset(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	r := newRig(t, "VCU0000341:2", KindRF, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
	if err := r.climate.SetMode(context.Background(), ModeOff, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if got[string(hmenum.ParameterManuMode)].(float64) != 4.5 {
		t.Errorf("MANU_MODE=%v, want 4.5", got[string(hmenum.ParameterManuMode)])
	}
	if got[string(hmenum.ParameterSetTemperature)].(float64) != 4.5 {
		t.Errorf("SET_TEMPERATURE=%v, want 4.5", got[string(hmenum.ParameterSetTemperature)])
	}
}

// TestRFSetModeHeatWritesManuMode verifies that RF heat mode restores the last
// known manual setpoint via temperatureForHeatMode rather than always
// writing MaxTemperature. When no setpoint has been observed, min_temp (4.5)
// is used.
func TestRFSetModeHeatWritesManuMode(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", KindRF, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
	if err := r.climate.SetMode(context.Background(), ModeHeat, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterManuMode {
		t.Fatalf("RF heat wrote param=%v, want MANU_MODE", got.param)
	}
	// min_temp (4.5) equals the off-sentinel, so temperatureForHeatMode
	// returns off-sentinel + step = 5.0.
	if got.value.(float64) != 5.0 {
		t.Fatalf("RF heat wrote value=%v, want 5.0 (off-sentinel+step, no prior setpoint)", got.value)
	}
}

// TestSetProfileWeekProgramIPMapsToActiveProfile verifies that on KindIP
// the week-program profile writes ACTIVE_PROFILE with a 1-based int index
// (HmIP shape).
func TestSetProfileWeekProgramIPMapsToActiveProfile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		profile Profile
		wantIdx int32
	}{
		{ProfileWeekProgram1, 1},
		{ProfileWeekProgram2, 2},
		{ProfileWeekProgram3, 3},
	}
	for _, tc := range cases {
		w := &stubWriter{}
		r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsProfile: true})
		if err := r.climate.SetProfile(context.Background(), tc.profile, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("profile=%v: %v", tc.profile, err)
		}
		got := w.last()
		if got.param != hmenum.ParameterActiveProfile {
			t.Errorf("profile=%v: param=%v, want ACTIVE_PROFILE", tc.profile, got.param)
			continue
		}
		v, ok := got.value.(int32)
		if !ok || v != tc.wantIdx {
			t.Errorf("profile=%v: value=%#v, want %d", tc.profile, got.value, tc.wantIdx)
		}
	}
}

// TestSetProfileWeekProgramRFMapsToEnumLabel verifies that on KindRF the
// week-program profile writes WEEK_PROGRAM_POINTER with the CCU's
// ENUM-string label ("WEEK PROGRAM N", 1-based).
func TestSetProfileWeekProgramRFMapsToEnumLabel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		profile   Profile
		wantLabel string
	}{
		{ProfileWeekProgram1, "WEEK PROGRAM 1"},
		{ProfileWeekProgram2, "WEEK PROGRAM 2"},
		{ProfileWeekProgram3, "WEEK PROGRAM 3"},
	}
	for _, tc := range cases {
		w := &stubWriter{}
		r := newRig(t, "x", KindRF, w, custom.ClimateCapabilities{SupportsProfile: true})
		if err := r.climate.SetProfile(context.Background(), tc.profile, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("profile=%v: %v", tc.profile, err)
		}
		got := w.last()
		if got.param != hmenum.ParameterWeekProgramPointer {
			t.Errorf("profile=%v: param=%v, want WEEK_PROGRAM_POINTER", tc.profile, got.param)
			continue
		}
		s, ok := got.value.(string)
		if !ok || s != tc.wantLabel {
			t.Errorf("profile=%v: value=%#v, want %q", tc.profile, got.value, tc.wantLabel)
		}
	}
}

// TestSetProfileSpecialProfilesWriteTheirModeParameter verifies that the
// three profiles [Climate.Profiles] advertises alongside the week programs
// reach their dedicated mode parameter.
//
// They carry no week-program pointer, so requiring one rejected them before
// the write ever happened — every "boost" / "comfort" / "eco" preset an
// operator selected failed, on a device that advertised all three.
func TestSetProfileSpecialProfilesWriteTheirModeParameter(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		kind      Kind
		caps      custom.ClimateCapabilities
		profile   Profile
		wantParam hmenum.Parameter
	}{
		{
			name:      "boost on RF",
			kind:      KindRF,
			caps:      custom.ClimateCapabilities{SupportsProfile: true, SupportsBoost: true},
			profile:   ProfileBoost,
			wantParam: hmenum.ParameterBoostMode,
		},
		{
			name:      "boost on IP",
			kind:      KindIP,
			caps:      custom.ClimateCapabilities{SupportsProfile: true, SupportsBoost: true},
			profile:   ProfileBoost,
			wantParam: hmenum.ParameterBoostMode,
		},
		{
			name:      "comfort on RF",
			kind:      KindRF,
			caps:      custom.ClimateCapabilities{SupportsProfile: true, SupportsComfort: true},
			profile:   ProfileComfort,
			wantParam: hmenum.ParameterComfortMode,
		},
		{
			name:      "eco on RF",
			kind:      KindRF,
			caps:      custom.ClimateCapabilities{SupportsProfile: true, SupportsEco: true},
			profile:   ProfileEco,
			wantParam: hmenum.ParameterLoweringMode,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := &stubWriter{}
			r := newRig(t, "x", tc.kind, w, tc.caps)
			if !slices.Contains(r.climate.Profiles(), tc.profile) {
				t.Fatalf("Profiles() = %v, must advertise %v for this capability set", r.climate.Profiles(), tc.profile)
			}
			if err := r.climate.SetProfile(context.Background(), tc.profile, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("SetProfile(%v): %v", tc.profile, err)
			}
			got := w.last()
			if got.param != tc.wantParam {
				t.Fatalf("SetProfile(%v) wrote param=%v, want %v", tc.profile, got.param, tc.wantParam)
			}
			if on, ok := got.value.(bool); !ok || !on {
				t.Fatalf("SetProfile(%v) wrote %v=%#v, want true", tc.profile, tc.wantParam, got.value)
			}
			if p, ok := r.climate.Profile(); !ok || p != tc.profile {
				t.Errorf("Profile() = (%v, %v), want (%v, true)", p, ok, tc.profile)
			}
		})
	}
}

// TestSetProfileSpecialProfileWithoutCapabilityRejected verifies that a
// profile the device does not advertise is refused rather than written.
func TestSetProfileSpecialProfileWithoutCapabilityRejected(t *testing.T) {
	t.Parallel()

	for _, p := range []Profile{ProfileBoost, ProfileComfort, ProfileEco} {
		w := &stubWriter{}
		r := newRig(t, "x", KindRF, w, custom.ClimateCapabilities{SupportsProfile: true})
		if err := r.climate.SetProfile(context.Background(), p, hmenum.CommandPriorityHigh); !errors.Is(err, ErrModeNotSupported) {
			t.Errorf("SetProfile(%v) without capability: err=%v, want ErrModeNotSupported", p, err)
		}
		if len(w.calls) != 0 {
			t.Errorf("SetProfile(%v) without capability wrote %+v", p, w.calls)
		}
	}
}

// TestSetProfileNonWeekRejected verifies that non-week profiles
// (e.g. Away) are rejected.
func TestSetProfileNonWeekRejected(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{SupportsProfile: true})
	if err := r.climate.SetProfile(context.Background(), ProfileAway, hmenum.CommandPriorityHigh); err == nil {
		t.Error("SetProfile(Away) must be rejected")
	}
}

// TestSetAwayIPAtomicPutParamset verifies that SetAway bundles
// PARTY_TIME_START + PARTY_TIME_END + SET_POINT_MODE + PARTY_TEMPERATURE
// into one atomic put_paramset.
func TestSetAwayIPAtomicPutParamset(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	r := newRig(t, "VCU0000050:4", KindIP, w, custom.ClimateCapabilities{
		MinTemperature: 4.5, MaxTemperature: 30.5, SupportsAway: true,
	})
	end := time.Now().Add(2 * time.Hour)
	if err := r.climate.SetAway(context.Background(), end, 17.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d (sets=%d)", len(w.puts), len(w.calls))
	}
	got := w.puts[0]
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterPartyTimeStart,
		hmenum.ParameterPartyTimeEnd,
		hmenum.ParameterSetPointMode,
		hmenum.ParameterSetPointTemperature,
	} {
		if _, ok := got[string(p)]; !ok {
			t.Errorf("missing %s in atomic away batch", p)
		}
	}
}

// TestBoostGatedOnCapabilityWithoutCap verifies that EnableBoost returns
// ErrModeNotSupported when the capability is absent.
func TestBoostGatedOnCapabilityWithoutCap(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	if err := r.climate.EnableBoost(context.Background(), hmenum.CommandPriorityHigh); !errors.Is(err, ErrModeNotSupported) {
		t.Fatalf("got %v, want ErrModeNotSupported", err)
	}
}

// TestBoostSetsProfileOptimistically verifies that SetBoost(true) optimistically
// sets the profile to ProfileBoost.
func TestBoostSetsProfileOptimistically(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{SupportsBoost: true})
	if err := r.climate.SetBoost(context.Background(), true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if p, ok := r.climate.Profile(); !ok || p != ProfileBoost {
		t.Errorf("profile after boost activation=(%v, %v), want (boost, true)", p, ok)
	}
}

// TestIngestionUpdatesAllAccessors verifies that CCU events update all
// accessors.
func TestIngestionUpdatesAllAccessors(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	r.actualTemperature.OnEvent(22.3)
	r.setpoint.OnEvent(21.0)
	r.humidity.OnEvent(45.0)
	r.climate.OnMode(ModeAuto)
	r.climate.OnProfile(ProfileWeekProgram2)

	if v, ok := r.climate.CurrentTemperature(); !ok || v != 22.3 {
		t.Errorf("CurrentTemperature=(%v, %v), want (22.3, true)", v, ok)
	}
	if v, ok := r.climate.Setpoint(); !ok || v != 21.0 {
		t.Errorf("Setpoint=(%v, %v), want (21.0, true)", v, ok)
	}
	if v, ok := r.climate.Humidity(); !ok || v != 45.0 {
		t.Errorf("Humidity=(%v, %v), want (45.0, true)", v, ok)
	}
	if m, ok := r.climate.Mode(); !ok || m != ModeAuto {
		t.Errorf("Mode=(%v, %v), want (auto, true)", m, ok)
	}
	if p, ok := r.climate.Profile(); !ok || p != ProfileWeekProgram2 {
		t.Errorf("Profile=(%v, %v), want (week_program_2, true)", p, ok)
	}
}

// TestClimateMinMaxTemperatureEnforcement verifies that SetTemperature rejects
// out-of-range values with ErrTemperatureOutOfRange and accepts in-range values.
func TestClimateMinMaxTemperatureEnforcement(t *testing.T) {
	t.Parallel()

	t.Run("in-range succeeds", func(t *testing.T) {
		t.Parallel()
		w := &stubWriter{}
		r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
		if err := r.climate.SetTemperature(context.Background(), 5.0, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("in-range SetTemperature(5.0): %v", err)
		}
	})
	t.Run("above max rejected", func(t *testing.T) {
		t.Parallel()
		w := &stubWriter{}
		r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
		err := r.climate.SetTemperature(context.Background(), 35.0, hmenum.CommandPriorityHigh)
		if !errors.Is(err, ErrTemperatureOutOfRange) {
			t.Errorf("SetTemperature(35.0): got %v, want ErrTemperatureOutOfRange", err)
		}
	})
	t.Run("below min rejected", func(t *testing.T) {
		t.Parallel()
		w := &stubWriter{}
		r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
		err := r.climate.SetTemperature(context.Background(), 1.0, hmenum.CommandPriorityHigh)
		if !errors.Is(err, ErrTemperatureOutOfRange) {
			t.Errorf("SetTemperature(1.0): got %v, want ErrTemperatureOutOfRange", err)
		}
	})
}

// TestClimateIsStateChangeMode verifies that IsStateChange returns true for a
// mode that differs from the observed current mode, and false for the same.
func TestClimateIsStateChangeMode(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnMode(ModeHeat)

	// Different mode → state change.
	modeAuto := ModeAuto
	if !r.climate.IsStateChange(nil, &modeAuto, nil) {
		t.Error("IsStateChange(mode=Auto) when current=Heat must return true")
	}
	// Same mode → no state change.
	modeHeat := ModeHeat
	if r.climate.IsStateChange(nil, &modeHeat, nil) {
		t.Error("IsStateChange(mode=Heat) when current=Heat must return false")
	}
}

// TestClimateIsStateChangeProfile verifies that IsStateChange returns true for
// a profile that differs from the observed current profile, and false for the
// same.
func TestClimateIsStateChangeProfile(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindRF, &stubWriter{}, custom.ClimateCapabilities{})
	r.climate.OnProfile(ProfileNone)

	// Different profile → state change.
	pComfort := ProfileComfort
	if !r.climate.IsStateChange(nil, nil, &pComfort) {
		t.Error("IsStateChange(profile=Comfort) when current=None must return true")
	}
	// Same profile → no state change.
	pNone := ProfileNone
	if r.climate.IsStateChange(nil, nil, &pNone) {
		t.Error("IsStateChange(profile=None) when current=None must return false")
	}
}

// TestClimateIsStateChangeTemperature verifies that IsStateChange returns true
// when the proposed temperature differs from the observed setpoint, and false
// when it matches.
func TestClimateIsStateChangeTemperature(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		current float64
		propose float64
		want    bool
	}{
		{"different_temperature", 20.0, 22.0, true},
		{"same_temperature", 20.0, 20.0, false},
		{"off_sentinel_different", 4.5, 22.0, true},
	}
	for _, tc := range cases {
		w := &stubWriter{}
		r := newRig(t, "x", KindIP, w, custom.ClimateCapabilities{MinTemperature: 4.5, MaxTemperature: 30.5})
		// Drive the setpoint to a known value.
		r.setpoint.OnEvent(tc.current)

		got := r.climate.IsStateChange(&tc.propose, nil, nil)
		if got != tc.want {
			t.Errorf("%s: IsStateChange(temp=%v, current=%v)=%v, want %v",
				tc.name, tc.propose, tc.current, got, tc.want)
		}
	}
}

// TestClimateIsStateChangeNoCurrentValue verifies that IsStateChange returns
// true when no current value has been observed — the first command always
// goes through.
func TestClimateIsStateChangeNoCurrentValue(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})
	// No OnMode / OnProfile / setpoint event fired — all (_, false).

	modeAuto := ModeAuto
	if !r.climate.IsStateChange(nil, &modeAuto, nil) {
		t.Error("IsStateChange with unobserved mode must return true")
	}
	pBoost := ProfileBoost
	if !r.climate.IsStateChange(nil, nil, &pBoost) {
		t.Error("IsStateChange with unobserved profile must return true")
	}
	temp := 21.0
	if !r.climate.IsStateChange(&temp, nil, nil) {
		t.Error("IsStateChange with unobserved setpoint must return true")
	}
}

// TestOptimumStartStop verifies that OptimumStartStop returns (false, false)
// before any event and reflects the last update via OnOptimumStartStop.
func TestOptimumStartStop(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{})

	// Before any event: not observed.
	if v, ok := r.climate.OptimumStartStop(); ok || v {
		t.Errorf("OptimumStartStop() before event = (%v, %v), want (false, false)", v, ok)
	}

	// After a true update.
	r.climate.OnOptimumStartStop(true)
	if v, ok := r.climate.OptimumStartStop(); !ok || !v {
		t.Errorf("OptimumStartStop() after true = (%v, %v), want (true, true)", v, ok)
	}

	// After a false update.
	r.climate.OnOptimumStartStop(false)
	if v, ok := r.climate.OptimumStartStop(); !ok || v {
		t.Errorf("OptimumStartStop() after false = (%v, %v), want (false, true)", v, ok)
	}
}

// TestScheduleProfileNos verifies that ScheduleProfileNos returns 0
// when profiles are not supported and a positive count otherwise.
func TestScheduleProfileNos(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		supportsProfile bool
		wantPositive    bool
	}{
		{"no_profiles", false, false},
		{"ip_with_profiles", true, true},
	}
	for _, tc := range cases {
		r := newRig(t, "x", KindIP, &stubWriter{}, custom.ClimateCapabilities{SupportsProfile: tc.supportsProfile})
		n := r.climate.ScheduleProfileNos()
		if tc.wantPositive && n <= 0 {
			t.Errorf("%s: ScheduleProfileNos()=%d, want >0", tc.name, n)
		}
		if !tc.wantPositive && n != 0 {
			t.Errorf("%s: ScheduleProfileNos()=%d, want 0", tc.name, n)
		}
	}
}

// TestTemperatureOffset verifies that TemperatureOffset returns (0, false)
// before any event and reflects the last update via OnTemperatureOffset.
func TestTemperatureOffset(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		kind Kind
	}{
		{"KindIP", KindIP},
		{"KindRF", KindRF},
	}
	for _, tc := range cases {
		r := newRig(t, "x", tc.kind, &stubWriter{}, custom.ClimateCapabilities{})

		// Before any event: not observed.
		if v, ok := r.climate.TemperatureOffset(); ok {
			t.Errorf("%s: TemperatureOffset() before event = (%v, %v), want (_, false)", tc.name, v, ok)
		}

		// After an event.
		r.climate.OnTemperatureOffset(0.5)
		v, ok := r.climate.TemperatureOffset()
		if !ok {
			t.Errorf("%s: TemperatureOffset() after event: not observed", tc.name)
		}
		if v != "0.5" {
			t.Errorf("%s: TemperatureOffset()=%v, want \"0.5\"", tc.name, v)
		}
	}
}

// TestPartyModeCodeFormat verifies that the PARTY_MODE_SUBMIT string written
// by SetAway on KindRF carries the correct CSV format:
// "setpoint,start_mod,dd,mm,yy,end_mod,dd,mm,yy"
func TestPartyModeCodeFormat(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", KindRF, w, custom.ClimateCapabilities{
		MinTemperature: 4.5, MaxTemperature: 30.5, SupportsAway: true,
	})

	// Fixed end time to produce deterministic output.
	end := time.Date(2025, 1, 16, 14, 45, 0, 0, time.UTC)
	if err := r.climate.SetAway(context.Background(), end, 18.5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}

	got := w.last()
	if got.param != hmenum.ParameterPartyModeSubmit {
		t.Fatalf("SetAway(KindRF) wrote param=%v, want PARTY_MODE_SUBMIT", got.param)
	}
	s, ok := got.value.(string)
	if !ok {
		t.Fatalf("SetAway(KindRF) value type=%T, want string", got.value)
	}

	// Format: "setpoint,start_mod,start_dd,start_mm,start_yy,end_mod,end_dd,end_mm,end_yy"
	parts := strings.Split(s, ",")
	if len(parts) != 9 {
		t.Fatalf("PARTY_MODE_SUBMIT=%q: expected 9 comma-separated fields, got %d", s, len(parts))
	}
	if parts[0] != fmt.Sprintf("%.1f", 18.5) {
		t.Errorf("PARTY_MODE_SUBMIT temperature field=%q, want %q", parts[0], fmt.Sprintf("%.1f", 18.5))
	}
	// End segment is fields [5..8]: end_mod,end_dd,end_mm,end_yy
	// end = 2025-01-16 14:45 → end_mod = 14*60+45 = 885, dd=16, mm=01, yy=25
	if parts[5] != "885" {
		t.Errorf("PARTY_MODE_SUBMIT end_mod=%q, want %q", parts[5], "885")
	}
	if parts[6] != "16" {
		t.Errorf("PARTY_MODE_SUBMIT end_dd=%q, want %q", parts[6], "16")
	}
	if parts[7] != "01" {
		t.Errorf("PARTY_MODE_SUBMIT end_mm=%q, want %q", parts[7], "01")
	}
	if parts[8] != "25" {
		t.Errorf("PARTY_MODE_SUBMIT end_yy=%q, want %q", parts[8], "25")
	}
}
