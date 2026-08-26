// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// sharedRow builds one output row enrolled under zoneID against an
// explicit channelAddress, independent of the id+":1" convention
// outputRow uses. Two zones can enroll separate rows against the very
// same physical channel this way, which is exactly the shared-channel
// fixture these tests need.
func sharedRow(id, zoneID string, class hmenum.AlarmOutputClass, channelAddress string, cfg OutputConfig) sqlitestore.AlarmOutputRow {
	b, err := json.Marshal(cfg)
	if err != nil {
		// Programmer error: OutputConfig always marshals.
		panic(err)
	}
	return sqlitestore.AlarmOutputRow{
		ID:             id,
		ZoneID:         zoneID,
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

// TestSharedChannel_StopAllDoesNotSilenceOtherZonesActiveDemand covers
// the core arbitration invariant: two zones enrolled on the same
// physical siren channel each hold a demand after firing, so the
// first zone's StopAll leaves the device sounding for the other zone
// without journaling a fault, and only the second zone's StopAll
// writes the OFF.
func TestSharedChannel_StopAllDoesNotSilenceOtherZonesActiveDemand(t *testing.T) {
	h := newHarness(t)
	rowA := sharedRow("sirA_zoneA", "zoneA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	rowB := sharedRow("sirA_zoneB", "zoneB", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	h.seedOutputs(rowA, rowB)
	dev := sirenAt(t, h, "shared:1")

	if err := h.mgr.FireCycle(h.ctx, "zoneA", newIncident(201, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle zoneA: %v", err)
	}
	if err := h.mgr.FireCycle(h.ctx, "zoneB", newIncident(202, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle zoneB: %v", err)
	}
	if n := dev.turnOnCount(); n != 2 {
		t.Fatalf("shared siren TurnOn calls = %d, want 2 (one per zone)", n)
	}

	if err := h.mgr.StopAll(h.ctx, "zoneA", 201); err != nil {
		t.Fatalf("StopAll zoneA: %v", err)
	}
	if n := dev.turnOffCount(); n != 0 {
		t.Fatalf("shared siren TurnOff calls after zoneA StopAll = %d, want 0 (device still on for zoneB)", n)
	}
	if h.journal.hasForOutput("output_stop_failed", rowA.ID) {
		t.Fatal("zoneA StopAll must not journal a fault when deferring to a shared demand")
	}
	if h.journal.hasForOutput("output_stop_unverified", rowA.ID) {
		t.Fatal("zoneA StopAll must not journal an unverified-stop fault when deferring to a shared demand")
	}

	if err := h.mgr.StopAll(h.ctx, "zoneB", 202); err != nil {
		t.Fatalf("StopAll zoneB: %v", err)
	}
	if n := dev.turnOffCount(); n != 1 {
		t.Fatalf("shared siren TurnOff calls after zoneB StopAll = %d, want 1 (last demanding zone stops it)", n)
	}
	if p := dev.turnOffCallsSnapshot()[0].Priority; p != hmenum.CommandPriorityCritical {
		t.Fatalf("stop priority = %v, want Critical", p)
	}
}

// TestSharedChannel_WatchdogDefersStopWhileOtherZoneStillDemands
// covers the watchdog leg of the same invariant: zoneA's own fire
// watchdog fires first (shorter duration) while zoneB's demand is
// still outstanding, so the scheduled stop is deferred and its
// activation is cleared without a write; zoneB's own watchdog later
// finds no foreign demand left and writes the OFF.
func TestSharedChannel_WatchdogDefersStopWhileOtherZoneStillDemands(t *testing.T) {
	h := newHarness(t)
	rowA := sharedRow("sirA_zoneA", "zoneA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 60})
	rowB := sharedRow("sirA_zoneB", "zoneB", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	h.seedOutputs(rowA, rowB)
	dev := sirenAt(t, h, "shared:1")

	if err := h.mgr.FireCycle(h.ctx, "zoneA", newIncident(203, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle zoneA: %v", err)
	}
	if err := h.mgr.FireCycle(h.ctx, "zoneB", newIncident(204, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle zoneB: %v", err)
	}

	// zoneA's watchdog fires at 60s; zoneB still demands the channel.
	h.advance(60 * time.Second)
	if n := dev.turnOffCount(); n != 0 {
		t.Fatalf("TurnOff calls at zoneA's deadline = %d, want 0 (zoneB still demands the channel)", n)
	}
	if h.journal.hasForOutput("output_stop_unverified", rowA.ID) {
		t.Fatal("a deferred watchdog stop must not escalate a fault for zoneA")
	}
	if h.healthCallCount() != 0 {
		t.Fatalf("health callback count after the deferred stop = %d, want 0", h.healthCallCount())
	}

	// zoneB's own watchdog fires at 120s; no foreign demand remains.
	h.advance(60 * time.Second)
	if n := dev.turnOffCount(); n != 1 {
		t.Fatalf("TurnOff calls at zoneB's deadline = %d, want 1", n)
	}
	if p := dev.turnOffCallsSnapshot()[0].Priority; p != hmenum.CommandPriorityCritical {
		t.Fatalf("stop priority = %v, want Critical", p)
	}
}

// TestSharedChannel_OwnZoneMultipleRowsNeverBlockOwnStop covers a
// single zone enrolling two rows against the same physical channel: a
// row's own-zone sibling demand must never count as foreign, so
// StopAll for the zone always writes the OFF for every row.
func TestSharedChannel_OwnZoneMultipleRowsNeverBlockOwnStop(t *testing.T) {
	h := newHarness(t)
	row1 := sharedRow("sirA_1", "zoneA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	row2 := sharedRow("sirA_2", "zoneA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	h.seedOutputs(row1, row2)
	dev := sirenAt(t, h, "shared:1")

	if err := h.mgr.FireCycle(h.ctx, "zoneA", newIncident(205, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}
	// One channel, one atomic activation write — the rows share the
	// device, and a second write would only replace the first. Each row
	// still holds its own demand, which is what the stop half below is
	// about.
	if n := dev.turnOnCount(); n != 1 {
		t.Fatalf("shared siren TurnOn calls = %d, want 1 (both rows address one channel)", n)
	}

	if err := h.mgr.StopAll(h.ctx, "zoneA", 205); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if n := dev.turnOffCount(); n != 2 {
		t.Fatalf("shared siren TurnOff calls = %d, want 2 (no own-zone demand blocks the stop)", n)
	}
	for _, c := range dev.turnOffCallsSnapshot() {
		if c.Priority != hmenum.CommandPriorityCritical {
			t.Fatalf("stop priority = %v, want Critical", c.Priority)
		}
	}
}

// TestSharedChannel_AlarmLightStaysOnUntilLastZoneStops covers the
// steady-on alarm-light class, which has no watchdog: two zones share
// the same light, and only the second zone's StopAll turns it off.
func TestSharedChannel_AlarmLightStaysOnUntilLastZoneStops(t *testing.T) {
	h := newHarness(t)
	rowA := sharedRow("light_zoneA", "zoneA", hmenum.AlarmOutputClassAlarmLight, "light:1", OutputConfig{})
	rowB := sharedRow("light_zoneB", "zoneB", hmenum.AlarmOutputClassAlarmLight, "light:1", OutputConfig{})
	h.seedOutputs(rowA, rowB)
	dev := actuatorAt(t, h, "light:1")

	if err := h.mgr.FireCycle(h.ctx, "zoneA", newIncident(206, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle zoneA: %v", err)
	}
	if err := h.mgr.FireCycle(h.ctx, "zoneB", newIncident(207, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle zoneB: %v", err)
	}
	if n := dev.steadyCallCount(); n != 2 {
		t.Fatalf("shared light TurnOnSteady calls = %d, want 2", n)
	}

	if err := h.mgr.StopAll(h.ctx, "zoneA", 206); err != nil {
		t.Fatalf("StopAll zoneA: %v", err)
	}
	if n := dev.turnOffCount(); n != 0 {
		t.Fatalf("shared light TurnOff calls after zoneA StopAll = %d, want 0 (zoneB still demands it)", n)
	}

	if err := h.mgr.StopAll(h.ctx, "zoneB", 207); err != nil {
		t.Fatalf("StopAll zoneB: %v", err)
	}
	if n := dev.turnOffCount(); n != 1 {
		t.Fatalf("shared light TurnOff calls after zoneB StopAll = %d, want 1", n)
	}
}

// TestSharedChannel_ReloadPrunesStaleDemandSoStopProceeds covers the
// Reload safety net: when zoneB's row disappears from the row source
// (output or zone deleted mid-alarm), Reload prunes its stale demand
// so zoneA's StopAll is no longer blocked.
func TestSharedChannel_ReloadPrunesStaleDemandSoStopProceeds(t *testing.T) {
	h := newHarness(t)
	rowA := sharedRow("sirA_zoneA", "zoneA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	rowB := sharedRow("sirA_zoneB", "zoneB", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	h.seedOutputs(rowA, rowB)
	dev := sirenAt(t, h, "shared:1")

	if err := h.mgr.FireCycle(h.ctx, "zoneA", newIncident(208, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle zoneA: %v", err)
	}
	if err := h.mgr.FireCycle(h.ctx, "zoneB", newIncident(209, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle zoneB: %v", err)
	}

	// zoneB's output row is deleted from the row source.
	h.allRows = []sqlitestore.AlarmOutputRow{rowA}
	h.rows.set(h.allRows)
	if err := h.mgr.Reload(h.ctx); err != nil {
		t.Fatalf("Reload: %v", err)
	}

	if err := h.mgr.StopAll(h.ctx, "zoneA", 208); err != nil {
		t.Fatalf("StopAll zoneA: %v", err)
	}
	if n := dev.turnOffCount(); n != 1 {
		t.Fatalf("shared siren TurnOff calls = %d, want 1 (a stale demand must not block a stop forever)", n)
	}
}

// TestSharedChannel_StopWatchdogsClearsDemandsSoStopProceeds covers
// the StopWatchdogs safety net: a bridge-level service stop drops
// every shared-channel demand along with the watchdog timers, so a
// later StopAll writes the OFF even though another zone's demand
// existed just before the service stop.
func TestSharedChannel_StopWatchdogsClearsDemandsSoStopProceeds(t *testing.T) {
	h := newHarness(t)
	rowA := sharedRow("sirA_zoneA", "zoneA", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	rowB := sharedRow("sirA_zoneB", "zoneB", hmenum.AlarmOutputClassAcousticSiren, "shared:1", OutputConfig{DurationSeconds: 120})
	h.seedOutputs(rowA, rowB)
	dev := sirenAt(t, h, "shared:1")

	if err := h.mgr.FireCycle(h.ctx, "zoneA", newIncident(210, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle zoneA: %v", err)
	}
	if err := h.mgr.FireCycle(h.ctx, "zoneB", newIncident(211, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle zoneB: %v", err)
	}

	h.mgr.StopWatchdogs()

	if err := h.mgr.StopAll(h.ctx, "zoneA", 210); err != nil {
		t.Fatalf("StopAll zoneA: %v", err)
	}
	if n := dev.turnOffCount(); n != 1 {
		t.Fatalf("shared siren TurnOff calls after StopWatchdogs+StopAll = %d, want 1 (demands are cleared with the watchdogs)", n)
	}
}
