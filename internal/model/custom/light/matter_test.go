// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/levelcontrol"
	"github.com/SukramJ/go-fabric/cluster/onoff"
	onoffspec "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	"github.com/SukramJ/go-fabric/cluster/wire"
	fabricdevice "github.com/SukramJ/go-fabric/device"
	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
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

// TestMatterClusterServersDimmable expects exactly OnOff (0x0006) and
// LevelControl (0x0008) from a dimmable light. Groups (0x0004) and
// ScenesManagement (0x0062), mandatory on DimmableLight per matter.js
// packages/node/src/devices/on-off-light.ts, are stack state the endpoint
// assembler mounts — the source supplies neither.
func TestMatterClusterServersDimmable(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	servers := matterServers(t, l)
	got := map[uint32]bool{}
	for _, s := range servers {
		got[s.MatterClusterID()] = true
	}
	if len(servers) != 2 || !got[0x0006] || !got[0x0008] {
		t.Fatalf("dimmable cluster set = %v (%d servers), want exactly {0x0006, 0x0008}", got, len(servers))
	}
}

// TestMatterClusterServersNonDimmable expects exactly OnOff (0x0006)
// from a non-dimmable light; Groups and ScenesManagement are mounted by
// the endpoint assembler, not supplied by the source.
func TestMatterClusterServersNonDimmable(t *testing.T) {
	l, _ := newLightRig(t, "HM-LC-Sw:1", &stubWriter{}, custom.LightCapabilities{})
	servers := matterServers(t, l)
	if len(servers) != 1 || servers[0].MatterClusterID() != 0x0006 {
		t.Fatalf("non-dimmable: %d cluster servers, want exactly [0x0006]", len(servers))
	}
}

// matterDeviceOf returns the device src projects, built once per source.
func matterDeviceOf(t *testing.T, src interfaces.MatterDeviceSource) *fabricdevice.Device {
	t.Helper()
	d, err := src.MatterDevice(fabricdevice.Options{StableKey: fabricdevice.StringKey("light-test"), Name: "light"})
	if err != nil || d == nil {
		t.Fatalf("MatterDevice = (%v, %v), want a device", d, err)
	}
	return d
}

// matterServers returns the cluster servers of the device src projects.
func matterServers(t *testing.T, src interfaces.MatterDeviceSource) []interfaces.MatterClusterServer {
	t.Helper()
	return matterDeviceOf(t, src).Servers()
}

// onOffServer returns the light's OnOff cluster server — go-fabric's
// [onoff.Server], built once per light.
func onOffServer(t *testing.T, l *Light) *onoff.Server {
	t.Helper()
	for _, s := range matterServers(t, l) {
		if srv, ok := s.(*onoff.Server); ok {
			return srv
		}
	}
	t.Fatalf("light has no OnOff cluster server")
	return nil
}

// recWriter records every LEVEL write the Light issues, with its
// priority, under a lock: the OnOff server's countdown writes from a
// timer goroutine. A non-nil err refuses every write.
type recWriter struct {
	mu     sync.Mutex
	values []float64
	prios  []hmenum.CommandPriority
	err    error
}

func (w *recWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, value any, priority hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	f, _ := value.(float64)
	w.values = append(w.values, f)
	w.prios = append(w.prios, priority)
	return nil
}

func (w *recWriter) writes() ([]float64, []hmenum.CommandPriority) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]float64(nil), w.values...), append([]hmenum.CommandPriority(nil), w.prios...)
}

