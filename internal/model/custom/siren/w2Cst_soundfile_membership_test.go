// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestW2CstSoundfileIndexIsCheckedAgainstTheDeviceList pins the two branches
// of PlaySound's soundfile switch to one rule.
//
// The label branch refuses a tone the device does not offer, because a
// dropped SOUNDFILE leaves the previous selection in place and the player
// then repeats that file while the call reports success — the reason
// [ErrUnknownSoundfile] exists at all. The index branch used to skip the
// check, so the same unplayable tone reached the wire whenever the caller
// spelled it as a number instead of a label. The alarm chirp output
// (internal/alarm/outputs/chirp.go) is a numeric caller.
func TestW2CstSoundfileIndexIsCheckedAgainstTheDeviceList(t *testing.T) {
	t.Parallel()

	// newSoundPlayerRig builds a device offering SOUNDFILE_001..003.
	sp, rec := newSoundPlayerRig(t)
	const absent = 5

	err := sp.PlaySound(context.Background(),
		PlayConfig{SoundfileIndex: absent, RepetitionsIndex: RepetitionsIndexNotSet},
		hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrUnknownSoundfile) {
		t.Errorf("PlaySound(index=%d) returned %v, want %v — the device offers %v", absent, err, ErrUnknownSoundfile, sp.AvailableSoundfiles())
	}
	if got, ok := rec.has(hmenum.ParameterSoundfile); ok {
		t.Errorf("PlaySound(index=%d) put %v on the wire — the device offers %v, so the player would repeat its previous file and report success",
			absent, got, sp.AvailableSoundfiles())
	}
}

// TestW2CstSoundfileIndexTheDeviceOffersStillReachesTheWire is the other
// direction, so the check above cannot be satisfied by refusing every index.
func TestW2CstSoundfileIndexTheDeviceOffersStillReachesTheWire(t *testing.T) {
	t.Parallel()

	sp, rec := newSoundPlayerRig(t)
	if err := sp.PlaySound(context.Background(),
		PlayConfig{SoundfileIndex: 2, RepetitionsIndex: RepetitionsIndexNotSet},
		hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("PlaySound(index=2): %v — the device offers %v", err, sp.AvailableSoundfiles())
	}
	if got, ok := rec.has(hmenum.ParameterSoundfile); !ok || got != "SOUNDFILE_002" {
		t.Errorf("PlaySound(index=2) wrote SOUNDFILE=%v (present=%v), want SOUNDFILE_002", got, ok)
	}
}
