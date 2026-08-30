// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHazardSensorsRequireAlwaysOn pins the safety invariant two surfaces
// depend on.
//
// A hazard sensor that is not always-on fires only while its zone is armed in
// one of its listed modes — and with the empty mode list that is normal for a
// smoke detector, it never fires at all. The REST write path couples the two
// so the failure cannot be configured; the input loader warns when a stored
// row arrives without it (a restored backup, a hand-edited row). Both asked
// the question in their own words until this named it.
func TestHazardSensorsRequireAlwaysOn(t *testing.T) {
	t.Parallel()
	if !engine.RequiresAlwaysOn(hmenum.AlarmSensorTypeHazard) {
		t.Error("a hazard sensor must bypass the arm state machine")
	}
	for _, other := range []hmenum.AlarmSensorType{
		hmenum.AlarmSensorTypeDoor, hmenum.AlarmSensorTypeWindow, hmenum.AlarmSensorTypeMotion,
	} {
		if engine.RequiresAlwaysOn(other) {
			t.Errorf("%s must not be forced always-on: it is armed-state driven", other)
		}
	}
	if !engine.AlwaysOnViolated(hmenum.AlarmSensorTypeHazard, engine.SensorConfig{}) {
		t.Error("a hazard sensor without always_on is a violation")
	}
	if engine.AlwaysOnViolated(hmenum.AlarmSensorTypeHazard, engine.SensorConfig{AlwaysOn: true}) {
		t.Error("a hazard sensor with always_on is not a violation")
	}
	if engine.AlwaysOnViolated(hmenum.AlarmSensorTypeMotion, engine.SensorConfig{}) {
		t.Error("a motion sensor without always_on is the normal shape")
	}
}