// levelServer returns the LevelControl cluster server for a dimmable
// light — go-fabric's [levelcontrol.Server], built once per light.
func levelServer(t *testing.T, l *Light) *levelcontrol.Server {
	t.Helper()
	for _, s := range matterServers(t, l) {
		if lvl, ok := s.(*levelcontrol.Server); ok {
			return lvl
		}
	}
	t.Fatalf("light has no LevelControl cluster server")
	return nil
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
// advertises exactly the LT (Lighting) feature — mandatory on
// OnOffLight / DimmableLight. matter.js on-off.element.ts:24.
func TestOnOffFeatureMapAdvertisesLighting(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	v, ok := srv.MatterRead(0xFFFC)
	if !ok {
		t.Fatal("MatterRead(FeatureMap) ok=false")
	}
	if v.(uint32) != onoff.FeatureLighting {
		t.Fatalf("OnOff FeatureMap = 0x%08X, want 0x%08X (LT only)", v.(uint32), onoff.FeatureLighting)
	}
}

// TestOnOffLightingGatedAttributes verifies the four LT-mandatory OnOff
// attributes are served (readable) on a light.
func TestOnOffLightingGatedAttributes(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	for _, id := range []uint32{onoff.AttrGlobalSceneControl, onoff.AttrOnTime, onoff.AttrOffWaitTime, onoff.AttrStartUpOnOff} {
		if _, ok := srv.MatterRead(id); !ok {
			t.Errorf("MatterRead(0x%04X) ok=false, want the LT attribute served", id)
		}
	}
}

// TestOnOffAttributesAndCommandsEnumerateLightingGated pins the exact
// attribute and accepted-command lists a light's OnOff server
// advertises for the LT FeatureMap.
func TestOnOffAttributesAndCommandsEnumerateLightingGated(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	if got, want := srv.MatterAttributes(), []uint32{0x0000, 0x4000, 0x4001, 0x4002, 0x4003}; !slices.Equal(got, want) {
		t.Errorf("MatterAttributes = %#x, want %#x", got, want)
	}
	if got, want := srv.MatterAcceptedCommands(), []uint32{0x00, 0x01, 0x02, 0x40, 0x41, 0x42}; !slices.Equal(got, want) {
		t.Errorf("MatterAcceptedCommands = %#x, want %#x", got, want)
	}
}

// TestOnOffLightingGatedCommandsAccepted verifies the three LT-mandatory
// commands reach the CCU as a plain turn-off / turn-on.
func TestOnOffLightingGatedCommandsAccepted(t *testing.T) {
	cases := []struct {
		cmd     uint32
		fields  any
		wantVal float64
	}{
		{onoff.CmdOffWithEffect, nil, 0.0},                                // → Off
		{onoff.CmdOnWithRecallGlobalScene, nil, 0.5},                      // → On (restores last level)
		{onoff.CmdOnWithTimedOff, onoffspec.OnWithTimedOffRequest{}, 0.5}, // → On
	}
	for _, tc := range cases {
		w := &stubWriter{last: -1}
		l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
		l.OnLevel(0.5)
		if tc.cmd != onoff.CmdOffWithEffect {
			l.OnLevel(0.0) // current goes to 0; LastLevel retains 0.5
		}
		srv := onOffServer(t, l)
		if _, err := srv.MatterInvoke(context.Background(), tc.cmd, tc.fields); err != nil {
			t.Fatalf("MatterInvoke(0x%02X) error: %v", tc.cmd, err)
		}
		if w.last != tc.wantVal {
			t.Fatalf("cmd 0x%02X wrote %v, want %v", tc.cmd, w.last, tc.wantVal)
		}
	}
}

// TestOnOffServerIsBuiltOncePerLight pins that every MatterClusterServers
// call hands out the same OnOff server: the bridge keeps the server it
// already serves across reassemblies, and a rebuilt one would lose its
// countdowns, its scene state and the confirmed-update hook behind it.
func TestOnOffServerIsBuiltOncePerLight(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	first := onOffServer(t, l)
	if second := onOffServer(t, l); second != first {
		t.Fatal("MatterClusterServers built a second OnOff server for the same light")
	}
}

// TestOnOffCommandReachesTheCCUAtHighPriority pins the Sink: a
// controller's On / Off becomes Light.TurnOn / Light.TurnOff, written at
// CommandPriorityHigh, and the attribute follows the accepted command.
func TestOnOffCommandReachesTheCCUAtHighPriority(t *testing.T) {
	w := &recWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)

	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdOn, nil); err != nil {
		t.Fatalf("On: %v", err)
	}
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != true {
		t.Fatalf("OnOff after On = %v, want true", v)
	}
	if _, err := srv.MatterInvoke(context.Background(), onoff.CmdOff, nil); err != nil {
		t.Fatalf("Off: %v", err)
	}
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != false {
		t.Fatalf("OnOff after Off = %v, want false", v)
	}

	values, prios := w.writes()
	if len(values) != 2 || values[0] <= 0 || values[1] != 0 {
		t.Fatalf("CCU writes = %v, want [turn-on level, 0]", values)
	}
	for i, p := range prios {
		if p != hmenum.CommandPriorityHigh {
			t.Errorf("write %d priority = %v, want CommandPriorityHigh", i, p)
		}
	}
}

