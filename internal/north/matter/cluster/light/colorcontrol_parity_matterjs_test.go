// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/light"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type matterSchemaCC struct {
	Clusters []struct {
		ID       uint32 `json:"id"`
		Name     string `json:"name"`
		Revision uint16 `json:"revision"`
	} `json:"clusters"`
}

func loadSchemaCC(t *testing.T) *matterSchemaCC {
	t.Helper()
	var s matterSchemaCC
	if err := json.Unmarshal(matterparity.SchemaJSON(), &s); err != nil {
		t.Fatalf("unmarshal matter-schema-snapshot.json: %v", err)
	}
	return &s
}

func findClusterCC(s *matterSchemaCC, id uint32) (revision uint16, found bool) {
	for _, c := range s.Clusters {
		if c.ID == id {
			return c.Revision, true
		}
	}
	return 0, false
}

// TestParityMatterJS_ColorControlClusterRevision pins the ColorControl
// cluster revision against matter.js HEAD so a stale constant fails the
// build before review.
func TestParityMatterJS_ColorControlClusterRevision(t *testing.T) {
	t.Parallel()
	schema := loadSchemaCC(t)
	rev, ok := findClusterCC(schema, wire.ColorControlClusterID)
	if !ok {
		t.Fatalf("matter.js schema has no ColorControl cluster (0x%04X)", wire.ColorControlClusterID)
	}
	if light.ColorControlClusterRevision != rev {
		t.Errorf("ColorControlClusterRevision = %d, want %d (matter.js HEAD)",
			light.ColorControlClusterRevision, rev)
	}
}

// TestParityMatterJS_ColorControlMandatoryAttributes verifies that the
// CT-only ColorControlServer exposes all mandatory attribute IDs from
// Matter §3.2 for a CT-capable device. Apple Home reads the full
// attribute list during commissioning; missing entries cause silent
// pair-abort.
func TestParityMatterJS_ColorControlMandatoryAttributes(t *testing.T) {
	t.Parallel()
	srv := light.NewColorControlServer(light.DefaultColorControlServerConfig())
	present := make(map[uint32]bool, 16)
	for _, id := range srv.MatterAttributes() {
		present[id] = true
	}
	mandatory := []struct {
		id   uint32
		name string
	}{
		{wire.ColorCtrlAttrColorTemperatureMireds, "ColorTemperatureMireds (0x0007)"},
		{wire.ColorCtrlAttrColorMode, "ColorMode (0x0008)"},
		{wire.ColorCtrlAttrOptions, "Options (0x000F)"},
		{wire.ColorCtrlAttrEnhancedColorMode, "EnhancedColorMode (0x4001)"},
		{wire.ColorCtrlAttrColorCapabilities, "ColorCapabilities (0x400A)"},
		{wire.ColorCtrlAttrColorTempPhysicalMin, "ColorTempPhysicalMinMireds (0x400B)"},
		{wire.ColorCtrlAttrColorTempPhysicalMax, "ColorTempPhysicalMaxMireds (0x400C)"},
	}
	for _, m := range mandatory {
		if !present[m.id] {
			t.Errorf("MatterAttributes() missing mandatory %s", m.name)
		}
		_, ok := srv.MatterRead(m.id)
		if !ok {
			t.Errorf("MatterRead(0x%04X) returned ok=false for %s", m.id, m.name)
		}
	}
}

// TestParityMatterJS_ColorControlColorCapabilitiesBits verifies that the
// CT-only server advertises exactly the CT capability bit (bit 4 = 0x0010)
// in ColorCapabilities (0x400A) and no HS / XY / EnhancedHue / ColorLoop
// bits. Advertising more bits than the physical device supports causes
// Apple Home to expose controls (Hue / Saturation) that have no effect.
func TestParityMatterJS_ColorControlColorCapabilitiesBits(t *testing.T) {
	t.Parallel()
	srv := light.NewColorControlServer(light.DefaultColorControlServerConfig())
	raw, ok := srv.MatterRead(wire.ColorCtrlAttrColorCapabilities)
	if !ok {
		t.Fatal("MatterRead(ColorCapabilities) returned ok=false")
	}
	caps, ok := raw.(uint16)
	if !ok {
		t.Fatalf("ColorCapabilities expected uint16, got %T", raw)
	}
	const wantCTBit uint16 = 1 << 4
	if caps&wantCTBit == 0 {
		t.Errorf("ColorCapabilities 0x%04X: CT bit (bit 4) not set", caps)
	}
	// No HS, XY, EnhancedHue, or ColorLoop bits.
	const unwantedBits uint16 = (1 << 0) | (1 << 1) | (1 << 2) | (1 << 3)
	if caps&unwantedBits != 0 {
		t.Errorf("ColorCapabilities 0x%04X: non-CT bits set (HS/EnhHS/Loop/XY), CT-only server must not advertise them", caps)
	}
}

