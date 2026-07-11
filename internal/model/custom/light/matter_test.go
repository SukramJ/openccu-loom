// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestMatterDeviceTypeDimmable maps a dimmable Light to
// DimmableLight (0x0101).
func TestMatterDeviceTypeDimmable(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	if got := l.MatterDeviceType(); got != 0x0101 {
		t.Fatalf("dimmable MatterDeviceType = 0x%04X, want 0x0101", got)
	}
}

// TestMatterDeviceTypeNonDimmable maps a non-dimmable Light to
// OnOffLight (0x0100).
func TestMatterDeviceTypeNonDimmable(t *testing.T) {
	l, _ := newLightRig(t, "HM-LC-Sw:1", &stubWriter{}, custom.LightCapabilities{})
	if got := l.MatterDeviceType(); got != 0x0100 {
		t.Fatalf("non-dimmable MatterDeviceType = 0x%04X, want 0x0100", got)
	}
}

// TestMatterClusterServersDimmable expects OnOff (0x0006),
// LevelControl (0x0008), Groups (0x0004) and ScenesManagement (0x0062)
// on a dimmable light. Groups + ScenesManagement are mandatory on
// OnOffLight / DimmableLight per Matter §5.3 +
// matter.js packages/node/src/devices/on-off-light.ts.
func TestMatterClusterServersDimmable(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	servers := l.MatterClusterServers()
	if len(servers) != 4 {
		t.Fatalf("dimmable: %d cluster servers, want 4 (OnOff + LevelControl + Groups + ScenesManagement)", len(servers))
	}
	got := map[uint32]bool{}
	for _, s := range servers {
		got[s.MatterClusterID()] = true
	}
	if !got[0x0006] || !got[0x0008] || !got[0x0004] || !got[0x0062] {
		t.Fatalf("dimmable cluster set = %v, want {0x0006, 0x0008, 0x0004, 0x0062}", got)
	}
}

// TestMatterClusterServersNonDimmable expects OnOff (0x0006),
// Groups (0x0004) and ScenesManagement (0x0062) on a non-dimmable
// light. Both are mandatory per matter.js
// packages/node/src/devices/on-off-light.ts.
func TestMatterClusterServersNonDimmable(t *testing.T) {
	l, _ := newLightRig(t, "HM-LC-Sw:1", &stubWriter{}, custom.LightCapabilities{})
	servers := l.MatterClusterServers()
	if len(servers) != 3 {
		t.Fatalf("non-dimmable: %d cluster servers, want 3 (OnOff + Groups + ScenesManagement)", len(servers))
	}
	got := map[uint32]bool{}
	for _, s := range servers {
		got[s.MatterClusterID()] = true
	}
	if !got[0x0006] || !got[0x0004] || !got[0x0062] {
		t.Fatalf("non-dimmable cluster set = %v, want {0x0006, 0x0004, 0x0062}", got)
	}
}

// onOffServer returns the OnOff cluster server for a dimmable light.
// Helper to keep tests focused.
func onOffServer(t *testing.T, l *Light) lightOnOffServer {
	t.Helper()
	for _, s := range l.MatterClusterServers() {
		if onoff, ok := s.(lightOnOffServer); ok {
			return onoff
		}
	}
	t.Fatalf("light has no OnOff cluster server")
	return lightOnOffServer{}
}

// levelServer returns the LevelControl cluster server for a dimmable
// light.
func levelServer(t *testing.T, l *Light) lightLevelServer {
	t.Helper()
	for _, s := range l.MatterClusterServers() {
		if lvl, ok := s.(lightLevelServer); ok {
			return lvl
		}
	}
	t.Fatalf("light has no LevelControl cluster server")
	return lightLevelServer{}
}

// TestOnOffReadUnobserved returns (nil, true) on an unobserved light —
// attribute is supported but no LEVEL has been observed yet — the bridge
// surfaces the boolean default false rather than TLV null because Apple
// Home's HAP-mapper aborts the service rebuild on a null OnOff and the
// bridge does not enumerate OnOff as a nullable attribute.
func TestOnOffReadUnobserved(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	v, ok := srv.MatterRead(0x0000)
	if !ok || v != false {
		t.Fatalf("MatterRead(OnOff) on unobserved = (%v, %v), want (false, true)", v, ok)
	}
}

