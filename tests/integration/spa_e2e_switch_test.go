// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

// SPA-E2E plans for the Switch tile family.
//
//   - HmIP-BSM on channel 4 → switch (IPSwitch profile)
//     HmIP-PS uses the same IPSwitch profile but is not in godevccu's
//     embedded fleet. HmIP-BSM (Switch + power meter) is the available
//     anchor; its switch primary channel is 4.
//
//     · turn_on  → STATE=true
//     · turn_off → STATE=false
//     · turn_on_for {seconds: 5} → STATE=true (ON_TIME write is
//       bundled; only STATE is asserted since ON_TIME is write-only)

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestSPAE2E_Switch_HmIPBSM(t *testing.T) {
	h := newSPAHarness(t, []string{"HmIP-BSM"})

	plan := spaPlan{
		name:     "switch_HmIP-BSM",
		model:    "HmIP-BSM",
		chNo:     4,
		wantKind: "switch",
		actions: []spaAction{
			{
				op: "turn_on",
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterState: true,
				},
			},
			{
				op: "turn_off",
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterState: false,
				},
			},
			// turn_on_for arms a timer and sets STATE. This mirrors the exact
			// payload the SPA SwitchTile emits — the canonical "seconds" key
			// (a number of seconds), NOT the "duration" alias. ON_TIME is
			// write-only in most CCU profiles so only STATE is asserted.
			{
				op:     "turn_on_for",
				params: map[string]any{"seconds": 5},
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterState: true,
				},
			},
			// The "duration" alias (string form) stays supported for
			// API/MQTT clients that predate the canonical "seconds" key.
			{
				op:     "turn_on_for",
				params: map[string]any{"duration": "5s"},
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterState: true,
				},
			},
		},
	}
	plan.execute(t, h)
}
