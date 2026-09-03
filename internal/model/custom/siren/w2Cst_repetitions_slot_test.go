// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"testing"
)

// w2CstRepetitionsValuesList is the REPETITIONS VALUE_LIST of the VALUES
// paramset, the one PlaySound stages the parameter into. Sixteen entries with
// the numbered run stopping at REPETITIONS_014 — see the citation on
// [github.com/SukramJ/openccu-loom/internal/model/custom.MaxRepetitions].
// Verified against every device in the descriptor corpus that declares the
// parameter (HmIP-MP3P channels :2/:6/:7/:8 and HmIP-WRCD channel :3, all
// five 16 entries ending at REPETITIONS_014).
var w2CstRepetitionsValuesList = []string{
	"NO_REPETITION",
	"REPETITIONS_001", "REPETITIONS_002", "REPETITIONS_003", "REPETITIONS_004",
	"REPETITIONS_005", "REPETITIONS_006", "REPETITIONS_007", "REPETITIONS_008",
	"REPETITIONS_009", "REPETITIONS_010", "REPETITIONS_011", "REPETITIONS_012",
	"REPETITIONS_013", "REPETITIONS_014",
	"INFINITE_REPETITIONS",
}

// TestW2CstFiniteRepetitionsIndexNeverBecomesInfinite pins the one outcome a
// siren must never produce from a bounded request: an unbounded alarm.
//
// The last entry of the REPETITIONS list is INFINITE_REPETITIONS on every
// list the firmware builds, so any rule that resolves an out-of-range finite
// index onto the last slot turns "repeat 15 times" into "repeat forever" and
// reports success. The check drives every finite index the profile accepts,
// plus the four that used to be accepted above the ceiling, against the real
// device list.
func TestW2CstFiniteRepetitionsIndexNeverBecomesInfinite(t *testing.T) {
	t.Parallel()

	infinite := w2CstRepetitionsValuesList[len(w2CstRepetitionsValuesList)-1]

	for index := range 19 {
		label, err := ConvertPlayRepetitionsIndex(index, w2CstRepetitionsValuesList)
		if err != nil {
			continue // rejected outright, which is the safe outcome
		}
		if label == infinite {
			t.Errorf("ConvertPlayRepetitionsIndex(%d) = %q — a finite repetition count resolved to the unbounded entry; a siren asked to repeat %d times would sound until stopped",
				index, label, index)
		}
	}

	// -1 is the one index that must reach the unbounded entry.
	if label, err := ConvertPlayRepetitionsIndex(-1, w2CstRepetitionsValuesList); err != nil || label != infinite {
		t.Errorf("ConvertPlayRepetitionsIndex(-1) = %q, err=%v — the infinite sentinel must still resolve to %q", label, err, infinite)
	}
}

// TestW2CstRepetitionsIndexHasNoSlotIsAnError covers the same rule on a
// device advertising a shorter list than the firmware factory builds: an
// index with no repeat slot must be reported, not slid onto whatever the last
// entry happens to be.
func TestW2CstRepetitionsIndexHasNoSlotIsAnError(t *testing.T) {
	t.Parallel()

	short := []string{"NO_REPETITION", "REPETITIONS_001", "INFINITE_REPETITIONS"}
	for _, index := range []int{2, 3, 7} {
		if label, err := ConvertPlayRepetitionsIndex(index, short); err == nil {
			t.Errorf("ConvertPlayRepetitionsIndex(%d, %v) = %q with no error — only slot 1 is a repeat count in a 3-entry list", index, short, label)
		}
	}
	// The one repeat slot the short list does offer still resolves.
	if label, err := ConvertPlayRepetitionsIndex(1, short); err != nil || label != "REPETITIONS_001" {
		t.Errorf("ConvertPlayRepetitionsIndex(1, %v) = %q, err=%v — want REPETITIONS_001", short, label, err)
	}
}
