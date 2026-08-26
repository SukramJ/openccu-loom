// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package outputs

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestWatchdog_StopVerifiesAndClearsActivation covers S2 case 14: at
// the activation deadline the watchdog writes a critical-priority
// stop; once the device confirms inactive+observed, the next verify
// pass clears the activation and reports healthy without retrying
// further.
func TestWatchdog_StopVerifiesAndClearsActivation(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(outputRow("sirA", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120}))
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(14, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	h.advance(120 * time.Second)
	off := h.siren("sirA").turnOffCallsSnapshot()
	if len(off) != 1 {
		t.Fatalf("TurnOff calls after deadline = %d, want 1", len(off))
	}
	if off[0].Priority != hmenum.CommandPriorityCritical {
		t.Fatalf("stop priority = %v, want Critical", off[0].Priority)
	}

	// The CCU confirms the device is off.
	h.siren("sirA").setAcoustic(false, true)
	h.advance(10 * time.Second)

	if n := h.siren("sirA").turnOffCount(); n != 1 {
		t.Fatalf("TurnOff calls after verification = %d, want 1 (no retry once verified)", n)
	}
	hc := h.lastHealth(t)
	if !hc.Healthy {
		t.Fatalf("health callback = %+v, want healthy=true", hc)
	}

	// Advancing well past the deadline must not produce further stops.
	h.advance(200 * time.Second)
	if n := h.siren("sirA").turnOffCount(); n != 1 {
		t.Fatalf("TurnOff calls after further advance = %d, want 1", n)
	}
}

// TestWatchdog_StopRetriesUntilVerifyWindowThenEscalates covers S2
// case 15: a device that stays observed-active is retried at
// critical priority every stop-verify interval; once the verify
// window elapses the failure escalates to a fault + unhealthy signal
// and retries stop.
func TestWatchdog_StopRetriesUntilVerifyWindowThenEscalates(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(outputRow("sirA", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120}))
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(15, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}
	// The device stubbornly reports itself as still on.
	h.siren("sirA").setAcoustic(true, true)

	h.advance(120 * time.Second)
	firstStop := h.siren("sirA").turnOffCount()
	if firstStop != 1 {
		t.Fatalf("TurnOff calls at deadline = %d, want 1", firstStop)
	}

	for i := 0; i < 10 && h.healthCallCount() == 0; i++ {
		h.advance(10 * time.Second)
	}
	if h.healthCallCount() != 1 {
		t.Fatalf("health callback count = %d, want 1 after the verify window elapses", h.healthCallCount())
	}
	hc := h.lastHealth(t)
	if hc.Healthy {
		t.Fatalf("health callback = %+v, want healthy=false", hc)
	}
	if !h.journal.hasForOutput("output_stop_unverified", "sirA") {
		t.Fatal("expected an output_stop_unverified journal entry for sirA")
	}
	stopsAtEscalation := h.siren("sirA").turnOffCount()
	if stopsAtEscalation <= 1 {
		t.Fatalf("expected multiple retries before escalation, got %d stop calls", stopsAtEscalation)
	}

	// No further retries once the window has closed.
	h.advance(100 * time.Second)
	if n := h.siren("sirA").turnOffCount(); n != stopsAtEscalation {
		t.Fatalf("TurnOff calls after escalation = %d, want %d (no further retries)", n, stopsAtEscalation)
	}
	if h.healthCallCount() != 1 {
		t.Fatalf("health callback count after escalation = %d, want 1", h.healthCallCount())
	}
}

// TestWatchdog_UnobservedStateCountsAsNotVerified covers S2 case 16:
// an active-but-unobserved read-back counts as not verified — the
// watchdog keeps retrying exactly like a confirmed-active device,
// then escalates once the verify window elapses.
func TestWatchdog_UnobservedStateCountsAsNotVerified(t *testing.T) {
	h := newHarness(t)
	h.seedOutputs(outputRow("sirA", hmenum.AlarmOutputClassAcousticSiren, OutputConfig{DurationSeconds: 120}))
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(16, hmenum.AlarmModeFull), engine.FireOptions{Policy: noPolicy}); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}
	// Active feedback never arrives (observed stays false).
	h.siren("sirA").setAcoustic(true, false)

	h.advance(120 * time.Second)
	for i := 0; i < 10 && h.healthCallCount() == 0; i++ {
		h.advance(10 * time.Second)
	}
	if h.healthCallCount() != 1 {
		t.Fatalf("health callback count = %d, want 1 after the verify window elapses", h.healthCallCount())
	}
	if h.lastHealth(t).Healthy {
		t.Fatal("expected the escalation to report healthy=false")
	}
	if !h.journal.hasForOutput("output_stop_unverified", "sirA") {
		t.Fatal("expected an output_stop_unverified journal entry for sirA")
	}
	if n := h.siren("sirA").turnOffCount(); n <= 1 {
		t.Fatalf("expected multiple retry attempts, got %d", n)
	}
}

// TestStopAll_CancelsPendingWatchdogAndStopsEveryClass covers S2
// case 17: StopAll cancels any pending fire-watchdog and issues an
// immediate critical-priority stop on every stoppable class,
// including the alarm light and the chirp siren; notification
// outputs are never touched.
func TestStopAll_CancelsPendingWatchdogAndStopsEveryClass(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.seedOutputs(outputRow("notify", hmenum.AlarmOutputClassNotification, OutputConfig{}))

	opts := engine.FireOptions{Policy: engine.OutputPolicy{ExcludeOutdoor: true}}
	if err := h.mgr.FireCycle(h.ctx, "eg", newIncident(17, hmenum.AlarmModeFull), opts); err != nil {
		t.Fatalf("FireCycle: %v", err)
	}

	if err := h.mgr.StopAll(h.ctx, "eg", 17); err != nil {
		t.Fatalf("StopAll: %v", err)
	}

	if n := h.siren("sirA").turnOffCount(); n != 1 {
		t.Fatalf("sirA TurnOff calls = %d, want 1", n)
	}
	if p := h.siren("sirA").turnOffCallsSnapshot()[0].Priority; p != hmenum.CommandPriorityCritical {
		t.Fatalf("sirA stop priority = %v, want Critical", p)
	}
	if n := h.siren("chirp").turnOffCount(); n != 1 {
		t.Fatalf("chirp TurnOff calls = %d, want 1", n)
	}
	if n := h.smoke("smoke").turnOffCount(); n != 1 {
		t.Fatalf("smoke TurnOff calls = %d, want 1", n)
	}
	if n := h.actuator("light").turnOffCount(); n != 1 {
		t.Fatalf("light TurnOff calls = %d, want 1", n)
	}

	// The old fire-watchdog for sirA (armed at 120 s) must have been
	// cancelled and replaced by StopAll's own verify pass.
	h.siren("sirA").setAcoustic(false, true)
	h.advance(10 * time.Second)
	h.advance(200 * time.Second)
	if n := h.siren("sirA").turnOffCount(); n != 1 {
		t.Fatalf("sirA TurnOff calls after advancing past the original deadline = %d, want 1", n)
	}
}
