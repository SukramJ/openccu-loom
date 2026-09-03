// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import "testing"

// asirOpticalValueList is the OPTICAL_ALARM_SELECTION list an HmIP-ASIR
// declares. The four repeating patterns are the alarm patterns; the three
// confirmation signals are one-shot acknowledgement blinks, and
// DISABLE_OPTICAL_SIGNAL turns the light off.
var asirOpticalValueList = []string{
	"DISABLE_OPTICAL_SIGNAL",
	"BLINKING_ALTERNATELY_REPEATING",
	"BLINKING_BOTH_REPEATING",
	"DOUBLE_FLASHING_REPEATING",
	"FLASHING_BOTH_REPEATING",
	"CONFIRMATION_SIGNAL_0",
	"CONFIRMATION_SIGNAL_1",
	"CONFIRMATION_SIGNAL_2",
}

// TestAlarmOpticalLabelPicksARepeatingPattern pins that the label an alarm
// activation falls back to is one the device sustains.
//
// The last entry of the list is CONFIRMATION_SIGNAL_2 — "long short short",
// an acknowledgement blink, not an alarm pattern. Choosing by position picks
// exactly that, and the device then reports OPTICAL_ALARM_ACTIVE while showing
// a confirmation blink, which satisfies the watchdog.
func TestAlarmOpticalLabelPicksARepeatingPattern(t *testing.T) {
	t.Parallel()

	s := &Siren{availableLights: asirOpticalValueList}
	got, ok := s.AlarmOpticalLabel()
	if !ok {
		t.Fatal("a device offering four repeating patterns must yield one")
	}
	if got == "CONFIRMATION_SIGNAL_2" {
		t.Fatal("chose the last list entry, which is a confirmation blink and not an alarm pattern")
	}
	if got != "BLINKING_ALTERNATELY_REPEATING" {
		t.Errorf("AlarmOpticalLabel() = %q, want the first repeating pattern", got)
	}
}

// TestAlarmOpticalLabelWithhoIdsWhenNoPatternRepeats pins that a device
// offering no repeating pattern yields nothing, so the caller leaves the
// device's own selection alone rather than writing an acknowledgement blink.
func TestAlarmOpticalLabelWithholdsWhenNoPatternRepeats(t *testing.T) {
	t.Parallel()

	for _, list := range [][]string{
		nil,
		{},
		{"DISABLE_OPTICAL_SIGNAL"},
		{"DISABLE_OPTICAL_SIGNAL", "CONFIRMATION_SIGNAL_0", "CONFIRMATION_SIGNAL_2"},
	} {
		s := &Siren{availableLights: list}
		if got, ok := s.AlarmOpticalLabel(); ok {
			t.Errorf("list %v: got %q, want no label", list, got)
		}
	}
}