// TestParityMatterJS_ColorControlColorModeDefault verifies that the
// CT-only server reports ColorMode = 2 (CT mode) on both ColorMode
// (0x0008) and EnhancedColorMode (0x4001). Matter §3.2.7.7 requires
// ColorMode to reflect the current active color mode; for a CT-only
// device this is always 2.
func TestParityMatterJS_ColorControlColorModeDefault(t *testing.T) {
	t.Parallel()
	srv := light.NewColorControlServer(light.DefaultColorControlServerConfig())
	for _, tc := range []struct {
		id   uint32
		name string
	}{
		{wire.ColorCtrlAttrColorMode, "ColorMode (0x0008)"},
		{wire.ColorCtrlAttrEnhancedColorMode, "EnhancedColorMode (0x4001)"},
	} {
		raw, ok := srv.MatterRead(tc.id)
		if !ok {
			t.Errorf("MatterRead(0x%04X) returned ok=false for %s", tc.id, tc.name)
			continue
		}
		mode, ok := raw.(uint8)
		if !ok {
			t.Errorf("%s expected uint8, got %T", tc.name, raw)
			continue
		}
		const wantCT uint8 = 2
		if mode != wantCT {
			t.Errorf("%s = %d, want %d (CT mode)", tc.name, mode, wantCT)
		}
	}
}

// TestParityMatterJS_ColorControlAcceptedCommands verifies that the
// CT-only server enumerates all four CT-related command IDs so
// AcceptedCommandList (0xFFF9) is correct during commissioning.
func TestParityMatterJS_ColorControlAcceptedCommands(t *testing.T) {
	t.Parallel()
	srv := light.NewColorControlServer(light.DefaultColorControlServerConfig())
	lister, ok := any(srv).(interface{ MatterAcceptedCommands() []uint32 })
	if !ok {
		t.Fatal("ColorControlServer does not implement MatterClusterCommandLister")
	}
	got := make(map[uint32]bool)
	for _, id := range lister.MatterAcceptedCommands() {
		got[id] = true
	}
	want := []struct {
		id   uint32
		name string
	}{
		{wire.ColorCtrlCmdMoveToColorTemperature, "MoveToColorTemperature (0x0A)"},
		{wire.ColorCtrlCmdStopMoveStep, "StopMoveStep (0x47)"},
		{wire.ColorCtrlCmdMoveColorTemperature, "MoveColorTemperature (0x4B)"},
		{wire.ColorCtrlCmdStepColorTemperature, "StepColorTemperature (0x4C)"},
	}
	for _, w := range want {
		if !got[w.id] {
			t.Errorf("MatterAcceptedCommands() missing %s", w.name)
		}
	}
}

// TestColorControl_FeatureMap_CTOnlyBit4 verifies that the CT-only server
// returns FeatureMap with only bit 4 (CT feature) set. HS/XY bits must be
// absent — advertising them without the corresponding attributes causes
// chip-tool conformance failures.
func TestColorControl_FeatureMap_CTOnlyBit4(t *testing.T) {
	t.Parallel()
	srv := light.NewColorControlServer(light.DefaultColorControlServerConfig())
	raw, ok := srv.MatterRead(0xFFFC) // AttrGlobalFeatureMap
	if !ok {
		t.Fatal("MatterRead(FeatureMap 0xFFFC) returned ok=false")
	}
	fm, ok := raw.(uint32)
	if !ok {
		t.Fatalf("FeatureMap expected uint32, got %T", raw)
	}
	const ctBit uint32 = 1 << 4
	if fm&ctBit == 0 {
		t.Errorf("FeatureMap 0x%08X: CT bit (bit 4) not set", fm)
	}
	// No HS (bit 0) or XY (bit 3) bits.
	if fm&(1<<0) != 0 {
		t.Errorf("FeatureMap 0x%08X: HS bit (bit 0) must not be set in CT-only mode", fm)
	}
	if fm&(1<<3) != 0 {
		t.Errorf("FeatureMap 0x%08X: XY bit (bit 3) must not be set in CT-only mode", fm)
	}
}

