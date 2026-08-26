// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	sqlitestore "github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeMotionReset records the RESET_MOTION writes the engine issues.
// supported lists the sensor IDs whose channel exposes the parameter;
// everything else answers false to Supports, mirroring a door contact.
type fakeMotionReset struct {
	mu        sync.Mutex
	supported map[string]bool
	failFor   map[string]bool
	writes    []string
}

func newFakeMotionReset(supported ...string) *fakeMotionReset {
	m := &fakeMotionReset{supported: map[string]bool{}, failFor: map[string]bool{}}
	for _, id := range supported {
		m.supported[id] = true
	}
	return m
}

func (m *fakeMotionReset) Supports(row sqlitestore.AlarmSensorRow) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.supported[row.ID]
}

func (m *fakeMotionReset) Reset(_ context.Context, row sqlitestore.AlarmSensorRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.writes = append(m.writes, row.ID)
	if m.failFor[row.ID] {
		return errors.New("device unreachable")
	}
	return nil
}

func (m *fakeMotionReset) written() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.writes...)
}

// motionHarness builds a started engine with the fake reset port wired
// and the standard zone seeded.
func motionHarness(t *testing.T, reset *fakeMotionReset) *harness {
	t.Helper()
	h := newHarness(t)
	h.seedStandardZone()
	h.motionReset = reset
	h.start()
	return h
}

// TestResetTouchesOnlyTriggeredSensors pins the rule the operator
// asked for: a detector that is not currently latched is left alone.
// Writing to every enrolled detector would put avoidable radio traffic
// on the whole installation on every arm.
func TestResetTouchesOnlyTriggeredSensors(t *testing.T) {
	t.Parallel()

	reset := newFakeMotionReset("motion", "window")
	h := motionHarness(t, reset)

	// Only `motion` is latched.
	h.eng.HandleSensorEvent(h.ctx, "motion", true)

	res := h.eng.ResetTriggeredMotion(h.ctx, "eg", "tester", "test")
	if got := reset.written(); len(got) != 1 || got[0] != "motion" {
		t.Errorf("writes = %v, want only [motion]", got)
	}
	if res.Reset != 1 || res.Failed != 0 {
		t.Errorf("result = %+v, want 1 reset, 0 failed", res)
	}
}

// TestResetSkipsSensorsWithoutTheParameter pins that a latched sensor
// whose channel has no RESET_MOTION is not written to. A door contact
// reads as open for a real reason and has nothing to reset.
func TestResetSkipsSensorsWithoutTheParameter(t *testing.T) {
	t.Parallel()

	reset := newFakeMotionReset("motion") // door deliberately unsupported
	h := motionHarness(t, reset)

	h.eng.HandleSensorEvent(h.ctx, "motion", true)
	h.eng.HandleSensorEvent(h.ctx, "door", true)

	h.eng.ResetTriggeredMotion(h.ctx, "eg", "tester", "test")
	if got := reset.written(); len(got) != 1 || got[0] != "motion" {
		t.Errorf("writes = %v, want only [motion]", got)
	}
}

// TestReportedCountMatchesWhatResetActsOn is the invariant behind the
// north-bound counter: the number the UI shows must be the number of
// sensors the button acts on. If they were derived separately, an
// operator could see "2 triggered" and press a button that clears one.
func TestReportedCountMatchesWhatResetActsOn(t *testing.T) {
	t.Parallel()

	reset := newFakeMotionReset("motion")
	h := motionHarness(t, reset)

	h.eng.HandleSensorEvent(h.ctx, "motion", true)
	h.eng.HandleSensorEvent(h.ctx, "door", true)    // latched, unsupported
	h.eng.HandleSensorEvent(h.ctx, "window", false) // supported, not latched

	listed := h.eng.TriggeredMotionSensors("eg")
	res := h.eng.ResetTriggeredMotion(h.ctx, "eg", "tester", "test")
	if len(listed) != len(res.Sensors) {
		t.Fatalf("listed %d sensors but reset acted on %d", len(listed), len(res.Sensors))
	}
	for i := range listed {
		if listed[i].SensorID != res.Sensors[i].SensorID {
			t.Errorf("sensor %d: listed %q, reset %q", i, listed[i].SensorID, res.Sensors[i].SensorID)
		}
	}
	if len(listed) != 1 || listed[0].SensorID != "motion" {
		t.Errorf("listed = %+v, want only motion", listed)
	}
}

