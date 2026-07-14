// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestChirp_ArmSquawkPlaysToneOnChirpOutputOnly covers S5 case 18:
// an arm squawk plays the configured confirmation tone at low
// priority, bounded to the fixed chirp duration, and only on
// chirp-class outputs.
func TestChirp_ArmSquawkPlaysToneOnChirpOutputOnly(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()

	if err := h.mgr.Chirp(h.ctx, "eg", engine.ChirpRequest{Kind: engine.ChirpArmSquawk}); err != nil {
		t.Fatalf("Chirp: %v", err)
	}

	calls := h.siren("chirp").turnOnCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("chirp TurnOn calls = %d, want 1", len(calls))
	}
	call := calls[0]
	if call.Cfg.Duration != time.Second {
		t.Fatalf("chirp duration = %v, want 1s", call.Cfg.Duration)
	}
	if call.Cfg.AcousticTone != "EXTERNALLY_ARMED" {
		t.Fatalf("chirp tone = %q, want EXTERNALLY_ARMED", call.Cfg.AcousticTone)
	}
	if call.Priority != hmenum.CommandPriorityLow {
		t.Fatalf("chirp priority = %v, want Low", call.Priority)
	}
	if n := h.siren("sirA").turnOnCount(); n != 0 {
		t.Fatalf("sirA TurnOn calls = %d, want 0 (arm squawk targets chirp outputs only)", n)
	}
}

// TestChirp_RateLimitDropsSecondEmissionWithinGap covers S5 case 19:
// a second chirp on the same output within the minimum gap is
// dropped.
func TestChirp_RateLimitDropsSecondEmissionWithinGap(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()

	req := engine.ChirpRequest{Kind: engine.ChirpArmSquawk}
	if err := h.mgr.Chirp(h.ctx, "eg", req); err != nil {
		t.Fatalf("Chirp #1: %v", err)
	}
	if err := h.mgr.Chirp(h.ctx, "eg", req); err != nil {
		t.Fatalf("Chirp #2: %v", err)
	}

	if n := h.siren("chirp").turnOnCount(); n != 1 {
		t.Fatalf("chirp TurnOn calls = %d, want 1 (second emission within the gap is dropped)", n)
	}
}

// TestChirp_CountdownTickPatternThinsAsRemainingShrinks covers S5
// case 20: the countdown-tick pattern is due on multiples of ten
// above ten seconds remaining, then on even seconds within the final
// ten, and dropped otherwise.
func TestChirp_CountdownTickPatternThinsAsRemainingShrinks(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()
	tick := func(remaining time.Duration) {
		t.Helper()
		if err := h.mgr.Chirp(h.ctx, "eg", engine.ChirpRequest{Kind: engine.ChirpCountdownTick, Remaining: remaining}); err != nil {
			t.Fatalf("Chirp(remaining=%v): %v", remaining, err)
		}
	}

	tick(30 * time.Second) // due: multiple of ten
	if n := h.siren("chirp").turnOnCount(); n != 1 {
		t.Fatalf("after 30s tick: TurnOn calls = %d, want 1", n)
	}

	h.advance(3 * time.Second)
	tick(27 * time.Second) // dropped: not a multiple of ten
	if n := h.siren("chirp").turnOnCount(); n != 1 {
		t.Fatalf("after 27s tick: TurnOn calls = %d, want 1 (dropped)", n)
	}

	h.advance(3 * time.Second)
	tick(8 * time.Second) // due: within final ten, even second
	if n := h.siren("chirp").turnOnCount(); n != 2 {
		t.Fatalf("after 8s tick: TurnOn calls = %d, want 2", n)
	}

	h.advance(1 * time.Second)
	tick(7 * time.Second) // dropped: odd second
	if n := h.siren("chirp").turnOnCount(); n != 2 {
		t.Fatalf("after 7s tick: TurnOn calls = %d, want 2 (dropped)", n)
	}
}

// TestChirp_SuppressedWhileAreaActivationInFlight covers S5 case 21:
// any chirp for an area with a pending alarm activation is dropped —
// chirp radio budget never competes with a stop.
func TestChirp_SuppressedWhileAreaActivationInFlight(t *testing.T) {
	h := newHarness(t)
	h.seedStandardArea()

	opts := engine.FireOptions{Policy: engine.OutputPolicy{ExcludeOutdoor: true}}
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(21, hmenum.AlarmModeFull), opts); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	if err := h.mgr.Chirp(h.ctx, "eg", engine.ChirpRequest{Kind: engine.ChirpArmSquawk}); err != nil {
		t.Fatalf("Chirp: %v", err)
	}
	if n := h.siren("chirp").turnOnCount(); n != 0 {
		t.Fatalf("chirp TurnOn calls = %d, want 0 while an activation is in flight", n)
	}
}

// TestChirp_MP3OutputPlaysSoundfileInsteadOfSiren covers S5 case 22:
// a chirp output configured with a soundfile index plays the MP3
// chirp instead of writing a siren tone.
func TestChirp_MP3OutputPlaysSoundfileInsteadOfSiren(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(outputRow("chirpMp3", hmenum.AlarmOutputClassChirp, OutputConfig{SoundfileIndex: 5, Volume: ptrFloat64(0.7)}))

	if err := h.mgr.Chirp(h.ctx, "eg", engine.ChirpRequest{Kind: engine.ChirpArmSquawk}); err != nil {
		t.Fatalf("Chirp: %v", err)
	}

	calls := h.sound("chirpMp3").playCallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("PlayChirp calls = %d, want 1", len(calls))
	}
	if calls[0].Index != 5 {
		t.Fatalf("soundfile index = %d, want 5", calls[0].Index)
	}
	if calls[0].Volume != 0.7 {
		t.Fatalf("volume = %v, want 0.7", calls[0].Volume)
	}
	if calls[0].Priority != hmenum.CommandPriorityLow {
		t.Fatalf("priority = %v, want Low", calls[0].Priority)
	}
	if n := h.siren("chirpMp3").turnOnCount(); n != 0 {
		t.Fatalf("siren TurnOn calls = %d, want 0 (MP3 path must not also write the siren)", n)
	}
}
