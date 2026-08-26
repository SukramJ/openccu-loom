// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/cover"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
)

// TestParity_WindowCovering_ConfigStatus_Bitmap verifies that
// ConfigStatus (0x0007) advertises bit 0 (Operational) and bit 2
// (LiftPositionAware) — the minimum bitmap for a position-aware
// lift-only cover. Mirrors matter.js
// packages/node/src/behaviors/window-covering/WindowCoveringServer.ts:
// configStatus initial value 0x05 when LF+PA_LF features are active.
func TestParity_WindowCovering_ConfigStatus_Bitmap(t *testing.T) {
	t.Parallel()
	srv := cover.NewWindowCoveringServer(cover.Config{
		Type:           0,
		EndProductType: 0,
		FeatureMap:     0x05, // LF (bit 0) + PA_LF (bit 2)
	})

	v, ok := srv.MatterRead(wire.WindowCoveringAttrConfigStatus)
	if !ok {
		t.Fatal("ConfigStatus: ok=false")
	}
	got := v.(uint8)
	const (
		bitOperational   uint8 = 1 << 0 // bit 0 per Matter §5.3.6.7
		bitLiftPosAware  uint8 = 1 << 2 // bit 2
		wantConfigStatus       = bitOperational | bitLiftPosAware
	)
	if got&wantConfigStatus != wantConfigStatus {
		t.Errorf("ConfigStatus = 0x%02X, want bits 0x%02X set", got, wantConfigStatus)
	}
}