// TestOnOffFeatureMapAdvertisesLighting verifies the OnOff cluster
// advertises the LT (Lighting) feature bit 0 (0x01) — mandatory on
// OnOffLight / DimmableLight. matter.js on-off.element.ts:24.
func TestOnOffFeatureMapAdvertisesLighting(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	v, ok := srv.MatterRead(0xFFFC)
	if !ok {
		t.Fatal("MatterRead(FeatureMap) ok=false")
	}
	if v.(uint32)&0x01 == 0 {
		t.Fatalf("OnOff FeatureMap = 0x%08X, missing LT bit (0x01)", v.(uint32))
	}
}

// TestOnOffLightingGatedAttributes verifies the four LT-mandatory OnOff
// attributes read with their matter.js-default values:
// GlobalSceneControl=true, OnTime=0, OffWaitTime=0, StartUpOnOff=null.
// matter.js OnOffServer.ts:39,75,80,102.
func TestOnOffLightingGatedAttributes(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	if v, ok := srv.MatterRead(0x4000); !ok || v != true {
		t.Fatalf("GlobalSceneControl = (%v, %v), want (true, true)", v, ok)
	}
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 0 {
		t.Fatalf("OnTime = (%v, %v), want (0, true)", v, ok)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 0 {
		t.Fatalf("OffWaitTime = (%v, %v), want (0, true)", v, ok)
	}
	if v, ok := srv.MatterRead(0x4003); !ok || v != nil {
		t.Fatalf("StartUpOnOff = (%v, %v), want (nil/null, true)", v, ok)
	}
}

// TestOnOffAttributesAndCommandsEnumerateLightingGated confirms the
// LT-mandatory attributes and commands are enumerated for conformance.
func TestOnOffAttributesAndCommandsEnumerateLightingGated(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	attrs := map[uint32]bool{}
	for _, a := range srv.MatterAttributes() {
		attrs[a] = true
	}
	for _, want := range []uint32{0x4000, 0x4001, 0x4002, 0x4003} {
		if !attrs[want] {
			t.Errorf("MatterAttributes missing LT-gated 0x%04X", want)
		}
	}
	cmds := map[uint32]bool{}
	for _, c := range srv.MatterAcceptedCommands() {
		cmds[c] = true
	}
	for _, want := range []uint32{0x40, 0x41, 0x42} {
		if !cmds[want] {
			t.Errorf("MatterAcceptedCommands missing LT-gated 0x%02X", want)
		}
	}
}

// TestOnOffLightingGatedCommandsAccepted verifies the three LT-mandatory
// commands route to plain On/Off without error.
func TestOnOffLightingGatedCommandsAccepted(t *testing.T) {
	cases := []struct {
		cmd     uint32
		wantOn  bool
		wantVal float64
	}{
		{0x40, false, 0.0}, // OffWithEffect → Off
		{0x41, true, 0.5},  // OnWithRecallGlobalScene → On (restores last level)
		{0x42, true, 0.5},  // OnWithTimedOff → On
	}
	for _, tc := range cases {
		w := &stubWriter{}
		l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
		l.OnLevel(0.5)
		l.OnLevel(0.0) // current goes to 0; LastLevel retains 0.5
		srv := onOffServer(t, l)
		if _, err := srv.MatterInvoke(context.Background(), tc.cmd, nil, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("MatterInvoke(0x%02X) error: %v", tc.cmd, err)
		}
		if w.last != tc.wantVal {
			t.Fatalf("cmd 0x%02X wrote %v, want %v", tc.cmd, w.last, tc.wantVal)
		}
	}
}

// TestGlobalSceneControlLifecycle verifies GlobalSceneControl (0x4000)
// is live state, not the hardcoded constant it used to be: it reads
// true initially, stays true after a plain On, flips to false after
// OffWithEffect, is left unchanged by a subsequent plain Off, and
// reverts to true on a following On. The value also survives a
// MatterClusterServers reconstruction — the lightOnOffServer
// projection is rebuilt fresh on every call, but the flag lives on the
// long-lived Light (see timedOnOffState in matter_timed_onoff.go).
// Mirrors matter.js packages/node/src/behaviors/on-off/OnOffServer.ts:
// 97-104 (on), :119-139 (off — GlobalSceneControl untouched),
// :158-169 (offWithEffect).
func TestGlobalSceneControlLifecycle(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5)
	srv := onOffServer(t, l)

	if v, ok := srv.MatterRead(0x4000); !ok || v != true {
		t.Fatalf("initial GlobalSceneControl = (%v, %v), want (true, true)", v, ok)
	}

	if _, err := srv.MatterInvoke(context.Background(), 0x01, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("On error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4000); !ok || v != true {
		t.Fatalf("GlobalSceneControl after On = (%v, %v), want (true, true)", v, ok)
	}

	if _, err := srv.MatterInvoke(context.Background(), 0x40, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("OffWithEffect error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4000); !ok || v != false {
		t.Fatalf("GlobalSceneControl after OffWithEffect = (%v, %v), want (false, true)", v, ok)
	}

	if _, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("plain Off error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4000); !ok || v != false {
		t.Fatalf("GlobalSceneControl after plain Off = (%v, %v), want (false, true) — a plain Off must not change it", v, ok)
	}

	if _, err := srv.MatterInvoke(context.Background(), 0x01, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("second On error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4000); !ok || v != true {
		t.Fatalf("GlobalSceneControl after second On = (%v, %v), want (true, true)", v, ok)
	}

	// The cluster-server projection is rebuilt fresh on every
	// MatterClusterServers call; the flag must still read back true.
	fresh := onOffServer(t, l)
	if v, ok := fresh.MatterRead(0x4000); !ok || v != true {
		t.Fatalf("GlobalSceneControl after MatterClusterServers reconstruction = (%v, %v), want (true, true)", v, ok)
	}
}

// TestGlobalSceneControlOnWithRecallGlobalSceneSetsTrue verifies
// OnWithRecallGlobalScene (0x41) sets GlobalSceneControl true, matching
// the plain On path. matter.js OnOffServer.ts:171-191.
func TestGlobalSceneControlOnWithRecallGlobalSceneSetsTrue(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5)
	srv := onOffServer(t, l)

	if _, err := srv.MatterInvoke(context.Background(), 0x40, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("OffWithEffect error: %v", err)
	}
	if v, _ := srv.MatterRead(0x4000); v != false {
		t.Fatalf("precondition: GlobalSceneControl = %v, want false", v)
	}

	if _, err := srv.MatterInvoke(context.Background(), 0x41, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("OnWithRecallGlobalScene error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4000); !ok || v != true {
		t.Fatalf("GlobalSceneControl after OnWithRecallGlobalScene = (%v, %v), want (true, true)", v, ok)
	}
}

// TestGlobalSceneControlOnWithTimedOffGatedNoOpLeavesUnchanged verifies
// the AcceptOnlyWhenOn gate on OnWithTimedOff (0x42): when the device
// is off and the gate rejects the command, no on() runs, so
// GlobalSceneControl must stay at its pre-call value instead of being
// flipped true by a command that never actually turned the light on.
// matter.js OnOffServer.ts:199-201 returns before the :101-104 GSC
// flip inside on() ever runs.
func TestGlobalSceneControlOnWithTimedOffGatedNoOpLeavesUnchanged(t *testing.T) {
	l := timedRig(t, &stubWriter{})
	l.OnLevel(0.0)
	l.matterClearGlobalSceneControl()
	srv := onOffServer(t, l)

	gated := map[uint8]any{0: uint64(1), 1: uint64(5), 2: uint64(5)} // AcceptOnlyWhenOn bit set
	if _, err := srv.MatterInvoke(context.Background(), 0x42, gated, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("gated OnWithTimedOff error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4000); !ok || v != false {
		t.Fatalf("GlobalSceneControl after gated no-op = (%v, %v), want (false, true) unchanged", v, ok)
	}
}

// TestLevelFeatureMapAdvertisesOnOffAndLighting verifies LevelControl
// advertises OO (bit 0) | LT (bit 1) = 0x03. DimmableLight mandates LT
// on LevelControl. matter.js level-control.element.ts:24-25.
func TestLevelFeatureMapAdvertisesOnOffAndLighting(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	v, ok := srv.MatterRead(0xFFFC)
	if !ok {
		t.Fatal("MatterRead(FeatureMap) ok=false")
	}
	got := v.(uint32)
	if got&0x01 == 0 {
		t.Errorf("LevelControl FeatureMap = 0x%08X, missing OO bit (0x01)", got)
	}
	if got&0x02 == 0 {
		t.Errorf("LevelControl FeatureMap = 0x%08X, missing LT bit (0x02)", got)
	}
}

// TestLevelLightingGatedAttributes verifies the two LT-mandatory
// LevelControl attributes read with matter.js defaults: RemainingTime=0,
// StartUpCurrentLevel=null. matter.js level-control.element.ts:33,67.
func TestLevelLightingGatedAttributes(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	if v, ok := srv.MatterRead(0x0001); !ok || v.(uint16) != 0 {
		t.Fatalf("RemainingTime = (%v, %v), want (0, true)", v, ok)
	}
	if v, ok := srv.MatterRead(0x4000); !ok || v != nil {
		t.Fatalf("StartUpCurrentLevel = (%v, %v), want (nil/null, true)", v, ok)
	}
	attrs := map[uint32]bool{}
	for _, a := range srv.MatterAttributes() {
		attrs[a] = true
	}
	if !attrs[0x0001] {
		t.Error("MatterAttributes missing RemainingTime (0x0001)")
	}
	if !attrs[0x4000] {
		t.Error("MatterAttributes missing StartUpCurrentLevel (0x4000)")
	}
}

// TestOnOffReadObservedOn returns true after a non-zero LEVEL is seen.
func TestOnOffReadObservedOn(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.4)
	srv := onOffServer(t, l)
	v, ok := srv.MatterRead(0x0000)
	if !ok || v != true {
		t.Fatalf("MatterRead(OnOff) = (%v, %v), want (true, true)", v, ok)
	}
}

// TestOnOffInvokeOnRestoresLastLevel asserts On routes through TurnOn,
// which restores LastLevel rather than jumping to 100 %.
func TestOnOffInvokeOnRestoresLastLevel(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.7)
	l.OnLevel(0.0) // last_level=0.7, current=0.0
	srv := onOffServer(t, l)
	if _, err := srv.MatterInvoke(context.Background(), 0x01, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(On) error: %v", err)
	}
	if w.last != 0.7 {
		t.Fatalf("On wrote %v, want 0.7 (LastLevel)", w.last)
	}
}

// TestOnOffInvokeOff routes through TurnOff (LEVEL=0).
func TestOnOffInvokeOff(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5)
	srv := onOffServer(t, l)
	if _, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(Off) error: %v", err)
	}
	if w.last != 0.0 {
		t.Fatalf("Off wrote %v, want 0", w.last)
	}
}

