// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package safety

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// hmDevExclusionReason is why a parameter sits in [excludedParameters].
// The three values are the ones the CCU's own parameter registration
// distinguishes, not a taxonomy of our own.
type hmDevExclusionReason string

const (
	// hmDevExclusionWritten: registered write-only (IOOperations.WRITE) —
	// the alarm engine or an operator writes it, so reading it back as a
	// cause feeds the domain's own output into its input.
	hmDevExclusionWritten hmDevExclusionReason = "written"
	// hmDevExclusionReported: registered read+event with no write bit —
	// device-reported feedback about an actuator's own state, which is not
	// evidence of a sensed condition.
	hmDevExclusionReported hmDevExclusionReason = "reported"
	// hmDevExclusionInert: not a parameter identifier on any CCU surface,
	// so the key can never match a lookup.
	hmDevExclusionInert hmDevExclusionReason = "inert"
)

// hmDevExclusionReasons is the reason recorded for every entry in
// [excludedParameters], transcribed from the CCU's parameter registration
// (see the comments on the map itself for the per-entry citation).
var hmDevExclusionReasons = map[hmenum.Parameter]hmDevExclusionReason{
	hmenum.ParameterAcousticAlarmActive:    hmDevExclusionReported,
	"OPTICAL_ALARM_ACTIVE":                 hmDevExclusionReported,
	"EMERGENCY_OPERATION":                  hmDevExclusionReported,
	hmenum.ParameterAcousticAlarmSelection: hmDevExclusionWritten,
	"OPTICAL_ALARM_SELECTION":              hmDevExclusionWritten,
	hmenum.ParameterSmokeDetectorCommand:   hmDevExclusionWritten,
	"INTRUSION_ALARM":                      hmDevExclusionInert,
}

// TestHmDevEveryExcludedParameterDeclaresItsReason pins that each exclusion
// carries a reason, and that the reason set stays the one the CCU's own
// registration distinguishes.
//
// The list's rationale is the admission test a future reader applies to a new
// entry, so it is the load-bearing part of a hand-maintained list. It used to
// read "every entry is a parameter the alarm engine or an operator *writes*",
// which is false for three of the seven — ACOUSTIC_ALARM_ACTIVE,
// OPTICAL_ALARM_ACTIVE and EMERGENCY_OPERATION are registered read+event with
// no write bit — and for a fourth, INTRUSION_ALARM, which is a value label
// rather than a parameter id. An entry admitted under that false test would
// exclude a genuine sensor reading from ever raising an alarm.
func TestHmDevEveryExcludedParameterDeclaresItsReason(t *testing.T) {
	for p := range excludedParameters {
		reason, ok := hmDevExclusionReasons[p]
		if !ok {
			t.Errorf("excludedParameters has %q with no recorded reason — an exclusion is admitted "+
				"either because the CCU registers the parameter write-only (written), because it is "+
				"device-reported feedback (reported), or because no such parameter id exists (inert). "+
				"Record which, with the registration it was read from.", p)
			continue
		}
		switch reason {
		case hmDevExclusionWritten, hmDevExclusionReported, hmDevExclusionInert:
		default:
			t.Errorf("%q: unknown exclusion reason %q", p, reason)
		}
	}
	for p := range hmDevExclusionReasons {
		if _, ok := excludedParameters[p]; !ok {
			t.Errorf("%q has a recorded exclusion reason but is no longer excluded", p)
		}
	}
}

// TestHmDevExcludedParametersAreNeverClassified pins the effect the list is
// there for: no excluded parameter yields a classification, whatever table
// would otherwise match.
func TestHmDevExcludedParametersAreNeverClassified(t *testing.T) {
	for p := range excludedParameters {
		for _, channelType := range []string{"", "ALARM_SWITCH_VIRTUAL_RECEIVER", "SMOKE_DETECTOR"} {
			if _, ok := Classify("", channelType, p); ok {
				t.Errorf("Classify(%q, %q) returned a classification — the parameter is excluded", channelType, p)
			}
		}
	}
}

// TestHmDevExcludedParametersAreBeltAndBraces pins the claim the map's
// comment makes about its own reach: no excluded parameter is in either
// classification table, so removing the exclusions would change no output
// today. When this fails, one exclusion has become load-bearing — that is a
// legitimate change, but the comment stops being true and has to say so.
func TestHmDevExcludedParametersAreBeltAndBraces(t *testing.T) {
	for p := range excludedParameters {
		if _, ok := byParameter[p]; ok {
			t.Errorf("%q is both excluded and in byParameter — the exclusion is now load-bearing", p)
		}
		for key := range byChannelAndParameter {
			if key.parameter == p {
				t.Errorf("%q is both excluded and in byChannelAndParameter[%q] — "+
					"the exclusion is now load-bearing", p, key.channelType)
			}
		}
	}
}
