// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package outputs

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// sharedRow builds one output row enrolled under areaID against an
// explicit channelAddress, independent of the id+":1" convention
// outputRow uses. Two areas can enroll separate rows against the very
// same physical channel this way, which is exactly the shared-channel
// fixture these tests need.
func sharedRow(id, areaID string, class hmenum.AlarmOutputClass, channelAddress string, cfg OutputConfig) sqlitestore.AlarmOutputRow {
	b, err := json.Marshal(cfg)
	if err != nil {
		// Programmer error: OutputConfig always marshals.
		panic(err)
	}
	return sqlitestore.AlarmOutputRow{
		ID:             id,
		AreaID:         areaID,
		Class:          class,
		CentralName:    testCentral,
		ChannelAddress: channelAddress,
		Name:           id,
		ConfigJSON:     string(b),
	}
}

// sirenAt resolves the fake siren device registered at an explicit
// channel address (see sirenAt vs. harness.siren, which derives the
// address from the row id and cannot address a channel shared by two
// differently-named rows).
func sirenAt(t *testing.T, h *harness, channelAddress string) *fakeSirenDevice {
	t.Helper()
	dev, err := h.resolver.Siren(testCentral, channelAddress)
	if err != nil {
		t.Fatalf("siren at %s: %v", channelAddress, err)
	}
	fd, ok := dev.(*fakeSirenDevice)
	if !ok {
		t.Fatalf("siren at %s: not a fake siren device", channelAddress)
	}
	return fd
}

// actuatorAt resolves the fake actuator device registered at an
// explicit channel address (see sirenAt).
func actuatorAt(t *testing.T, h *harness, channelAddress string) *fakeActuator {
	t.Helper()
	dev, err := h.resolver.Actuator(testCentral, channelAddress)
	if err != nil {
		t.Fatalf("actuator at %s: %v", channelAddress, err)
	}
	fd, ok := dev.(*fakeActuator)
	if !ok {
		t.Fatalf("actuator at %s: not a fake actuator", channelAddress)
	}
	return fd
}