// TestLevelReadCurrentLevelEncoding confirms HM 0.0–1.0 maps to Matter
// 0–254 (NOT 0–255 — 255 is the null sentinel).
func TestLevelReadCurrentLevelEncoding(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(1.0)
	srv := levelServer(t, l)
	v, ok := srv.MatterRead(0x0000)
	if !ok {
		t.Fatalf("MatterRead(CurrentLevel) ok=false, want true")
	}
	if v.(uint8) != 0xFE {
		t.Fatalf("HM 1.0 → Matter %d, want 254 (0xFE, not 255)", v.(uint8))
	}
}

// TestLevelReadCurrentLevelHalf checks the midpoint encoding.
func TestLevelReadCurrentLevelHalf(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5)
	srv := levelServer(t, l)
	v, ok := srv.MatterRead(0x0000)
	if !ok {
		t.Fatalf("MatterRead(CurrentLevel) ok=false")
	}
	got := v.(uint8)
	// 0.5 * 254 = 127, with rounding tolerance.
	if got < 126 || got > 128 {
		t.Fatalf("HM 0.5 → Matter %d, want ~127", got)
	}
}

// TestLevelWriteAtSaturation confirms a full-range write (0xFE) round-
// trips back to HM 1.0 brightness.
func TestLevelWriteAtSaturation(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	if err := srv.MatterWrite(context.Background(), 0x0000, uint8(0xFE), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(0xFE) error: %v", err)
	}
	if w.last != 1.0 {
		t.Fatalf("Matter 254 → HM %v, want 1.0", w.last)
	}
}

