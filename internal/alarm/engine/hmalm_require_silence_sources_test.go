// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestRequireSilenceGatesOnlyAnonymousSources pins which sources a
// per-source silence-code gate can actually reach.
//
// RequireSilence is keyed by the source string, but resolveCode drops
// the requirement for every pre-authenticated source: the operator
// surfaces carry a session, and a keypad or remote press is
// authenticated by its slot/binding match and carries no PIN that could
// be typed. Only the anonymous planes — MQTT, sysvar — are gateable.
//
// The asymmetry is invisible from the config document, so it is stated
// on the field (see CodePolicy.RequireSilence) and measured here: an
// entry for a pre-authenticated source is inert, and an operator who
// enables it must not be told otherwise by a surface that offers it.
func TestRequireSilenceGatesOnlyAnonymousSources(t *testing.T) {
	gateAll := map[string]bool{
		"mqtt":                        true,
		"sysvar":                      true,
		engine.CodeSourceKeypad:       true,
		engine.CodeSourceRemote:       true,
		engine.CodeSourceRESTOperator: true,
		engine.CodeSourceWSOperator:   true,
		engine.CodeSourceHmcli:        true,
	}
	cases := []struct {
		source    string
		wantGated bool
	}{
		{source: "mqtt", wantGated: true},
		{source: "sysvar", wantGated: true},
		{source: engine.CodeSourceKeypad, wantGated: false},
		{source: engine.CodeSourceRemote, wantGated: false},
		{source: engine.CodeSourceRESTOperator, wantGated: false},
		{source: engine.CodeSourceWSOperator, wantGated: false},
		{source: engine.CodeSourceHmcli, wantGated: false},
	}

	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			h := newHarness(t)
			h.seedZone("eg", "Erdgeschoss", codePolicyZoneConfig(false, boolPtr(false), gateAll))
			h.seedSensor("window", "eg", hmenum.AlarmSensorTypeWindow, engine.SensorConfig{
				Modes: []hmenum.AlarmMode{hmenum.AlarmModeFull},
			})
			// No code authenticates: every code-gated verb is refused.
			h.startWithValidator(newFakeCodeValidator(nil))
			h.armFull()
			h.eng.HandleSensorEvent(h.ctx, "window", true)
			h.wantState("eg", hmenum.AlarmZoneStateTriggered)

			err := h.eng.Silence(h.ctx, "eg", "tester", tc.source)
			gated := errors.Is(err, engine.ErrInvalidCode)
			if gated != tc.wantGated {
				t.Fatalf("code-free silence from %q: err = %v (gated=%v), want gated=%v",
					tc.source, err, gated, tc.wantGated)
			}
			if err != nil && !gated {
				t.Fatalf("silence from %q: unexpected error %v", tc.source, err)
			}
		})
	}
}
