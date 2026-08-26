// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestSendReceive_LevelControl is the send/receive matrix cell for
// LevelControl (0x0008). HmIP-BDT (light.Light dimmable) is the
// representative device in godevccu's fleet.
//
//   - SEND: a controller `move-to-level` invoke must reach the CCU as a
//     LEVEL write, decoded via matterLevelToHM (see
//     internal/model/custom/light/matter.go).
//   - RECEIVE: a simulated device-originated LEVEL push must reach the
//     controller through a PROACTIVE Subscribe report on CurrentLevel
//     (via [harness.AwaitProactiveReport] — subscribe first, then fire).
//
// Writes go against the in-process simulator; the live CCU is never
// touched.
func TestSendReceive_LevelControl(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0008, 1)
	if len(eps) == 0 {
		t.Skip("no LevelControl endpoint — godevccu fleet lacks a dimmable light device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0008)
	if !ok {
		t.Fatalf("could not resolve CCU address for LevelControl endpoint %d", ep)
	}

	// SEND — a controller move-to-level-with-on-off(128, 0, 0, 0) must reach
	// the CCU as LEVEL = matterLevelToHM(128) = 128/254 ~= 0.503937.
	//
	// The WithOnOff variant is used deliberately: a HM dimmer sits at
	// LEVEL=0 (off) between runs, and per Matter §1.6.4.1.2 a PLAIN
	// MoveToLevel on an off device with no ExecuteIfOff option is a silent
	// no-op (matter.js LevelControlServer.ts:245 returns without acting) —
	// LEVEL would stay 0. MoveToLevelWithOnOff couples the level to OnOff
	// and always executes, which is how a controller dims a light up from
	// off; it is the correct SEND operation to exercise the LEVEL write.
	t.Run("send/move-to-level", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "levelcontrol", "move-to-level-with-on-off", ep, "128", "0", "0", "0"); err != nil {
			t.Fatalf("invoke move-to-level-with-on-off: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "LEVEL")
		if !ok {
			t.Fatalf("LEVEL absent on CCU after move-to-level-with-on-off")
		}
		want := 128.0 / 254.0
		if !valueNear(got, want, 0.01) {
			t.Fatalf("CCU LEVEL = %v, want ~%.6f", got, want)
		}
	})

	// RECEIVE — an external LEVEL change (device-side dim) must reach
	// the controller as a proactive CurrentLevel report.
	// brightnessToMatter(0.5) = uint8(0.5*254 + 0.5) = 127; the SEND
	// cell above already parked CCU LEVEL at ~0.504 (Matter level 128),
	// so 127 is distinct from the pre-state and cannot be pre-satisfied
	// by the subscribe's own priming read.
	//
	// Light is not a [interfaces.MatterClusterServer] (unlike e.g.
	// switch.Switch for OnOff), so a confirmed LEVEL change dirty-marks
	// the FULL endpoint and OnOff.OnOff may co-report alongside
	// CurrentLevel in the same ReportData. want() only checks for the
	// CurrentLevel line, so that co-report is tolerated rather than
	// asserted against.
	t.Run("receive/current-level", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"levelcontrol", "current-level", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "LEVEL", 0.5) },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "CurrentLevel")
				return ok && v == 127
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive CurrentLevel=127: %v\n%s", err, out)
		}
	})
}