// TestLevelWriteHalf checks midpoint round-trip.
func TestLevelWriteHalf(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	if err := srv.MatterWrite(context.Background(), 0x0000, uint8(127), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite(127) error: %v", err)
	}
	// 127/254 ≈ 0.5
	if w.last < 0.49 || w.last > 0.51 {
		t.Fatalf("Matter 127 → HM %v, want ~0.5", w.last)
	}
}

// TestLevelWriteWrongTypeRejected catches non-uint8 writes.
func TestLevelWriteWrongTypeRejected(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	err := srv.MatterWrite(context.Background(), 0x0000, 127, hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterValueType) {
		t.Fatalf("err = %v, want errMatterValueType", err)
	}
}

// TestLevelInvokeMoveToLevel routes a MoveToLevel command through to
// SetLevel. The bare-uint8 shape carries no transition time, so the
// instant SetLevel path applies.
// The plain MoveToLevel variant (0x00, no OnOff coupling) is gated on the
// effective ExecuteIfOff option while the light is off (matter.js
// LevelControlServer.ts:596 #optionsAllowExecution); with no options set it
// is a silent no-op on an off/unobserved light (see
// TestLevelInvokeMoveToLevelWhileOffIsNoOp). The rig is therefore primed
// with l.OnLevel before invoking so this test exercises the executed path.
func TestLevelInvokeMoveToLevel(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5) // light is on
	srv := levelServer(t, l)
	if _, err := srv.MatterInvoke(context.Background(), 0x00, uint8(190), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevel error: %v", err)
	}
	// 190/254 ≈ 0.748
	if w.last < 0.74 || w.last > 0.76 {
		t.Fatalf("MoveToLevel(190) wrote %v, want ~0.748", w.last)
	}
}

