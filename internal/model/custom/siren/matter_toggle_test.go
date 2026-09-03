// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package siren

import (
	"slices"
	"testing"
)

// TestOnOffToggleIsAdvertisedAndAccepted pins Toggle onto the siren's OnOff
// projection.
//
// matter.js declares it `Command({ name: "Toggle", id: 0x2, access: "O",
// conformance: "!OFFONLY" })` (on-off.element.ts:39): mandatory unless the
// cluster advertises the OffOnly feature. This projection advertises LT
// (0x01) and not OffOnly, so Toggle is mandatory for it — the comment that
// justified its absence argued from the device's wire surface, which
// conformance does not ask about. Our three other OnOff projections all carry
// it, so a controller met one cluster that answered a mandatory command with
// an error, which is the shape that ends a commissioning in a pair-abort.
func TestOnOffToggleIsAdvertisedAndAccepted(t *testing.T) {
	t.Parallel()

	srv := sirenOnOffServer{}
	if !slices.Contains(srv.MatterAcceptedCommands(), matterCmdToggle) {
		t.Errorf("AcceptedCommandList = %v, want Toggle (0x02): mandatory while OffOnly is unset",
			srv.MatterAcceptedCommands())
	}
	// The premise: this projection advertises LT and nothing else, so the
	// OffOnly feature that would make Toggle optional is not set.
	if matterFeatureOnOffLT != 0x01 {
		t.Errorf("LT feature bit = %#x, want 0x01", matterFeatureOnOffLT)
	}
}