// TestOnOffSinkErrorFailsTheCommandAndKeepsTheAttribute pins that a CCU
// write the CCU refuses answers the command with an error and leaves
// OnOff — and the OnOff server's data version — where they were.
func TestOnOffSinkErrorFailsTheCommandAndKeepsTheAttribute(t *testing.T) {
	w := &recWriter{err: errors.New("ccu unreachable")}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	before := srv.MatterDataVersion()

	for _, cmd := range []struct {
		id     uint32
		fields any
	}{
		{onoff.CmdOn, nil},
		{onoff.CmdToggle, nil},
		{onoff.CmdOnWithTimedOff, onoffspec.OnWithTimedOffRequest{OnTime: 5}},
	} {
		if _, err := srv.MatterInvoke(context.Background(), cmd.id, cmd.fields); err == nil {
			t.Fatalf("command 0x%02X: want the CCU refusal answered as an error", cmd.id)
		}
		if v, _ := srv.MatterRead(onoff.AttrOnOff); v != false {
			t.Fatalf("command 0x%02X: OnOff after a refused write = %v, want false", cmd.id, v)
		}
	}
	if v, _ := srv.MatterRead(onoff.AttrOnTime); v != uint16(0) {
		t.Fatalf("OnTime after a refused OnWithTimedOff = %v, want 0", v)
	}
	if after := srv.MatterDataVersion(); after != before {
		t.Fatalf("refused commands bumped the data version: %d -> %d", before, after)
	}
}

// TestOnOffConfirmedLevelUpdateIsStoredWithoutACCUWrite pins the
// feedback path: a CCU-confirmed LEVEL update — a wall switch, a CCU
// program — reaches the OnOff attribute through the confirmed-update
// hook, and is not echoed back to the CCU as a second write.
func TestOnOffConfirmedLevelUpdateIsStoredWithoutACCUWrite(t *testing.T) {
	w := &recWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	before := srv.MatterDataVersion()

	l.OnLevel(0.6)
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != true {
		t.Fatalf("OnOff after confirmed LEVEL 0.6 = %v, want true", v)
	}
	if srv.MatterDataVersion() == before {
		t.Fatal("a confirmed change must bump the data version a subscription reports with")
	}
	l.OnLevel(0)
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != false {
		t.Fatalf("OnOff after confirmed LEVEL 0 = %v, want false", v)
	}
	if values, _ := w.writes(); len(values) != 0 {
		t.Fatalf("confirmed updates reached the CCU as writes %v, want none", values)
	}
}

// TestOnOffAttributeIsNotWritable pins that OnOff itself is read-only
// (matter.js on-off.element.ts, access "R V"): a write is refused and
// nothing reaches the CCU — a controller changes it through a command.
func TestOnOffAttributeIsNotWritable(t *testing.T) {
	w := &recWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	srv := onOffServer(t, l)
	if err := srv.MatterWrite(context.Background(), onoff.AttrOnOff, true); err == nil {
		t.Fatal("MatterWrite(OnOff) succeeded, want a refusal")
	}
	if v, _ := srv.MatterRead(onoff.AttrOnOff); v != false {
		t.Fatalf("OnOff after a refused write = %v, want false", v)
	}
	if values, _ := w.writes(); len(values) != 0 {
		t.Fatalf("a refused OnOff write reached the CCU: %v", values)
	}
}