// TestLevelInvokeMoveToLevelWithOnOffMap accepts the typed-request
// fallback shape (map carrying "level").
func TestLevelInvokeMoveToLevelWithOnOffMap(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	fields := map[string]any{"level": uint8(64)}
	if _, err := srv.MatterInvoke(context.Background(), 0x04, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevelWithOnOff error: %v", err)
	}
	// 64/254 ≈ 0.252
	if w.last < 0.24 || w.last > 0.26 {
		t.Fatalf("MoveToLevelWithOnOff(64) wrote %v, want ~0.252", w.last)
	}
}

// TestLevelInvokeMoveToLevelTransitionMapsToRampTime verifies that a
// positive TransitionTime (tenths of a second, Matter §1.6.7.1) on
// MoveToLevel (0x00) and MoveToLevelWithOnOff (0x04) is delegated to
// the device as one atomic put_paramset carrying LEVEL + RAMP_TIME
// (30 tenths → 3.0 s) + the ON_TIME=NotUsed sentinel, mirroring how
// matter.js LevelControlServer.ts:297-303 (moveToLevelLogic) derives a
// transition rate from a truthy transition time.
func TestLevelInvokeMoveToLevelTransitionMapsToRampTime(t *testing.T) {
	for _, cmdID := range []uint32{0x00, 0x04} {
		t.Run(fmt.Sprintf("cmd=0x%02X", cmdID), func(t *testing.T) {
			w := &putWriter{}
			l, _ := newLightRigPut(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true, Transition: true})
			l.OnLevel(0.5) // the plain variant (0x00) is gated on the light being on
			srv := levelServer(t, l)
			versionBefore := l.MatterDataVersion()
			tt := uint16(30)
			fields := wire.MoveToLevelRequest{Level: 190, TransitionTime: &tt}
			if _, err := srv.MatterInvoke(context.Background(), cmdID, fields, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("cmd 0x%02X with TransitionTime=30 error: %v", cmdID, err)
			}
			if len(w.puts) != 1 {
				t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
			}
			got := w.puts[0]
			// 190/254 ≈ 0.748
			if lvl := got[string(hmenum.ParameterLevel)].(float64); lvl < 0.74 || lvl > 0.76 {
				t.Errorf("LEVEL=%v, want ~0.748", lvl)
			}
			if ramp := got[string(hmenum.ParameterRampTime)].(float64); ramp != 3.0 {
				t.Errorf("RAMP_TIME=%v, want 3.0 (30 tenths of a second)", ramp)
			}
			// ON_TIME=NotUsed must accompany a stand-alone ramp so the CCU
			// does not overlay an implicit off-timer.
			if on := got[string(hmenum.ParameterOnTime)].(float64); on != NotUsed {
				t.Errorf("ON_TIME=%v, want NotUsed (%v)", on, NotUsed)
			}
			if l.MatterDataVersion() == versionBefore {
				t.Error("ramped MoveToLevel must bump the cluster data version")
			}
		})
	}
}