// TestResetIsScopedToOneZone pins that "reset this zone" does not
// reach into another zone's detectors, and that the fleet-wide form
// does.
func TestResetIsScopedToOneZone(t *testing.T) {
	t.Parallel()

	reset := newFakeMotionReset("motion", "og-motion")
	h := newHarness(t)
	h.seedStandardZone()
	h.seedZone("og", "Obergeschoss", defaultZoneConfig())
	h.seedSensor("og-motion", "og", hmenum.AlarmSensorTypeMotion, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
	})
	h.motionReset = reset
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "motion", true)
	h.eng.HandleSensorEvent(h.ctx, "og-motion", true)

	h.eng.ResetTriggeredMotion(h.ctx, "eg", "tester", "test")
	if got := reset.written(); len(got) != 1 || got[0] != "motion" {
		t.Fatalf("zone reset writes = %v, want only [motion]", got)
	}

	// The fleet-wide form covers both.
	res := h.eng.ResetTriggeredMotion(h.ctx, "", "tester", "test")
	if res.Reset != 2 {
		t.Errorf("fleet-wide reset = %d sensors, want 2", res.Reset)
	}
}

// TestResetContinuesPastAFailingDevice pins the best-effort contract.
// One detector that stopped answering must not keep the others latched
// — least of all on the arming path.
func TestResetContinuesPastAFailingDevice(t *testing.T) {
	t.Parallel()

	reset := newFakeMotionReset("motion", "window")
	reset.failFor["motion"] = true
	h := motionHarness(t, reset)

	h.eng.HandleSensorEvent(h.ctx, "motion", true)
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	res := h.eng.ResetTriggeredMotion(h.ctx, "eg", "tester", "test")
	if len(reset.written()) != 2 {
		t.Errorf("writes = %v, want both attempted", reset.written())
	}
	if res.Reset != 1 || res.Failed != 1 {
		t.Errorf("result = %+v, want 1 reset / 1 failed", res)
	}
}

// TestResetJournalsUnderMaintenance pins that the actuation reaches the
// operator timeline. A write to somebody's detectors that leaves no
// trace is exactly the kind of silent action the journal exists for.
func TestResetJournalsUnderMaintenance(t *testing.T) {
	t.Parallel()

	reset := newFakeMotionReset("motion")
	h := motionHarness(t, reset)
	h.eng.HandleSensorEvent(h.ctx, "motion", true)
	h.journal.reset()

	h.eng.ResetTriggeredMotion(h.ctx, "eg", "tester", "ui")

	entries := h.journal.entriesOfClass(hmenum.AlarmJournalClassMaintenance)
	if len(entries) != 1 {
		t.Fatalf("maintenance entries = %d, want 1 (all: %+v)", len(entries), h.journal.all())
	}
	if entries[0].Event != "motion_reset" {
		t.Errorf("event = %q, want motion_reset", entries[0].Event)
	}
	if entries[0].ZoneID != "eg" {
		t.Errorf("zone = %q, want eg", entries[0].ZoneID)
	}
}

