// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// seedSoundOnlyChirp seeds one chirp row whose target is an MP3 sound
// player and no siren — the HmIP-MP3P enrollment. The standard
// registerDevice path registers both a siren and a sound fake for a
// chirp row, which hides every siren-port assumption on that class.
func (h *harness) seedSoundOnlyChirp(id string, cfg OutputConfig) *fakeSoundDevice {
	h.t.Helper()
	row := outputRow(id, hmenum.AlarmOutputClassChirp, cfg)
	dev := newFakeSoundDevice(h.seq)
	h.resolver.addSound(row.CentralName, row.ChannelAddress, dev)
	h.allRows = append(h.allRows, row)
	h.rows.set(h.allRows)
	if h.mgr == nil {
		h.build(nil)
	} else if err := h.mgr.Reload(h.ctx); err != nil {
		h.t.Fatalf("Reload: %v", err)
	}
	return dev
}

// stopCallsSnapshot returns a defensive copy of the recorded Stop calls.
func (s *fakeSoundDevice) stopCallsSnapshot() []priorityCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]priorityCall(nil), s.stopCalls...)
}

// TestStopAll_SoundPlayerChirpIsNotAPhantomFailure pins that silencing
// a zone which carries an MP3-player chirp output reports no failure:
// the sound player is not a siren, so stopping it through the siren
// port turned every silence/disarm into a permanent alarm-health
// degradation and dropped the zone's panel to unavailable (S7).
func TestStopAll_SoundPlayerChirpIsNotAPhantomFailure(t *testing.T) {
	h := newHarness(t)
	sound := h.seedSoundOnlyChirp("mp3", OutputConfig{SoundfileIndex: 3})

	if err := h.mgr.StopAll(h.ctx, "eg", 41); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	// The verify chain must not escalate either.
	h.advance(200 * time.Second)

	if h.journal.hasForOutput("output_stop_failed", "mp3") {
		t.Fatal("StopAll journaled an output_stop_failed fault for an MP3 chirp output")
	}
	if h.journal.hasForOutput("output_stop_unverified", "mp3") {
		t.Fatal("the verify chain escalated an MP3 chirp output to stop-unverified")
	}
	h.healthMu.Lock()
	calls := append([]healthCall(nil), h.healthCalls...)
	h.healthMu.Unlock()
	for _, c := range calls {
		if !c.Healthy {
			t.Fatalf("StopAll degraded alarm health for an MP3 chirp output: %q", c.Note)
		}
	}
	if n := len(sound.stopCallsSnapshot()); n != 1 {
		t.Fatalf("sound Stop calls = %d, want 1 (the stop must reach the sound port)", n)
	}
}

// TestTestFire_SoundPlayerChirpPlaysTheSoundfile pins that the manual
// test fire of an MP3-player chirp output plays its soundfile instead
// of failing on the siren port.
func TestTestFire_SoundPlayerChirpPlaysTheSoundfile(t *testing.T) {
	h := newHarness(t)
	sound := h.seedSoundOnlyChirp("mp3", OutputConfig{SoundfileIndex: 3, Volume: ptrFloat64(0.4)})

	if err := h.mgr.TestFire(h.ctx, "mp3", false); err != nil {
		t.Fatalf("TestFire: %v", err)
	}
	plays := sound.playCallsSnapshot()
	if len(plays) != 1 {
		t.Fatalf("sound PlayChirp calls = %d, want 1", len(plays))
	}
	if plays[0].Index != 3 || plays[0].Volume != 0.4 {
		t.Fatalf("PlayChirp(index=%d, volume=%v), want (3, 0.4)", plays[0].Index, plays[0].Volume)
	}
	if h.journal.hasForOutput("output_test_failed", "mp3") {
		t.Fatal("TestFire journaled an output_test_failed fault for an MP3 chirp output")
	}
	if !h.journal.hasForOutput("output_test_fired", "mp3") {
		t.Fatal("expected an output_test_fired journal entry for the MP3 chirp output")
	}
}

// smokeRowInZone builds a smoke-sounder row in zoneID that addresses
// the shared channel address.
func smokeRowInZone(id, zoneID, channelAddress string) sqlitestore.AlarmOutputRow {
	row := outputRow(id, hmenum.AlarmOutputClassSmokeSounder, OutputConfig{})
	row.ZoneID = zoneID
	row.ChannelAddress = channelAddress
	return row
}

// TestFireCycle_FailedSmokeFireReleasesTheSharedChannelDemand pins the
// shared-channel arbitration contract for the smoke path: the demand
// is the claim of an output that is actually sounding, so a failed
// activation write must not leave one behind. A leaked demand makes
// another zone's silence a no-op — the sounders stay latched on after
// the operator silenced them.
func TestFireCycle_FailedSmokeFireReleasesTheSharedChannelDemand(t *testing.T) {
	h := newHarness(t)
	const shared = "SMOKE001:1"
	h.seedOutputs(
		smokeRowInZone("smokeA", "zoneA", shared),
		smokeRowInZone("smokeB", "zoneB", shared),
	)
	dev := h.resolverSmoke(t, shared)

	opts := engine.FireOptions{Policy: engine.OutputPolicy{SmokeSounders: true}}
	dev.setTurnOnErr(errors.New("smoke activation failed"))
	if err := h.mgr.FireCycle(h.ctx, "zoneA", newIncident(51, hmenum.AlarmModeFull), opts); err == nil {
		t.Fatal("expected zone A's smoke fire to fail")
	}
	dev.setTurnOnErr(nil)
	if err := h.mgr.FireCycle(h.ctx, "zoneB", newIncident(52, hmenum.AlarmModeFull), opts); err != nil {
		t.Fatalf("zone B FireCycle: %v", err)
	}

	if err := h.mgr.StopAll(h.ctx, "zoneB", 52); err != nil {
		t.Fatalf("StopAll zone B: %v", err)
	}
	if n := dev.turnOffCount(); n != 1 {
		t.Fatalf("smoke TurnOff calls = %d, want 1 (zone A's failed fire must not block zone B's silence)", n)
	}
}

// resolverSmoke resolves the fake smoke device registered for a raw
// channel address (the harness helper keys by output id).
func (h *harness) resolverSmoke(t *testing.T, channelAddress string) *fakeSmokeDevice {
	t.Helper()
	dev, err := h.resolver.SmokeSounder(testCentral, channelAddress)
	if err != nil {
		t.Fatalf("smoke %s: %v", channelAddress, err)
	}
	fd, ok := dev.(*fakeSmokeDevice)
	if !ok {
		t.Fatalf("smoke %s: not a fake smoke device", channelAddress)
	}
	return fd
}