// TestLevelInvokeMoveToLevelWithOnOffTransitionToMinRampsOff verifies
// that MoveToLevelWithOnOff to MinLevel with a positive TransitionTime
// ramps the light off: the MinLevel→Off coupling (matter.js
// LevelControlServer.ts:500 couple) projects to LEVEL=0 on the single
// HM LEVEL knob, and the transition rides along as RAMP_TIME in the
// same atomic put_paramset.
func TestLevelInvokeMoveToLevelWithOnOffTransitionToMinRampsOff(t *testing.T) {
	w := &putWriter{}
	l, _ := newLightRigPut(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true, Transition: true})
	l.OnLevel(0.5)
	srv := levelServer(t, l)
	tt := uint16(30)
	fields := wire.MoveToLevelRequest{Level: 1, TransitionTime: &tt}
	if _, err := srv.MatterInvoke(context.Background(), 0x04, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevelWithOnOff(min, TransitionTime=30) error: %v", err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if lvl := got[string(hmenum.ParameterLevel)].(float64); lvl != 0 {
		t.Errorf("LEVEL=%v, want 0 (MinLevel + WithOnOff couples to off)", lvl)
	}
	if ramp := got[string(hmenum.ParameterRampTime)].(float64); ramp != 3.0 {
		t.Errorf("RAMP_TIME=%v, want 3.0", ramp)
	}
	if on := got[string(hmenum.ParameterOnTime)].(float64); on != NotUsed {
		t.Errorf("ON_TIME=%v, want NotUsed (%v)", on, NotUsed)
	}
}

// TestLevelInvokeMoveToLevelNilTransitionIsInstant verifies that a
// null/absent TransitionTime keeps the instant SetLevel path (no
// put_paramset, no RAMP_TIME) even on a ramp-capable device — matter.js
// LevelControlServer.ts:297 substitutes onOffTransitionTime ?? null and
// only builds a rate for a truthy value.
func TestLevelInvokeMoveToLevelNilTransitionIsInstant(t *testing.T) {
	w := &putWriter{}
	l, _ := newLightRigPut(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true, Transition: true})
	l.OnLevel(0.5)
	srv := levelServer(t, l)
	fields := wire.MoveToLevelRequest{Level: 190}
	if _, err := srv.MatterInvoke(context.Background(), 0x00, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevel(nil TransitionTime) error: %v", err)
	}
	if len(w.puts) != 0 {
		t.Fatalf("nil TransitionTime must not put_paramset, got %d puts", len(w.puts))
	}
	if w.last < 0.74 || w.last > 0.76 {
		t.Fatalf("instant SetLevel wrote %v, want ~0.748", w.last)
	}
}

// TestLevelInvokeMoveToLevelZeroTransitionIsInstant verifies that
// TransitionTime=0 means "move as fast as able", i.e. the instant
// SetLevel path — matter.js LevelControlServer.ts:459 documents the
// transition rate contract as "0 or nullish means transition
// instantly", and moveToLevelLogic (LevelControlServer.ts:300) skips
// rate derivation for a zero transition time.
func TestLevelInvokeMoveToLevelZeroTransitionIsInstant(t *testing.T) {
	w := &putWriter{}
	l, _ := newLightRigPut(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true, Transition: true})
	l.OnLevel(0.5)
	srv := levelServer(t, l)
	tt := uint16(0)
	fields := wire.MoveToLevelRequest{Level: 190, TransitionTime: &tt}
	if _, err := srv.MatterInvoke(context.Background(), 0x00, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevel(TransitionTime=0) error: %v", err)
	}
	if len(w.puts) != 0 {
		t.Fatalf("TransitionTime=0 must not put_paramset, got %d puts", len(w.puts))
	}
	if w.last < 0.74 || w.last > 0.76 {
		t.Fatalf("instant SetLevel wrote %v, want ~0.748", w.last)
	}
}

