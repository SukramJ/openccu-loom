// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// This file covers the door-chime output (docs/alarm-concept.md §15 row
// 23): a Chime-flagged sensor sounds only on its opening edge, only
// while the area is disarmed, and never while a walk test is running.

// chirpKinds returns the ordered list of chirp kinds o has recorded.
func chirpKinds(o *fakeOutputs) []engine.ChirpKind {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]engine.ChirpKind, 0, len(o.chirps))
	for _, c := range o.chirps {
		out = append(out, c.Req.Kind)
	}
	return out
}

// hasChirp reports whether o recorded at least one chirp of kind.
func hasChirp(o *fakeOutputs, kind engine.ChirpKind) bool {
	for _, k := range chirpKinds(o) {
		if k == kind {
			return true
		}
	}
	return false
}

func TestChime_FiresOnActivationWhileDisarmed(t *testing.T) {
	h := newHarness(t)
	h.seedArea("eg", "Erdgeschoss", defaultAreaConfig())
	h.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
		Chime: true,
	})
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "door", true)

	if !hasChirp(h.outputs, engine.ChirpChime) {
		t.Fatalf("expected a chime chirp; got %v", chirpKinds(h.outputs))
	}
}

func TestChime_DoesNotFireWhileArmed(t *testing.T) {
	h := newHarness(t)
	h.seedArea("eg", "Erdgeschoss", defaultAreaConfig())
	h.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}, UseEntryDelay: true, Chime: true,
	})
	h.start()
	h.armFull()

	h.eng.HandleSensorEvent(h.ctx, "door", true)
	h.wantState("eg", hmenum.AlarmAreaStatePending) // entry delay, not evaluated here

	if hasChirp(h.outputs, engine.ChirpChime) {
		t.Fatalf("chime fired while armed; chirps = %v", chirpKinds(h.outputs))
	}
}

func TestChime_SuppressedDuringAWalkTest(t *testing.T) {
	h := newHarness(t)
	h.seedArea("eg", "Erdgeschoss", defaultAreaConfig())
	h.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
		Chime: true,
	})
	h.start()

	if err := h.eng.WalkTestStart(h.ctx, "eg", "tester", "test"); err != nil {
		t.Fatalf("walk test start: %v", err)
	}
	h.eng.HandleSensorEvent(h.ctx, "door", true)

	if hasChirp(h.outputs, engine.ChirpChime) {
		t.Fatalf("chime fired during a walk test; chirps = %v", chirpKinds(h.outputs))
	}
	if !h.journal.has("walktest_sensor_seen") {
		t.Fatalf("missing walktest_sensor_seen journal entry; got %v", h.journal.events())
	}
}

func TestChime_OnlyFiresOnceOnTheOpeningEdge(t *testing.T) {
	h := newHarness(t)
	h.seedArea("eg", "Erdgeschoss", defaultAreaConfig())
	h.seedSensor("door", "eg", hmenum.AlarmSensorTypeDoor, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
		Chime: true,
	})
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "door", false) // already-closed, no edge
	h.eng.HandleSensorEvent(h.ctx, "door", true)  // opening edge: one chime
	h.eng.HandleSensorEvent(h.ctx, "door", true)  // repeat, ignored
	h.eng.HandleSensorEvent(h.ctx, "door", false) // closing, no chime

	n := 0
	for _, k := range chirpKinds(h.outputs) {
		if k == engine.ChirpChime {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("chime fire count = %d, want exactly 1; chirps = %v", n, chirpKinds(h.outputs))
	}
}

func TestChime_NotConfiguredNeverFires(t *testing.T) {
	h := newHarness(t)
	h.seedArea("eg", "Erdgeschoss", defaultAreaConfig())
	h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
		Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull}, // Chime left false
	})
	h.start()

	h.eng.HandleSensorEvent(h.ctx, "window", true)

	if hasChirp(h.outputs, engine.ChirpChime) {
		t.Fatalf("chime fired for a sensor without Chime configured; chirps = %v", chirpKinds(h.outputs))
	}
}
