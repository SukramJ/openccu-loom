// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestPlaySoundRefusesARepetitionsIndexOutsideTheRange pins that a
// repetitions index PlaySound cannot write is an error, not a silent skip.
// A dropped REPETITIONS leaves the device's previous setting in place, so
// the player plays with a repeat count nobody asked for while the call
// reports success. Home Assistant's play_sound offers indices the device
// has no slot for, so these values do reach the daemon.
func TestPlaySoundRefusesARepetitionsIndexOutsideTheRange(t *testing.T) {
	t.Parallel()

	for _, idx := range []int{maxRepetitionsIndex + 1, 18, -3} {
		sp, rec := newSoundPlayerRig(t)
		err := sp.PlaySound(context.Background(),
			PlayConfig{SoundfileIndex: 1, RepetitionsIndex: idx},
			hmenum.CommandPriorityHigh)
		if !errors.Is(err, ErrInvalidRepetitionsIndex) {
			t.Errorf("PlaySound(repetitions_index=%d) returned %v, want %v", idx, err, ErrInvalidRepetitionsIndex)
		}
		if n := len(rec.calls); n != 0 {
			t.Errorf("PlaySound(repetitions_index=%d) issued %d writes (%v); a refused play must not reach the wire", idx, n, rec.calls)
		}
	}
}

// TestPlaySoundRefusesARepetitionsIndexTheDeviceHasNoSlotFor covers the
// in-range index the device's own REPETITIONS list cannot hold. The rig's
// list is NO_REP, REPETITIONS_5, INFINITE: index 2 has no finite slot.
func TestPlaySoundRefusesARepetitionsIndexTheDeviceHasNoSlotFor(t *testing.T) {
	t.Parallel()

	sp, rec := newSoundPlayerRig(t)
	err := sp.PlaySound(context.Background(),
		PlayConfig{SoundfileIndex: 1, RepetitionsIndex: 2},
		hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrInvalidRepetitionsIndex) {
		t.Errorf("PlaySound(repetitions_index=2) returned %v, want %v", err, ErrInvalidRepetitionsIndex)
	}
	if n := len(rec.calls); n != 0 {
		t.Errorf("PlaySound(repetitions_index=2) issued %d writes (%v); a refused play must not reach the wire", n, rec.calls)
	}
}

// TestPlaySoundStillWritesEveryRepetitionsIndexItCanTake is the other
// direction, so the refusals above cannot be satisfied by refusing every
// index: the not-set sentinel plays without REPETITIONS, and -1, 0 and a
// slot the device offers reach the wire.
func TestPlaySoundStillWritesEveryRepetitionsIndexItCanTake(t *testing.T) {
	t.Parallel()

	cases := []struct {
		idx       int
		wantLabel string // empty: REPETITIONS must not be written
	}{
		{RepetitionsIndexNotSet, ""},
		{-1, "INFINITE"},
		{0, "NO_REP"},
		{1, "REPETITIONS_5"},
	}
	for _, tc := range cases {
		sp, rec := newSoundPlayerRig(t)
		if err := sp.PlaySound(context.Background(),
			PlayConfig{SoundfileIndex: 1, RepetitionsIndex: tc.idx},
			hmenum.CommandPriorityHigh); err != nil {
			t.Errorf("PlaySound(repetitions_index=%d): %v", tc.idx, err)
			continue
		}
		got, ok := rec.has(hmenum.ParameterRepetitions)
		switch {
		case tc.wantLabel == "" && ok:
			t.Errorf("PlaySound(repetitions_index=%d) wrote REPETITIONS=%v, want no write", tc.idx, got)
		case tc.wantLabel != "" && got != tc.wantLabel:
			t.Errorf("PlaySound(repetitions_index=%d) wrote REPETITIONS=%v (present=%v), want %s", tc.idx, got, ok, tc.wantLabel)
		}
		if _, ok := rec.has(hmenum.ParameterLevel); !ok {
			t.Errorf("PlaySound(repetitions_index=%d) did not write LEVEL; the play never reached the wire", tc.idx)
		}
	}
}

// TestPlaySoundServiceRefusesARepetitionsIndexOutsideTheRange drives the
// same refusal through the play service, the path Home Assistant's
// play_sound takes.
func TestPlaySoundServiceRefusesARepetitionsIndexOutsideTheRange(t *testing.T) {
	t.Parallel()

	sp, rec := newSoundPlayerRig(t)
	err := sp.Invoke(context.Background(), "turn_on",
		map[string]any{"soundfile_index": 1, "repetitions_index": 15},
		hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrInvalidRepetitionsIndex) {
		t.Errorf("turn_on with repetitions_index=15 returned %v, want %v", err, ErrInvalidRepetitionsIndex)
	}
	if n := len(rec.calls); n != 0 {
		t.Errorf("turn_on with repetitions_index=15 issued %d writes (%v); a refused play must not reach the wire", n, rec.calls)
	}
}