// TestLevelInvokeMoveToLevelTransitionWithoutRampSupportIsInstant
// verifies the capability gate: a device that does not accept
// RAMP_TIME (Capabilities.Transition unset) falls back to the instant
// SetLevel path even when the command carries a positive
// TransitionTime — sending RAMP_TIME to such a channel would fault the
// put_paramset on the CCU.
func TestLevelInvokeMoveToLevelTransitionWithoutRampSupportIsInstant(t *testing.T) {
	w := &putWriter{}
	l, _ := newLightRigPut(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5)
	srv := levelServer(t, l)
	tt := uint16(30)
	fields := wire.MoveToLevelRequest{Level: 190, TransitionTime: &tt}
	if _, err := srv.MatterInvoke(context.Background(), 0x00, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MoveToLevel(TransitionTime=30, no ramp support) error: %v", err)
	}
	if len(w.puts) != 0 {
		t.Fatalf("device without RAMP_TIME must not put_paramset, got %d puts", len(w.puts))
	}
	if w.last < 0.74 || w.last > 0.76 {
		t.Fatalf("instant SetLevel wrote %v, want ~0.748", w.last)
	}
}

// TestLevelInvokeMoveReturnsSuccess verifies that Move (0x01) and
// MoveWithOnOff (0x05) return Success without error — HM has no
// continuous-rate dimming, so these are accepted no-ops.
func TestLevelInvokeMoveReturnsSuccess(t *testing.T) {
	for _, cmdID := range []uint32{0x01, 0x05} {
		t.Run(fmt.Sprintf("cmd=0x%02X", cmdID), func(t *testing.T) {
			l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
			srv := levelServer(t, l)
			_, err := srv.MatterInvoke(context.Background(), cmdID, nil, hmenum.CommandPriorityHigh)
			if err != nil {
				t.Fatalf("cmd 0x%02X returned error: %v", cmdID, err)
			}
		})
	}
}

// TestLevelInvokeStepUpAndDown verifies that Step (0x02) and
// StepWithOnOff (0x06) adjust the brightness by the given step size.
func TestLevelInvokeStepUpAndDown(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5) // current ≈ 127

	srv := levelServer(t, l)
	// Step up by 10 — expect ~137/254 ≈ 0.539.
	fields := map[string]any{"step_mode": uint8(0), "step_size": uint8(10)}
	if _, err := srv.MatterInvoke(context.Background(), 0x02, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Step up error: %v", err)
	}
	if w.last < 0.52 || w.last > 0.56 {
		t.Fatalf("Step up wrote %v, want ~0.539", w.last)
	}
}

// TestLevelInvokeStepDownFloor verifies that a plain Step-Down (0x02) that
// would go below MinLevel clamps to MinLevel (1), not 0 — a plain Step can
// never turn the device off, mirroring matter.js Transitions.ts:139's
// min/max property clamp against [MinLevel, MaxLevel] = [1, 254].
func TestLevelInvokeStepDownFloor(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.1)
	srv := levelServer(t, l)
	// Step down by 200 from a low baseline — must clamp to MinLevel (1).
	fields := map[string]any{"step_mode": uint8(1), "step_size": uint8(200)}
	if _, err := srv.MatterInvoke(context.Background(), 0x02, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Step down error: %v", err)
	}
	// 1/254 ≈ 0.0039
	if w.last < 0.003 || w.last > 0.005 {
		t.Fatalf("Step down floor wrote %v, want ~0.0039 (MinLevel, not 0)", w.last)
	}
}

// TestLevelInvokeStopReturnsSuccess verifies that Stop (0x03) and
// StopWithOnOff (0x07) return Success without modifying brightness —
// there is no in-flight ramp on HM to stop.
func TestLevelInvokeStopReturnsSuccess(t *testing.T) {
	for _, cmdID := range []uint32{0x03, 0x07} {
		t.Run(fmt.Sprintf("cmd=0x%02X", cmdID), func(t *testing.T) {
			w := &stubWriter{}
			l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
			l.OnLevel(0.6)
			srv := levelServer(t, l)
			_, err := srv.MatterInvoke(context.Background(), cmdID, nil, hmenum.CommandPriorityHigh)
			if err != nil {
				t.Fatalf("cmd 0x%02X returned error: %v", cmdID, err)
			}
			if w.last != 0 {
				t.Fatalf("Stop cmd 0x%02X triggered a write (wrote %v), want no-op", cmdID, w.last)
			}
		})
	}
}

// TestLevelInvokeUnknownCommand rejects commands outside the handled set.
// 0x09 is beyond StopWithOnOff (0x07) and MoveToClosestFrequency (0x08);
// both are unimplemented.
func TestLevelInvokeUnknownCommand(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	_, err := srv.MatterInvoke(context.Background(), 0x09, nil, hmenum.CommandPriorityHigh)
	if !errors.Is(err, errMatterUnknownCommand) {
		t.Fatalf("err = %v, want errMatterUnknownCommand", err)
	}
}