// TestSharedChannel_StopAllDoesNotSilenceOtherAreasActiveDemand covers
// the core arbitration invariant: two areas enrolled on the same
// physical siren channel each hold a demand after firing, so the
// first area's StopAll leaves the device sounding for the other area
// without journaling a fault, and only the second area's StopAll
// writes the OFF.
func TestSharedChannel_StopAllDoesNotSilenceOtherAreasActiveDemand(t *testing.T) {
	h := newHarness(t)
	rowA := sharedRow("sirA_areaA", "areaA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	rowB := sharedRow("sirA_areaB", "areaB", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	h.seedOutputs(rowA, rowB)
	dev := sirenAt(t, h, "shared:1")

	if err := h.mgr.FireCycle(h.ctx, "areaA", newIncident(201, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle areaA: %v", err)
	}
	if err := h.mgr.FireCycle(h.ctx, "areaB", newIncident(202, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle areaB: %v", err)
	}
	if n := dev.turnOnCount(); n != 2 {
		t.Fatalf("shared siren TurnOn calls = %d, want 2 (one per area)", n)
	}

	if err := h.mgr.StopAll(h.ctx, "areaA", 201); err != nil {
		t.Fatalf("StopAll areaA: %v", err)
	}
	if n := dev.turnOffCount(); n != 0 {
		t.Fatalf("shared siren TurnOff calls after areaA StopAll = %d, want 0 (device still on for areaB)", n)
	}
	if h.journal.hasForOutput("output_stop_failed", rowA.ID) {
		t.Fatal("areaA StopAll must not journal a fault when deferring to a shared demand")
	}
	if h.journal.hasForOutput("output_stop_unverified", rowA.ID) {
		t.Fatal("areaA StopAll must not journal an unverified-stop fault when deferring to a shared demand")
	}

	if err := h.mgr.StopAll(h.ctx, "areaB", 202); err != nil {
		t.Fatalf("StopAll areaB: %v", err)
	}
	if n := dev.turnOffCount(); n != 1 {
		t.Fatalf("shared siren TurnOff calls after areaB StopAll = %d, want 1 (last demanding area stops it)", n)
	}
	if p := dev.turnOffCallsSnapshot()[0].Priority; p != hmenum.CommandPriorityCritical {
		t.Fatalf("stop priority = %v, want Critical", p)
	}
}

// TestSharedChannel_WatchdogDefersStopWhileOtherAreaStillDemands
// covers the watchdog leg of the same invariant: areaA's own fire
// watchdog fires first (shorter duration) while areaB's demand is
// still outstanding, so the scheduled stop is deferred and its
// activation is cleared without a write; areaB's own watchdog later
// finds no foreign demand left and writes the OFF.
func TestSharedChannel_WatchdogDefersStopWhileOtherAreaStillDemands(t *testing.T) {
	h := newHarness(t)
	rowA := sharedRow("sirA_areaA", "areaA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 60})
	rowB := sharedRow("sirA_areaB", "areaB", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	h.seedOutputs(rowA, rowB)
	dev := sirenAt(t, h, "shared:1")

	if err := h.mgr.FireCycle(h.ctx, "areaA", newIncident(203, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle areaA: %v", err)
	}
	if err := h.mgr.FireCycle(h.ctx, "areaB", newIncident(204, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle areaB: %v", err)
	}

	// areaA's watchdog fires at 60s; areaB still demands the channel.
	h.advance(60 * time.Second)
	if n := dev.turnOffCount(); n != 0 {
		t.Fatalf("TurnOff calls at areaA's deadline = %d, want 0 (areaB still demands the channel)", n)
	}
	if h.journal.hasForOutput("output_stop_unverified", rowA.ID) {
		t.Fatal("a deferred watchdog stop must not escalate a fault for areaA")
	}
	if h.healthCallCount() != 0 {
		t.Fatalf("health callback count after the deferred stop = %d, want 0", h.healthCallCount())
	}

	// areaB's own watchdog fires at 120s; no foreign demand remains.
	h.advance(60 * time.Second)
	if n := dev.turnOffCount(); n != 1 {
		t.Fatalf("TurnOff calls at areaB's deadline = %d, want 1", n)
	}
	if p := dev.turnOffCallsSnapshot()[0].Priority; p != hmenum.CommandPriorityCritical {
		t.Fatalf("stop priority = %v, want Critical", p)
	}
}

// TestSharedChannel_OwnAreaMultipleRowsNeverBlockOwnStop covers a
// single area enrolling two rows against the same physical channel: a
// row's own-area sibling demand must never count as foreign, so
// StopAll for the area always writes the OFF for every row.
func TestSharedChannel_OwnAreaMultipleRowsNeverBlockOwnStop(t *testing.T) {
	h := newHarness(t)
	row1 := sharedRow("sirA_1", "areaA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	row2 := sharedRow("sirA_2", "areaA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	h.seedOutputs(row1, row2)
	dev := sirenAt(t, h, "shared:1")

	if err := h.mgr.FireCycle(h.ctx, "areaA", newIncident(205, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}
	if n := dev.turnOnCount(); n != 2 {
		t.Fatalf("shared siren TurnOn calls = %d, want 2 (one per enrolled row)", n)
	}

	if err := h.mgr.StopAll(h.ctx, "areaA", 205); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if n := dev.turnOffCount(); n != 2 {
		t.Fatalf("shared siren TurnOff calls = %d, want 2 (no own-area demand blocks the stop)", n)
	}
	for _, c := range dev.turnOffCallsSnapshot() {
		if c.Priority != hmenum.CommandPriorityCritical {
			t.Fatalf("stop priority = %v, want Critical", c.Priority)
		}
	}
}

// TestSharedChannel_AlarmLightStaysOnUntilLastAreaStops covers the
// steady-on alarm-light class, which has no watchdog: two areas share
// the same light, and only the second area's StopAll turns it off.
func TestSharedChannel_AlarmLightStaysOnUntilLastAreaStops(t *testing.T) {
	h := newHarness(t)
	rowA := sharedRow("light_areaA", "areaA", hmenum.AlarmOutputClassAlarmLight, "light:1", OutputConfig{})
	rowB := sharedRow("light_areaB", "areaB", hmenum.AlarmOutputClassAlarmLight, "light:1", OutputConfig{})
	h.seedOutputs(rowA, rowB)
	dev := actuatorAt(t, h, "light:1")

	if err := h.mgr.FireCycle(h.ctx, "areaA", newIncident(206, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle areaA: %v", err)
	}
	if err := h.mgr.FireCycle(h.ctx, "areaB", newIncident(207, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle areaB: %v", err)
	}
	if n := dev.steadyCallCount(); n != 2 {
		t.Fatalf("shared light TurnOnSteady calls = %d, want 2", n)
	}

	if err := h.mgr.StopAll(h.ctx, "areaA", 206); err != nil {
		t.Fatalf("StopAll areaA: %v", err)
	}
	if n := dev.turnOffCount(); n != 0 {
		t.Fatalf("shared light TurnOff calls after areaA StopAll = %d, want 0 (areaB still demands it)", n)
	}

	if err := h.mgr.StopAll(h.ctx, "areaB", 207); err != nil {
		t.Fatalf("StopAll areaB: %v", err)
	}
	if n := dev.turnOffCount(); n != 1 {
		t.Fatalf("shared light TurnOff calls after areaB StopAll = %d, want 1", n)
	}
}

// TestSharedChannel_ReloadPrunesStaleDemandSoStopProceeds covers the
// Reload safety net: when areaB's row disappears from the row source
// (output or area deleted mid-alarm), Reload prunes its stale demand
// so areaA's StopAll is no longer blocked.
func TestSharedChannel_ReloadPrunesStaleDemandSoStopProceeds(t *testing.T) {
	h := newHarness(t)
	rowA := sharedRow("sirA_areaA", "areaA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	rowB := sharedRow("sirA_areaB", "areaB", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	h.seedOutputs(rowA, rowB)
	dev := sirenAt(t, h, "shared:1")

	if err := h.mgr.FireCycle(h.ctx, "areaA", newIncident(208, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle areaA: %v", err)
	}
	if err := h.mgr.FireCycle(h.ctx, "areaB", newIncident(209, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle areaB: %v", err)
	}

	// areaB's output row is deleted from the row source.
	h.allRows = []sqlitestore.AlarmOutputRow{rowA}
	h.rows.set(h.allRows)
	if err := h.mgr.Reload(h.ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if err := h.mgr.StopAll(h.ctx, "areaA", 208); err != nil {
		t.Fatalf("StopAll areaA: %v", err)
	}
	if n := dev.turnOffCount(); n != 1 {
		t.Fatalf("shared siren TurnOff calls = %d, want 1 (a stale demand must not block a stop forever)", n)
	}
}

// TestSharedChannel_StopWatchdogsClearsDemandsSoStopProceeds covers
// the StopWatchdogs safety net: a bridge-level service stop drops
// every shared-channel demand along with the watchdog timers, so a
// later StopAll writes the OFF even though another area's demand
// existed just before the service stop.
func TestSharedChannel_StopWatchdogsClearsDemandsSoStopProceeds(t *testing.T) {
	h := newHarness(t)
	rowA := sharedRow("sirA_areaA", "areaA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	rowB := sharedRow("sirA_areaB", "areaB", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	h.seedOutputs(rowA, rowB)
	dev := sirenAt(t, h, "shared:1")

	if err := h.mgr.FireCycle(h.ctx, "areaA", newIncident(210, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle areaA: %v", err)
	}
	if err := h.mgr.FireCycle(h.ctx, "areaB", newIncident(211, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle areaB: %v", err)
	}

	h.mgr.StopWatchdogs()

	if err := h.mgr.StopAll(h.ctx, "areaA", 210); err != nil {
		t.Fatalf("StopAll areaA: %v", err)
	}
	if n := dev.turnOffCount(); n != 1 {
		t.Fatalf("shared siren TurnOff calls after StopWatchdogs+StopAll = %d, want 1 (demands are cleared with the watchdogs)", n)
	}
}