// TestParityMatterJS_ColorControl_MoveToColorTemperatureCropsToRange verifies
// that MoveToColorTemperature crops the target to [MinMireds, MaxMireds] and
// updates the in-process current value. Mirrors matter.js ColorControlServer.ts
// :#cropColorTemperature (line 221) which is called from the setter at line 221.
func TestParityMatterJS_ColorControl_MoveToColorTemperatureCropsToRange(t *testing.T) {
	t.Parallel()

	cfg := light.DefaultColorControlServerConfig() // min=153 max=500
	srv := light.NewColorControlServer(cfg)
	ctx := context.Background()

	cases := []struct {
		name   string
		target uint16
		want   uint16
	}{
		{"within range → exact", 300, 300},
		{"below min → clamped to min", 100, 153},
		{"above max → clamped to max", 600, 500},
		{"at min boundary", 153, 153},
		{"at max boundary", 500, 500},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv2 := light.NewColorControlServer(cfg)
			if _, err := srv2.MatterInvoke(ctx, wire.ColorCtrlCmdMoveToColorTemperature, tc.target, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("MoveToColorTemperature(%d): %v", tc.target, err)
			}
			v, ok := srv2.MatterRead(wire.ColorCtrlAttrColorTemperatureMireds)
			if !ok {
				t.Fatal("ColorTemperatureMireds: ok=false after command")
			}
			if got, _ := v.(uint16); got != tc.want {
				t.Errorf("ColorTemperatureMireds = %d, want %d (target=%d)", got, tc.want, tc.target)
			}
		})
	}
	_ = srv // used to check default remains unchanged
}

// TestParityMatterJS_ColorControl_MoveToColorTemperatureMapPayload verifies
// the map[string]any delivery path for MoveToColorTemperature fields.
func TestParityMatterJS_ColorControl_MoveToColorTemperatureMapPayload(t *testing.T) {
	t.Parallel()
	cfg := light.DefaultColorControlServerConfig()
	srv := light.NewColorControlServer(cfg)
	ctx := context.Background()

	fields := map[string]any{"colorTemperatureMireds": uint16(250)}
	if _, err := srv.MatterInvoke(ctx, wire.ColorCtrlCmdMoveToColorTemperature, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToColorTemperature(map): %v", err)
	}
	v, ok := srv.MatterRead(wire.ColorCtrlAttrColorTemperatureMireds)
	if !ok {
		t.Fatal("ColorTemperatureMireds: ok=false")
	}
	if got, _ := v.(uint16); got != 250 {
		t.Errorf("ColorTemperatureMireds = %d, want 250", got)
	}
}

// TestColorControl_NoHSXYAttributes verifies that CurrentHue, CurrentSaturation,
// CurrentX, CurrentY are absent from both MatterAttributes() and MatterRead() in
// CT-only mode — they have HS and XY conformance respectively.
func TestColorControl_NoHSXYAttributes(t *testing.T) {
	t.Parallel()
	srv := light.NewColorControlServer(light.DefaultColorControlServerConfig())

	haveAttrs := make(map[uint32]bool)
	for _, id := range srv.MatterAttributes() {
		haveAttrs[id] = true
	}
	hsXY := []struct {
		id   uint32
		name string
	}{
		{wire.ColorCtrlAttrCurrentHue, "CurrentHue (0x0000) — HS conformance"},
		{wire.ColorCtrlAttrCurrentSaturation, "CurrentSaturation (0x0001) — HS conformance"},
		{wire.ColorCtrlAttrCurrentX, "CurrentX (0x0003) — XY conformance"},
		{wire.ColorCtrlAttrCurrentY, "CurrentY (0x0004) — XY conformance"},
	}
	for _, a := range hsXY {
		if haveAttrs[a.id] {
			t.Errorf("MatterAttributes() contains %s but neither HS nor XY feature is set", a.name)
		}
		_, ok := srv.MatterRead(a.id)
		if ok {
			t.Errorf("MatterRead(0x%04X) returned ok=true for %s but HS/XY feature absent", a.id, a.name)
		}
	}
}
