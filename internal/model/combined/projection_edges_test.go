// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Edge cases of the projection seam: the shapes a wire value can arrive
// in, the state renderings that need clamping, and the guards that stop a
// missing collaborator from becoming a panic on the publish path.

package combined_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestEnumSelectResolvesAnIndexAgainstItsValueList pins the
// index-to-token resolution.
//
// The resolver sits between the generic data point and the mode list. An
// index it cannot resolve yields no token, which reads downstream as "not
// a selectable mode" and leaves the control valueless — the honest
// outcome, since the alternative is naming a mode nothing observed.
// Exercised through Subscribe because that is the only production caller.
func TestEnumSelectResolvesAnIndexAgainstItsValueList(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		event int32
		want  string
	}{
		{name: "index in range", event: 1, want: "OPEN"},
		{name: "index past the end of the list", event: 99, want: ""},
		{name: "negative index", event: -1, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ch, sensor := newEnumChannel(t, "MOD0100:1",
				[]string{"CLOSED", "OPEN", "VENTILATION_POSITION", "POSITION_UNKNOWN"})
			e := newGarageSelect(&recordingWriter{})
			unsub := e.Subscribe(ch)
			defer unsub()

			sensor.OnEvent(tc.event)
			got, ok := e.Value()
			if tc.want == "" {
				if ok {
					t.Fatalf("Value() = (%q, true), want no value for an unresolvable index", got)
				}
				return
			}
			if !ok || got != tc.want {
				t.Fatalf("Value() = (%q, %v), want %q", got, ok, tc.want)
			}
		})
	}
}

// TestEnumSelectToleratesAnEmptyValueList pins the case where the device
// description carries no VALUE_LIST: every index resolves to no token, so
// the control stays valueless rather than reporting a mode derived from a
// list that does not exist.
func TestEnumSelectToleratesAnEmptyValueList(t *testing.T) {
	t.Parallel()
	ch, sensor := newEnumChannel(t, "MOD0101:1", nil)
	e := newGarageSelect(&recordingWriter{})
	unsub := e.Subscribe(ch)
	defer unsub()

	sensor.OnEvent(0)
	if v, ok := e.Value(); ok {
		t.Fatalf("Value() = (%q, true) with no VALUE_LIST, want no value", v)
	}
}

// TestCombinedProjectionsDeclineWithoutAContext pins the nil-context guard
// on every migrated projection. The bridge always supplies one; the guard
// is what keeps a nil from becoming a panic on the publish path.
func TestCombinedProjectionsDeclineWithoutAContext(t *testing.T) {
	t.Parallel()
	projections := []payload.CombinedProjection{
		combined.NewTimer("VCU0000001:1", nil, "DURATION_VALUE", "DURATION_UNIT"),
		combined.NewLevelCombined("VCU0000001:1", "LEVEL", "LEVEL_2"),
		combined.NewHSColor("VCU0000001:1", nil, "HUE", "SATURATION"),
	}
	for _, p := range projections {
		component, body := p.HACombinedDiscovery(nil)
		if component != "" || body != nil {
			t.Errorf("%s: HACombinedDiscovery(nil) = (%q, %v), want a declined projection",
				p.CombinedKind(), component, body)
		}
	}
}

// TestTimerStatePayloadRendersFractionalAndClampedSeconds pins the two
// edges of the timer's state rendering: a fractional value keeps its
// fraction, and a negative one is clamped rather than published as a
// negative duration no consumer can act on.
func TestTimerStatePayloadRendersFractionalAndClampedSeconds(t *testing.T) {
	t.Parallel()

	t.Run("fractional", func(t *testing.T) {
		t.Parallel()
		timer := combined.NewTimer("VCU0000001:1", nil, "DURATION_VALUE", "DURATION_UNIT")
		timer.OnComponents(1.5, hmenum.TimerUnitSeconds)
		state, observed := timer.CombinedStatePayload()
		if !observed || state != "1.5" {
			t.Fatalf("state = (%q, %v), want (\"1.5\", true)", state, observed)
		}
	})

	t.Run("negative is clamped", func(t *testing.T) {
		t.Parallel()
		timer := combined.NewTimer("VCU0000001:1", nil, "DURATION_VALUE", "DURATION_UNIT")
		timer.OnComponents(-5, hmenum.TimerUnitSeconds)
		state, observed := timer.CombinedStatePayload()
		if !observed || state != "0" {
			t.Fatalf("state = (%q, %v), want (\"0\", true)", state, observed)
		}
	})
}
