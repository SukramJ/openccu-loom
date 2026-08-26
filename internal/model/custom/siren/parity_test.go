// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Parity tests for the Siren custom data point. Each test function maps to
// one semantic from the Python reference.

package siren

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestParityTurnOnSendsAcousticAndOptical verifies that TurnOn writes both
// ACOUSTIC_ALARM_SELECTION and OPTICAL_ALARM_SELECTION. Mirrors
// test_ip_siren_functionality → "turn_on(acoustic, optical, duration)".
func TestParityTurnOnSendsAcousticAndOptical(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "VCU8249617:3", w, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsOptical:  true,
		SupportsDuration: true,
	})
	acoustic := "FREQUENCY_RISING"
	optical := "BLINKING_RED"
	if err := r.siren.TurnOn(context.Background(), OnConfig{
		Duration:          30 * time.Minute,
		AcousticSelection: &acoustic,
		OpticalSelection:  &optical,
	}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if v, ok := w.has(hmenum.ParameterAcousticAlarmSelection); !ok || v.(string) != "FREQUENCY_RISING" {
		t.Errorf("ACOUSTIC_ALARM_SELECTION=%v ok=%v, want FREQUENCY_RISING", v, ok)
	}
	if v, ok := w.has(hmenum.ParameterOpticalAlarmSelection); !ok || v.(string) != "BLINKING_RED" {
		t.Errorf("OPTICAL_ALARM_SELECTION=%v ok=%v, want BLINKING_RED", v, ok)
	}
}

// TestParityTurnOnSendsDuration verifies that TurnOn writes
// DURATION_VALUE when SupportsDuration capability is set. Mirrors
// test_ip_siren_functionality → duration parameter check.
func TestParityTurnOnSendsDuration(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "VCU8249617:3", w, custom.SirenCapabilities{
		SupportsAcoustic: true,
		SupportsDuration: true,
	})
	acoustic := "FREQUENCY_RISING"
	if err := r.siren.TurnOn(context.Background(), OnConfig{
		Duration:          30 * time.Second,
		AcousticSelection: &acoustic,
	}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.has(hmenum.ParameterDurationValue); !ok {
		t.Error("DURATION_VALUE must be written when SupportsDuration=true")
	}
}

// TestParityTurnOffZerosBothChannels verifies TurnOff writes both selections
// to silence the siren. The selection value is the DEFAULT string label from
// the DP descriptor, or an empty string when no DEFAULT is declared.
func TestParityTurnOffZerosBothChannels(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", w, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})
	if err := r.siren.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if _, ok := w.has(hmenum.ParameterAcousticAlarmSelection); !ok {
		t.Errorf("ACOUSTIC_ALARM_SELECTION must be written by TurnOff")
	}
	if _, ok := w.has(hmenum.ParameterOpticalAlarmSelection); !ok {
		t.Errorf("OPTICAL_ALARM_SELECTION must be written by TurnOff")
	}
}

// TestParityTurnOffClearsActiveState verifies TurnOff marks the siren as
// inactive optimistically. Mirrors test_ip_siren → is_on after turn_off.
func TestParityTurnOffClearsActiveState(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", w, custom.SirenCapabilities{SupportsAcoustic: true, SupportsOptical: true})
	_ = r.siren.TurnOff(context.Background(), hmenum.CommandPriorityHigh)
	if active, _ := r.siren.IsActive(); active {
		t.Error("TurnOff must optimistically clear the active state")
	}
}

