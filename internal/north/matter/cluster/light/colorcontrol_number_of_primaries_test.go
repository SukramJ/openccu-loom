// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light_test

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/light"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
)

// TestColorControlServer_NumberOfPrimaries_Null verifies that
// ColorControlServer.MatterRead for NumberOfPrimaries (0x0010) returns
// (nil, true) — i.e. a null value with present=true. NumberOfPrimaries is
// mandatory per Matter §3.2.6.6 with Quality X (nullable); a CT-only device
// has no primary colours so the spec-correct encoding is null.
func TestColorControlServer_NumberOfPrimaries_Null(t *testing.T) {
	t.Parallel()
	srv := light.NewColorControlServer(light.DefaultColorControlServerConfig())

	val, ok := srv.MatterRead(wire.ColorCtrlAttrNumberOfPrimaries)
	if !ok {
		t.Fatal("MatterRead(NumberOfPrimaries 0x0010) returned ok=false; attribute must be present (mandatory, Quality X)")
	}
	if val != nil {
		t.Errorf("MatterRead(NumberOfPrimaries 0x0010) = %v (%T), want nil (null for CT-only)", val, val)
	}
}

// TestColorControlServer_NumberOfPrimaries_InAttributeList verifies that
// NumberOfPrimaries appears in MatterAttributes() so wildcard-reads and
// AttributeList (0xFFFB) enumeration include it.
func TestColorControlServer_NumberOfPrimaries_InAttributeList(t *testing.T) {
	t.Parallel()
	srv := light.NewColorControlServer(light.DefaultColorControlServerConfig())

	found := slices.Contains(srv.MatterAttributes(), wire.ColorCtrlAttrNumberOfPrimaries)
	if !found {
		t.Errorf("MatterAttributes() missing NumberOfPrimaries (0x%04X)", wire.ColorCtrlAttrNumberOfPrimaries)
	}
}