// TestResetWritesNothingWhenNoSensorIsLatched pins the quiet path: an
// arm on a clean installation must not produce any radio traffic.
func TestResetWritesNothingWhenNoSensorIsLatched(t *testing.T) {
	t.Parallel()

	reset := newFakeMotionReset("motion")
	h := motionHarness(t, reset)

	res := h.eng.ResetTriggeredMotion(h.ctx, "eg", "tester", "test")
	if got := reset.written(); len(got) != 0 {
		t.Errorf("writes = %v, want none", got)
	}
	if res.Reset != 0 || res.Failed != 0 || len(res.Sensors) != 0 {
		t.Errorf("result = %+v, want empty", res)
	}
	if entries := h.journal.entriesOfClass(hmenum.AlarmJournalClassMaintenance); len(entries) != 0 {
		t.Errorf("journal entries = %d, want none for a no-op", len(entries))
	}
}

// TestArmResetsLatchedMotion pins the automatic pass the operator
// asked for: activating the alarm clears the detectors that are
// holding a stale latch.
func TestArmResetsLatchedMotion(t *testing.T) {
	t.Parallel()

	reset := newFakeMotionReset("motion")
	h := motionHarness(t, reset)
	h.eng.HandleSensorEvent(h.ctx, "motion", true)

	// Arm in perimeter, a mode `motion` does not participate in, so the
	// latch is not a blocker and the arm succeeds. That the reset still
	// covers the detector is the second half of the assertion: the pass
	// clears the zone's latched detectors, not just the ones the
	// requested mode happens to watch — otherwise a latch would survive
	// unnoticed until the next full arm tripped over it.
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModePerimeter, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	waitForWrites(t, reset, 1)
	if got := reset.written(); len(got) != 1 || got[0] != "motion" {
		t.Errorf("writes = %v, want [motion] on arm", got)
	}
}

// TestArmWithLatchedMotionStillBlocks documents the behaviour that
// motivated the feature: in a mode the detector participates in, a
// stale latch fails the arm outright. The automatic reset is what
// shortens that state — it does not, and must not, mask it.
func TestArmWithLatchedMotionStillBlocks(t *testing.T) {
	t.Parallel()

	reset := newFakeMotionReset("motion")
	h := motionHarness(t, reset)
	h.eng.HandleSensorEvent(h.ctx, "motion", true)

	_, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"})
	var notReady *engine.NotReadyError
	if !errors.As(err, &notReady) {
		t.Fatalf("arm error = %v, want NotReadyError", err)
	}
	waitForWrites(t, reset, 1)
}

// TestArmDoesNotResetWhenNothingIsLatched pins that a routine arm on a
// clean installation stays silent on the radio.
func TestArmDoesNotResetWhenNothingIsLatched(t *testing.T) {
	t.Parallel()

	reset := newFakeMotionReset("motion")
	h := motionHarness(t, reset)

	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.advance(30 * time.Second)
	if got := reset.written(); len(got) != 0 {
		t.Errorf("writes = %v, want none", got)
	}
}

// TestArmResetDoesNotOverrideBlockers is the safety pin on the
// automatic pass. A detector latched because somebody is moving in the
// room must still block the arm — the reset shortens a stale latch, it
// does not vote on readiness. Without this the feature would silently
// convert "someone is in the room" into "armed".
func TestArmResetDoesNotOverrideBlockers(t *testing.T) {
	t.Parallel()

	reset := newFakeMotionReset("window")
	h := motionHarness(t, reset)

	// `window` is an instant sensor in both modes: latched, it blocks.
	h.eng.HandleSensorEvent(h.ctx, "window", true)

	_, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"})
	var notReady *engine.NotReadyError
	if !errors.As(err, &notReady) {
		t.Fatalf("arm error = %v, want NotReadyError — the reset must not clear the blocker", err)
	}
	// The reset still went out: the point is that it does not pre-empt
	// the decision, not that it is skipped.
	waitForWrites(t, reset, 1)
}

// waitForWrites waits for the asynchronous pre-arm pass to reach n
// writes. The arming path hands the writes to a goroutine on purpose
// (they must not run under the engine lock), so tests synchronise here
// rather than sleeping.
func waitForWrites(t *testing.T, reset *fakeMotionReset, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(reset.written()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d reset write(s), got %v", n, reset.written())
}