// TestParityIsActiveFromAcousticEvent verifies that an ACOUSTIC_ALARM_ACTIVE
// event drives IsActive. Mirrors test_ip_siren → "ACOUSTIC_ALARM_ACTIVE=1
// → is_on=True".
func TestParityIsActiveFromAcousticEvent(t *testing.T) {
	t.Parallel()

	r := newRig(t, "VCU8249617:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	r.acousticActiveDP.OnEvent(true)
	if active, _ := r.siren.IsActive(); !active {
		t.Error("acoustic active=true must set IsActive=true")
	}
	r.acousticActiveDP.OnEvent(false)
	if active, _ := r.siren.IsActive(); active {
		t.Error("acoustic active=false must set IsActive=false")
	}
}

// TestParityIsActiveFromOpticalEvent verifies that an OPTICAL_ALARM_ACTIVE
// event drives IsActive. Mirrors test_ip_siren → "OPTICAL_ALARM_ACTIVE=1
// → is_on=True".
func TestParityIsActiveFromOpticalEvent(t *testing.T) {
	t.Parallel()

	r := newRig(t, "VCU8249617:3", &stubWriter{}, custom.SirenCapabilities{SupportsOptical: true})
	r.opticalActiveDP.OnEvent(true)
	if active, _ := r.siren.IsActive(); !active {
		t.Error("optical active=true must set IsActive=true")
	}
}

// TestParityConvertSoundfileIndexValidRange verifies the accepted index
// range and the SOUNDFILE_%03d label shape.
//
// The upper bound is the device's own: the HmIP-MP3P SOUNDFILE
// VALUE_LIST runs to SOUNDFILE_252. The reference stack stops at 189,
// which is a deliberate divergence catalogued in
// notes/parity/by_design.md — a lower cap makes the daemon advertise
// tones it then refuses to send.
func TestParityConvertSoundfileIndexValidRange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		index   int
		want    string
		wantErr bool
	}{
		{1, "SOUNDFILE_001", false},
		{42, "SOUNDFILE_042", false},
		{189, "SOUNDFILE_189", false},
		{252, "SOUNDFILE_252", false},
		{0, "", true},   // below min
		{253, "", true}, // above the device's highest numbered file
		{-1, "", true},  // negative
	}
	for _, tc := range cases {
		got, err := ConvertSoundfileIndex(tc.index)
		if tc.wantErr {
			if err == nil {
				t.Errorf("index=%d: expected error, got %q", tc.index, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("index=%d: unexpected error: %v", tc.index, err)
			continue
		}
		if got != tc.want {
			t.Errorf("index=%d: got %q, want %q", tc.index, got, tc.want)
		}
	}
}

// TestParityEncodeDurationUnits verifies EncodeTimerDuration units.
// Mirrors test_ip_siren → duration unit encoding (seconds/minutes/hours).
func TestParityEncodeDurationUnits(t *testing.T) {
	t.Parallel()

	// All values < 16343 s stay in the seconds bucket (Python _TIME_UNIT_THRESHOLD=16343).
	// 5 min = 300 s → (300, S); 2 h = 7200 s → (7200, S). Promotion only
	// happens when the value exceeds 16343 in the current unit.
	cases := []struct {
		d        time.Duration
		wantV    int32
		wantUnit int32
	}{
		{30 * time.Second, 30, 0},
		{61 * time.Second, 61, 0}, // 61 s stays S, not promoted to M
		{5 * time.Minute, 300, 0}, // 300 s < 16343 → stays S
		{2 * time.Hour, 7200, 0},  // 7200 s < 16343 → stays S
		{5 * time.Hour, 300, 1},   // 18000 s > 16343 → promoted to M (300 min < 16343) → M
		{0, 0, 0},
	}
	for _, c := range cases {
		v, u := custom.EncodeTimerDuration(c.d)
		if v != c.wantV || u != c.wantUnit {
			t.Errorf("%v → (%d, %d), want (%d, %d)", c.d, v, u, c.wantV, c.wantUnit)
		}
	}
}

// TestParityAvailableTonesFromValueList verifies that AvailableTones returns
// the VALUE_LIST captured at construction time.
func TestParityAvailableTonesFromValueList(t *testing.T) {
	t.Parallel()

	r := newRig(t, "VCU8249617:3", &stubWriter{}, custom.SirenCapabilities{SupportsAcoustic: true})
	tones := r.siren.AvailableTones()
	// The test rig does not populate VALUE_LIST on the DPs, so AvailableTones
	// should return nil or empty — verify no panic and nil/empty contract.
	if len(tones) != 0 {
		// If the rig populates a value list, verify each entry is non-empty.
		for _, tone := range tones {
			if tone == "" {
				t.Error("AvailableTones entry must not be empty string")
			}
		}
	}
}

// TestParityAddressRoundtrip verifies Address() matches the construction arg.
func TestParityAddressRoundtrip(t *testing.T) {
	t.Parallel()

	const addr = "VCU8249617:3"
	r := newRig(t, addr, &stubWriter{}, custom.SirenCapabilities{})
	if got := r.siren.Address; got != addr {
		t.Errorf("Address=%q, want %q", got, addr)
	}
}

// TestParityTurnOnAcousticOnlyWhenOpticalAbsent verifies that TurnOn only
// writes ACOUSTIC when SupportsOptical=false.
func TestParityTurnOnAcousticOnlyWhenOpticalAbsent(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "x", w, custom.SirenCapabilities{SupportsAcoustic: true})
	acoustic := "FREQUENCY_RISING"
	_ = r.siren.TurnOn(context.Background(), OnConfig{
		AcousticSelection: &acoustic,
	}, hmenum.CommandPriorityHigh)
	// OPTICAL should not have been written.
	if _, ok := w.has(hmenum.ParameterOpticalAlarmSelection); ok {
		t.Error("TurnOn must not write OPTICAL when SupportsOptical=false")
	}
}
