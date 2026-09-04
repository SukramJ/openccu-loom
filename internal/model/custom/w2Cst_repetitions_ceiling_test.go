// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom_test

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
)

// w2CstRepetitionsValuesList is the REPETITIONS VALUE_LIST of the VALUES
// paramset — the paramset every writer in this repo stages the parameter
// into. Sixteen entries, the numbered run stopping at REPETITIONS_014.
//
// Sourced twice, not from a convention:
//
//   - HMIPServer de.eq3.cbcs.devicedescription.channelspecification.
//     stateparameter.GeneralStateParameterFactory#createRepetitionParameter
//     builds the parameter over Repetitions.getNames(16), and
//     ...channelspecification.Repetitions#getNames places NO_REPETITION at
//     index 0, INFINITE_REPETITIONS at the last index and
//     String.format("REPETITIONS_%03d", i) in between — so a 16-slot list
//     ends at REPETITIONS_014.
//   - Every device in the captured paramset-description corpus checked out
//     beside this repo that declares the parameter agrees: HmIP-MP3P
//     (channels :2, :6, :7, :8) and HmIP-WRCD (channel :3). Measured by
//     walking every JSON file in that corpus and collecting the
//     (len(VALUE_LIST), last REPETITIONS_* entry) pair of each VALUES-paramset
//     REPETITIONS parameter: five occurrences, all (16, REPETITIONS_014).
//
// The 256-slot list ending at REPETITIONS_254 exists only on the MASTER /
// LINK profile-repetition config parameters (POWERUP_/SHORT_/LONG_
// PROFILE_REPETITIONS), which no path in this repo writes.
var w2CstRepetitionsValuesList = []string{
	"NO_REPETITION",
	"REPETITIONS_001", "REPETITIONS_002", "REPETITIONS_003", "REPETITIONS_004",
	"REPETITIONS_005", "REPETITIONS_006", "REPETITIONS_007", "REPETITIONS_008",
	"REPETITIONS_009", "REPETITIONS_010", "REPETITIONS_011", "REPETITIONS_012",
	"REPETITIONS_013", "REPETITIONS_014",
	"INFINITE_REPETITIONS",
}

// TestW2CstRepetitionsLabelStaysInsideTheDeviceValueList ties
// [custom.MaxRepetitions] to the VALUE_LIST the firmware builds for the
// VALUES-paramset REPETITIONS parameter, in both directions.
//
// Upward: every count the label grammar accepts must produce a label the
// device offers. A ceiling above the list's own top makes
// [custom.RepetitionsLabel] hand out REPETITIONS_015..018 — labels no device
// carrying this parameter declares. The sound-player LED
// (internal/model/custom/light/sound_led.go) stages that result without a
// membership check, so an out-of-list label reaches the wire.
//
// Downward: the ceiling must not be so low that a label the device offers is
// unreachable, which is what keeps this from being satisfiable by lowering
// the constant to zero.
func TestW2CstRepetitionsLabelStaysInsideTheDeviceValueList(t *testing.T) {
	t.Parallel()

	for n := 1; n <= custom.MaxRepetitions; n++ {
		label, err := custom.RepetitionsLabel(n)
		if err != nil {
			t.Fatalf("RepetitionsLabel(%d) rejected a count below the ceiling MaxRepetitions=%d: %v", n, custom.MaxRepetitions, err)
		}
		if !slices.Contains(w2CstRepetitionsValuesList, label) {
			t.Errorf("RepetitionsLabel(%d) = %q, which the VALUES-paramset REPETITIONS list does not offer (it ends at %q) — MaxRepetitions=%d is above the firmware's ceiling",
				n, label, w2CstRepetitionsValuesList[len(w2CstRepetitionsValuesList)-2], custom.MaxRepetitions)
		}
	}

	// Every numbered entry the device offers must be reachable.
	for _, label := range w2CstRepetitionsValuesList {
		if label == custom.RepetitionsNone || label == custom.RepetitionsInfinite {
			continue
		}
		var found bool
		for n := 1; n <= custom.MaxRepetitions; n++ {
			got, err := custom.RepetitionsLabel(n)
			if err == nil && got == label {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no count in 1..%d produces %q, which the device offers — MaxRepetitions is below the firmware's ceiling", custom.MaxRepetitions, label)
		}
	}

	// The first count past the ceiling has to be rejected rather than
	// formatted, because there is no slot for it on the device.
	if label, err := custom.RepetitionsLabel(custom.MaxRepetitions + 1); err == nil {
		t.Errorf("RepetitionsLabel(%d) = %q with no error — one past the ceiling must be rejected", custom.MaxRepetitions+1, label)
	}
}
