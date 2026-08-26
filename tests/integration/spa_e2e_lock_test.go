// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

// SPA-E2E plans for the Lock tile family.
//
//   - HmIP-DLD on channel 1 → lock (IPLock profile, SupportsOpen=true)
//     · lock   → LOCK_TARGET_LEVEL="LOCKED"
//     · unlock → LOCK_TARGET_LEVEL="UNLOCKED"
//     · open   → LOCK_TARGET_LEVEL="OPEN" (short-time unlock action)
//
// LOCK_TARGET_LEVEL is an HmIP ENUM whose descriptor carries a string MIN
// ("LOCKED"), so the wire value is the enum *label*, not its integer index
// — the reference stack sends the index only for classic-HM ENUMs whose MIN
// is an integer. The parameter is write-only (OPERATIONS=2), so getValue is
// not meaningful; the plan runner falls back to the captured setValue.

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestSPAE2E_Lock_HmIPDLD(t *testing.T) {
	h := newSPAHarness(t, []string{"HmIP-DLD"})

	dp, ch := h.findCustomDP("HmIP-DLD", 1)
	if dp == nil {
		t.Skip("HmIP-DLD CDP not attached on channel 1; investigate godevccu profile")
	}
	_ = ch

	plan := spaPlan{
		name:     "lock_HmIP-DLD",
		model:    "HmIP-DLD",
		chNo:     1,
		wantKind: "lock",
		actions: []spaAction{
			{
				op: "lock",
				// IP locks write the LOCK_TARGET_LEVEL enum label.
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterLockTargetLevel: "LOCKED",
				},
			},
			{
				op: "unlock",
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterLockTargetLevel: "UNLOCKED",
				},
			},
			{
				op: "open",
				wantWire: map[hmenum.Parameter]any{
					hmenum.ParameterLockTargetLevel: "OPEN",
				},
			},
		},
	}
	plan.execute(t, h)
}
