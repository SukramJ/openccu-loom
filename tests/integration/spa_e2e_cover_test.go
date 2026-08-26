// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

// SPA-E2E plans for the Cover tile family.
//
//   - HmIP-FBL on channel 4 → cover_blind
//     IP-Blind wire shape: COMBINED_PARAMETER="L2=<tilt_pct>,L=<level_pct>"
//     (single string write per command).
//     · open  → "L2=0,L=100"
//     · set_position {position: 0.5} → "L2=0,L=50"
//     · set_tilt {tilt: 0.3}         → "L2=30,L=50"
//     · close → "L2=30,L=0"
//     · stop  → (no error; STOP is write-only action)
//
//   - HM-LC-Bl1-FM on channel 1 → cover (no tilt)
//     · open, set_position {position: 0.7}, close, stop
//
//   - HmIP-MOD-HO on channel 1 → cover_garage (write-only DOOR_COMMAND)
//     · open, close, stop, ventilate — accepted as long as no error

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestSPAE2E_Cover_Blind_HmIPFBL(t *testing.T) {
	h := newSPAHarness(t, []string{"HmIP-FBL"})

	plan := spaPlan{
		name:     "cover_blind_HmIP-FBL",
		model:    "HmIP-FBL",
		chNo:     4,
		wantKind: "cover_blind",
		actions: []spaAction{
			{
				op: "open",
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterCombinedParameter: "L2=0,L=100",
				},
			},
			{
				op:     "set_position",
				params: map[string]any{"position": 0.5},
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterCombinedParameter: "L2=0,L=50",
				},
			},
			{
				op:     "set_tilt",
				params: map[string]any{"tilt": 0.3},
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterCombinedParameter: "L2=30,L=50",
				},
			},
			{
				op: "close",
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterCombinedParameter: "L2=30,L=0",
				},
			},
			// STOP is a write-only action; read-back via getValue is not
			// meaningful. Accept any successful dispatch return.
			{op: "stop"},
		},
	}
	plan.execute(t, h)
}

func TestSPAE2E_Cover_RfBlind_HMLCBl1FM(t *testing.T) {
	h := newSPAHarness(t, []string{"HM-LC-Bl1-FM"})

	plan := spaPlan{
		name:     "cover_HM-LC-Bl1-FM",
		model:    "HM-LC-Bl1-FM",
		chNo:     1,
		wantKind: "cover",
		actions: []spaAction{
			{
				op: "open",
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterLevel: 1.0,
				},
			},
			{
				op:     "set_position",
				params: map[string]any{"position": 0.7},
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterLevel: 0.7,
				},
			},
			{
				op: "close",
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterLevel: 0.0,
				},
			},
			// STOP is a write-only action on RF covers.
			{op: "stop"},
		},
	}
	plan.execute(t, h)
}

func TestSPAE2E_Cover_Garage_HmIPMODHO(t *testing.T) {
	h := newSPAHarness(t, []string{"HmIP-MOD-HO"})
	plan := spaPlan{
		name:     "cover_garage_HmIP-MOD-HO",
		model:    "HmIP-MOD-HO",
		chNo:     1,
		wantKind: "cover_garage",
		actions: []spaAction{
			// All four Garage commands are write-only ACTION dispatches —
			// godevccu fires the matching event but does not retain a
			// readable value via getValue (DOOR_COMMAND is write-only).
			// The plan accepts a successful dispatch as evidence and
			// leaves the wire side to the ACTION-echo path validated by
			// godevccu's PutParamset response.
			{op: "open"},
			{op: "close"},
			{op: "stop"},
			{op: "ventilate"},
		},
	}
	plan.execute(t, h)
}