// TestLevelWithOnOffCommandsDriveTheOnOffAttribute pins the
// LevelControl → OnOff coupling of the "with On/Off" commands: a
// MoveToLevelWithOnOff to MinLevel turns OnOff off, one above it turns
// it on, each with exactly the one LEVEL write — the OnOff attribute
// follows without a second CCU write and before any CCU confirmation.
func TestLevelWithOnOffCommandsDriveTheOnOffAttribute(t *testing.T) {
	w := &recWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5)
	oo := onOffServer(t, l)
	lv := levelServer(t, l)

	if _, err := lv.MatterInvoke(context.Background(), levelcontrol.CmdMoveToLevelWithOnOff, wire.MoveToLevelRequest{Level: matterLevelMinDefault}); err != nil {
		t.Fatalf("MoveToLevelWithOnOff(MinLevel): %v", err)
	}
	if v, _ := oo.MatterRead(onoff.AttrOnOff); v != false {
		t.Fatalf("OnOff after MoveToLevelWithOnOff(MinLevel) = %v, want false", v)
	}
	if values, _ := w.writes(); len(values) != 1 || values[0] != 0 {
		t.Fatalf("CCU writes after MoveToLevelWithOnOff(MinLevel) = %v, want exactly [0]", values)
	}

	if _, err := lv.MatterInvoke(context.Background(), levelcontrol.CmdMoveToLevelWithOnOff, wire.MoveToLevelRequest{Level: 190}); err != nil {
		t.Fatalf("MoveToLevelWithOnOff(190): %v", err)
	}
	if v, _ := oo.MatterRead(onoff.AttrOnOff); v != true {
		t.Fatalf("OnOff after MoveToLevelWithOnOff(190) = %v, want true", v)
	}
	if values, _ := w.writes(); len(values) != 2 || values[1] <= 0 {
		t.Fatalf("CCU writes after MoveToLevelWithOnOff(190) = %v, want exactly one more, a turn-on level", values)
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
	if got := v.(uint32); got != 0x03 {
		t.Errorf("LevelControl FeatureMap = 0x%08X, want exactly OO|LT = 0x03", got)
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
	if _, err := srv.MatterInvoke(context.Background(), 0x01, nil); err != nil {
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
	if _, err := srv.MatterInvoke(context.Background(), 0x00, nil); err != nil {
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

// TestLevelInvokeMoveToLevel routes a MoveToLevel command through to
// SetLevel. The request carries no transition time, so the instant
// SetLevel path applies.
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
	if _, err := srv.MatterInvoke(context.Background(), 0x00, wire.MoveToLevelRequest{Level: 190}); err != nil {
		t.Fatalf("MoveToLevel error: %v", err)
	}
	// 190/254 ≈ 0.748
	if w.last < 0.74 || w.last > 0.76 {
		t.Fatalf("MoveToLevel(190) wrote %v, want ~0.748", w.last)
	}
}

// TestLevelInvokeMoveToLevelTransitionMapsToRampTime verifies that a
// positive TransitionTime (tenths of a second, Matter §1.6.7.1) on
// MoveToLevel (0x00) and MoveToLevelWithOnOff (0x04) is delegated to
// the device as one atomic put_paramset carrying LEVEL + RAMP_TIME
// (30 tenths → 3.0 s) + the ON_TIME=TimerNotUsed sentinel, mirroring how
// matter.js LevelControlServer.ts:297-303 (moveToLevelLogic) derives a
// transition rate from a truthy transition time.
func TestLevelInvokeMoveToLevelTransitionMapsToRampTime(t *testing.T) {
	for _, cmdID := range []uint32{0x00, 0x04} {
		t.Run(fmt.Sprintf("cmd=0x%02X", cmdID), func(t *testing.T) {
			w := &putWriter{}
			l, _ := newLightRigPut(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true, Transition: true})
			l.OnLevel(0.5) // the plain variant (0x00) is gated on the light being on
			srv := levelServer(t, l)
			versionBefore := srv.MatterDataVersion()
			tt := uint16(30)
			fields := wire.MoveToLevelRequest{Level: 190, TransitionTime: &tt}
			if _, err := srv.MatterInvoke(context.Background(), cmdID, fields); err != nil {
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
			// ON_TIME=TimerNotUsed must accompany a stand-alone ramp so the CCU
			// does not overlay an implicit off-timer.
			if on := got[string(hmenum.ParameterOnTime)].(float64); on != custom.TimerNotUsed {
				t.Errorf("ON_TIME=%v, want TimerNotUsed (%v)", on, custom.TimerNotUsed)
			}
			if srv.MatterDataVersion() == versionBefore {
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
	if _, err := srv.MatterInvoke(context.Background(), 0x04, fields); err != nil {
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
	if on := got[string(hmenum.ParameterOnTime)].(float64); on != custom.TimerNotUsed {
		t.Errorf("ON_TIME=%v, want TimerNotUsed (%v)", on, custom.TimerNotUsed)
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
	if _, err := srv.MatterInvoke(context.Background(), 0x00, fields); err != nil {
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
	if _, err := srv.MatterInvoke(context.Background(), 0x00, fields); err != nil {
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
	if _, err := srv.MatterInvoke(context.Background(), 0x00, fields); err != nil {
		t.Fatalf("MoveToLevel(TransitionTime=30, no ramp support) error: %v", err)
	}
	if len(w.puts) != 0 {
		t.Fatalf("device without RAMP_TIME must not put_paramset, got %d puts", len(w.puts))
	}
	if w.last < 0.74 || w.last > 0.76 {
		t.Fatalf("instant SetLevel wrote %v, want ~0.748", w.last)
	}
}

// TestLevelInvokeStepUpAndDown verifies that Step (0x02) adjusts the
// brightness by the given step size.
func TestLevelInvokeStepUpAndDown(t *testing.T) {
	w := &stubWriter{}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.OnLevel(0.5) // current ≈ 127

	srv := levelServer(t, l)
	// Step up by 10 — expect ~137/254 ≈ 0.539.
	fields := wire.StepRequest{StepMode: wire.LevelStepModeUp, StepSize: 10}
	if _, err := srv.MatterInvoke(context.Background(), 0x02, fields); err != nil {
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
	fields := wire.StepRequest{StepMode: wire.LevelStepModeDown, StepSize: 200}
	if _, err := srv.MatterInvoke(context.Background(), 0x02, fields); err != nil {
		t.Fatalf("Step down error: %v", err)
	}
	// 1/254 ≈ 0.0039
	if w.last < 0.003 || w.last > 0.005 {
		t.Fatalf("Step down floor wrote %v, want ~0.0039 (MinLevel, not 0)", w.last)
	}
}

// TestLevelInvokeUnknownCommand rejects commands outside the handled set.
// 0x09 is beyond StopWithOnOff (0x07) and MoveToClosestFrequency (0x08);
// both are unimplemented.
func TestLevelInvokeUnknownCommand(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	_, err := srv.MatterInvoke(context.Background(), 0x09, nil)
	var sc im.StatusCodeError
	if !errors.As(err, &sc) || sc.MatterStatusCode() != im.StatusUnsupportedCommand {
		t.Fatalf("err = %v, want UnsupportedCommand", err)
	}
}

// TestLevelControlNotPresentOnNonDimmable double-checks that the
// LevelControl cluster server is absent on non-dimmable lights — the
// endpoint assembler relies on this to advertise OnOffLight (0x0100)
// without LevelControl.
func TestLevelControlNotPresentOnNonDimmable(t *testing.T) {
	l, _ := newLightRig(t, "HM-LC-Sw:1", &stubWriter{}, custom.LightCapabilities{})
	for _, s := range matterServers(t, l) {
		if _, ok := s.(*levelcontrol.Server); ok {
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
	v, ok := srv.MatterRead(levelcontrol.AttrMinLevel)
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
	v, ok := srv.MatterRead(levelcontrol.AttrMaxLevel)
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
	if !got[levelcontrol.AttrMinLevel] {
		t.Errorf("MatterAttributes does not contain MinLevel (0x%04X)", levelcontrol.AttrMinLevel)
	}
	if !got[levelcontrol.AttrMaxLevel] {
		t.Errorf("MatterAttributes does not contain MaxLevel (0x%04X)", levelcontrol.AttrMaxLevel)
	}
}

// TestLevelControlMaxLevelDoesNotEqualNullSentinel is a regression guard:
// MaxLevel must not be the TLV null sentinel (0xFF) for nullable uint8.
func TestLevelControlMaxLevelDoesNotEqualNullSentinel(t *testing.T) {
	l, _ := newLightRig(t, "HmIP-BDT:4", &stubWriter{}, custom.LightCapabilities{Dimmable: true})
	srv := levelServer(t, l)
	v, ok := srv.MatterRead(levelcontrol.AttrMaxLevel)
	if !ok {
		t.Fatal("MatterRead(MaxLevel) ok=false")
	}
	if v.(uint8) == 0xFF {
		t.Fatal("MaxLevel = 0xFF (TLV null sentinel) — must be 0xFE")
	}
}