// TestLevelControlNotPresentOnNonDimmable double-checks that the
// LevelControl cluster server is absent on non-dimmable lights — the
// endpoint assembler relies on this to advertise OnOffLight (0x0100)
// without LevelControl.
func TestLevelControlNotPresentOnNonDimmable(t *testing.T) {
	l, _ := newLightRig(t, "HM-LC-Sw:1", &stubWriter{}, custom.LightCapabilities{})
	for _, s := range l.MatterClusterServers() {
		if _, ok := s.(lightLevelServer); ok {
			t.Fatalf("non-dimmable light unexpectedly contributes LevelControl")
		}
	}
}

// TestBrightnessByteEncodingDoesNotCollideWithMatterNull is the
// regression guard for the 254-vs-255 gotcha. HM Brightness.Byte()
// returns 255 at level=1.0 (the HM byte serialisation), but Matter's
// LevelControl uses 255 as a null sentinel; the projection must
// clamp to 254.
func TestBrightnessByteEncodingDoesNotCollideWithMatterNull(t *testing.T) {
	hmByteAtFull := custom.NewBrightness(1.0).Byte()
	if hmByteAtFull != 255 {
		t.Fatalf("test assumption failed: HM Byte(1.0)=%d, want 255", hmByteAtFull)
	}
	matterAtFull := brightnessToMatter(custom.NewBrightness(1.0))
	if matterAtFull == 255 {
		t.Fatalf("Matter encoding hit null sentinel — projection must clamp to 254")
	}
	if matterAtFull != 0xFE {
		t.Fatalf("Matter encoding at full = %d, want 254 (0xFE)", matterAtFull)
	}
}

// TestLevelControlMinLevelDefault verifies that MatterRead(0x0002)
// returns the Matter-defined default MinLevel of 0x01.
func TestLevelControlMinLevelDefault(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	v, ok := srv.MatterRead(matterAttrLevelMin)
	if !ok {
		t.Fatal("MatterRead(MinLevel=0x0002) ok=false, want true")
	}
	got, isByte := v.(uint8)
	if !isByte {
		t.Fatalf("MinLevel type = %T, want uint8", v)
	}
	if got != 0x01 {
		t.Fatalf("MinLevel = 0x%02X, want 0x01", got)
	}
}

// TestLevelControlMaxLevelDefault verifies that MatterRead(0x0003)
// returns the Matter-defined default MaxLevel of 0xFE.
func TestLevelControlMaxLevelDefault(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	v, ok := srv.MatterRead(matterAttrLevelMax)
	if !ok {
		t.Fatal("MatterRead(MaxLevel=0x0003) ok=false, want true")
	}
	got, isByte := v.(uint8)
	if !isByte {
		t.Fatalf("MaxLevel type = %T, want uint8", v)
	}
	if got != 0xFE {
		t.Fatalf("MaxLevel = 0x%02X, want 0xFE", got)
	}
}

// TestLevelControlMatterAttributesIncludesMinMax verifies that
// MatterAttributes enumerates MinLevel (0x0002) and MaxLevel (0x0003).
func TestLevelControlMatterAttributesIncludesMinMax(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	attrs := srv.MatterAttributes()
	got := make(map[uint32]bool, len(attrs))
	for _, a := range attrs {
		got[a] = true
	}
	if !got[matterAttrLevelMin] {
		t.Errorf("MatterAttributes does not contain MinLevel (0x%04X)", matterAttrLevelMin)
	}
	if !got[matterAttrLevelMax] {
		t.Errorf("MatterAttributes does not contain MaxLevel (0x%04X)", matterAttrLevelMax)
	}
}

// TestLevelControlMaxLevelDoesNotEqualNullSentinel is a regression guard:
// MaxLevel must not be the TLV null sentinel (0xFF) for nullable uint8.
func TestLevelControlMaxLevelDoesNotEqualNullSentinel(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	v, ok := srv.MatterRead(matterAttrLevelMax)
	if !ok {
		t.Fatal("MatterRead(MaxLevel) ok=false")
	}
	if v.(uint8) == 0xFF {
		t.Fatal("MaxLevel = 0xFF (TLV null sentinel) — must be 0xFE")
	}
}
