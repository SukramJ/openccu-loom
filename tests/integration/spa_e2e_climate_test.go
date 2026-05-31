// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

// SPA-E2E plans for the Climate tile family.
//
//   - HmIP-BWTH on channel 1 → climate_hmip
//     · set_temperature {temperature: 21.5} → SET_POINT_TEMPERATURE=21.5
//     · set_mode {mode: auto}              → CONTROL_MODE=0
//     · set_mode {mode: heat}              → CONTROL_MODE=1 + SET_POINT_TEMPERATURE=max
//     · set_mode {mode: off}               → CONTROL_MODE=1 + SET_POINT_TEMPERATURE=4.5
//     · enable_boost / disable_boost
//
//   - HM-CC-RT-DN on channel 4 → climate_rf
//     · set_temperature {temperature: 19.0} → SET_TEMPERATURE=19.0
//     · set_mode {mode: auto}              → AUTO_MODE fires (action)
//     · set_mode {mode: heat}              → MANU_MODE writes the manu setpoint
//     · enable_boost / disable_boost       → BOOST_MODE action

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestSPAE2E_Climate_HmIP_BWTH(t *testing.T) {
	h := newSPAHarness(t, []string{"HmIP-BWTH"})

	plan := spaPlan{
		name:     "climate_hmip_BWTH",
		model:    "HmIP-BWTH",
		chNo:     1,
		wantKind: "climate_hmip",
		actions: []spaAction{
			{
				op:     "set_temperature",
				params: map[string]any{"temperature": 21.5},
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterSetPointTemperature: 21.5,
				},
			},
			{
				op:     "set_mode",
				params: map[string]any{"mode": "auto"},
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterControlMode: 0,
				},
			},
			{
				op:     "set_mode",
				params: map[string]any{"mode": "heat"},
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterControlMode: 1,
				},
			},
			{
				op:     "set_mode",
				params: map[string]any{"mode": "off"},
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterControlMode:         1,
					hmenum.ParameterSetPointTemperature: 4.5,
				},
			},
			{
				op: "enable_boost",
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterBoostMode: true,
				},
			},
			// Mode switch while BOOST is active must bundle BOOST_MODE=false
			// with CONTROL_MODE in a single put_paramset envelope — the CCU
			// rejects an isolated CONTROL_MODE=0 when BOOST is still on
			// (the "set_mode auto" 502 we hunted down via SPA).
			{
				op:     "set_mode",
				params: map[string]any{"mode": "auto"},
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterBoostMode:   false,
					hmenum.ParameterControlMode: 0,
				},
			},
			{
				op: "disable_boost",
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterBoostMode: false,
				},
			},
		},
	}
	plan.execute(t, h)
}

func TestSPAE2E_Climate_RF_HMCCRTDN(t *testing.T) {
	h := newSPAHarness(t, []string{"HM-CC-RT-DN"})

	plan := spaPlan{
		name:     "climate_rf_HM-CC-RT-DN",
		model:    "HM-CC-RT-DN",
		chNo:     4,
		wantKind: "climate_rf",
		actions: []spaAction{
			{
				op:     "set_temperature",
				params: map[string]any{"temperature": 19.0},
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterSetTemperature: 19.0,
				},
			},
			// AUTO / BOOST writes are ACTION-typed on the RF
			// thermostat — the wire side echoes them by flipping
			// CONTROL_MODE on the next status update. Asserting the
			// ACTION write itself doesn't read back via getValue
			// (the wire DP is write-only), so we accept any
			// successful return as evidence that the dispatcher
			// reached the SetValue path.
			{op: "set_mode", params: map[string]any{"mode": "auto"}},
			{op: "set_mode", params: map[string]any{"mode": "heat"}},
			{op: "enable_boost"},
			{op: "disable_boost"},
		},
	}
	plan.execute(t, h)
}
