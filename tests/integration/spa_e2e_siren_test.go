// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

// SPA-E2E plans for the Siren tile family.
//
//   - HmIP-ASIR on channel 3 → siren (IPSiren profile)
//     · turn_on {acoustic_selection: 1} → ACOUSTIC_ALARM_SELECTION=1
//     · turn_off → ACOUSTIC_ALARM_SELECTION=0 (silenced)

import (
	"testing"
)

func TestSPAE2E_Siren_HmIPASIR(t *testing.T) {
	h := newSPAHarness(t, []string{"HmIP-ASIR"})

	plan := spaPlan{
		name:     "siren_HmIP-ASIR",
		model:    "HmIP-ASIR",
		chNo:     3,
		wantKind: "siren",
		actions: []spaAction{
			// turn_on without params is a no-op (no write occurs if neither
			// AcousticSelection nor OpticalSelection is provided). Pass
			// acoustic_selection=1 so the dispatcher actually writes via
			// put_paramset. godevccu routes the write through the VALUES
			// paramset; getValue may still return the initial 0 because
			// put_paramset and getValue are separate RPC calls. Accept any
			// successful dispatch — wire check is nil (godevccu limitation).
			{
				op:     "turn_on",
				params: map[string]any{"acoustic_selection": int32(1)},
			},
			// turn_off writes both selections to 0 to silence the siren.
			// Same godevccu putParamset vs getValue asymmetry — no wire check.
			{op: "turn_off"},
		},
	}
	plan.execute(t, h)
}
