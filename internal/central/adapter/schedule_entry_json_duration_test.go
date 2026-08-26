// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
)

// TestSimpleEntryJSONDistinguishesAbsentFromZeroDuration is the regression
// guard for the schedule-duration collision: an empty Duration/RampTime
// ("no duration — leave the device's value alone") used to be rewritten to
// the literal "0ms", which is the daemon's own encoding for the genuine
// (base 0, factor 0) zero duration AND for the firmware's "permanent"
// sentinel (base 7, factor 31) — both decode to "". A door-lock slot with a
// standing/permanent duration would publish "duration": "0ms" on the MQTT
// Zeitplan attributes topic, the opposite of what it holds.
func TestSimpleEntryJSONDistinguishesAbsentFromZeroDuration(t *testing.T) {
	t.Parallel()

	t.Run("absent duration publishes null, not 0ms", func(t *testing.T) {
		t.Parallel()
		got := simpleEntryJSON(schedule.SimpleEntry{Duration: "", RampTime: ""})
		if got["duration"] != nil {
			t.Errorf("duration = %#v, want nil (JSON null)", got["duration"])
		}
		if got["ramp_time"] != nil {
			t.Errorf("ramp_time = %#v, want nil (JSON null)", got["ramp_time"])
		}
	})

	t.Run("genuine zero duration is preserved verbatim", func(t *testing.T) {
		t.Parallel()
		got := simpleEntryJSON(schedule.SimpleEntry{Duration: "0ms", RampTime: "500ms"})
		if got["duration"] != "0ms" {
			t.Errorf("duration = %#v, want the genuine zero-duration string 0ms", got["duration"])
		}
		if got["ramp_time"] != "500ms" {
			t.Errorf("ramp_time = %#v, want 500ms", got["ramp_time"])
		}
	})
}
