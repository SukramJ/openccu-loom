// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestSendReceive_OnOff is the SEND/RECEIVE matrix cell for OnOff
// (0x0006) against the representative HmIP-PS switch host (STATE
// bool) — HmIP-BSM ch4/5/6 and HmIP-BDT's lightOnOffServer are
// equally valid discovery hits when the fleet lacks a bare PSM. It
// upgrades TestInvoke_OnOff_OnToggleOffCycle (invoke_test.go), which
// only re-reads OnOff through the bridge, with CCU-side ground-truth
// assertions via [harness.MockCCU.GetDPValue] plus the RECEIVE
// direction via a subscribe-first-then-fire proactive report.
func TestSendReceive_OnOff(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0006, 1)
	if len(eps) == 0 {
		t.Skip("no OnOff endpoint — godevccu fleet lacks a Switch/PSM/BSM/BDT device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep)
	if !ok {
		t.Fatalf("could not resolve CCU address for OnOff endpoint %d", ep)
	}

	// Baseline OFF so the cycle below is reproducible no matter what
	// the simulator's last state was.
	if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "off", ep); err != nil {
		t.Fatalf("baseline off: %v", err)
	}
	if got, ok := b.CCU.GetDPValue(address, "STATE"); !ok || !onoffIsFalse(got) {
		t.Fatalf("CCU STATE after baseline off = %v (ok=%v), want false", got, ok)
	}

	// SEND — an On/Toggle/Off invoke cycle must land on the CCU as
	// STATE writes, not just flip the bridge's own cache. This is the
	// upgrade over invoke_test.go's cycle test, which only asserts via
	// a Matter re-read.
	t.Run("send/on-toggle-off-cycle", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "on", ep); err != nil {
			t.Fatalf("invoke on: %v", err)
		}
		if got, ok := b.CCU.GetDPValue(address, "STATE"); !ok || !onoffIsTrue(got) {
			t.Fatalf("CCU STATE after on = %v (ok=%v), want true", got, ok)
		}

		if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "toggle", ep); err != nil {
			t.Fatalf("invoke toggle: %v", err)
		}
		if got, ok := b.CCU.GetDPValue(address, "STATE"); !ok || !onoffIsFalse(got) {
			t.Fatalf("CCU STATE after toggle (from on) = %v (ok=%v), want false", got, ok)
		}

		if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "on", ep); err != nil {
			t.Fatalf("invoke on (re-arm): %v", err)
		}
		if got, ok := b.CCU.GetDPValue(address, "STATE"); !ok || !onoffIsTrue(got) {
			t.Fatalf("CCU STATE after re-arm on = %v (ok=%v), want true", got, ok)
		}

		if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "off", ep); err != nil {
			t.Fatalf("invoke off: %v", err)
		}
		if got, ok := b.CCU.GetDPValue(address, "STATE"); !ok || !onoffIsFalse(got) {
			t.Fatalf("CCU STATE after off = %v (ok=%v), want false", got, ok)
		}
	})

	// SEND — a raw attribute write (as opposed to the on/off/toggle
	// command invocations above) must land the same way: MatterWrite
	// accepts OnOff.OnOff as a plain boolean write onto STATE.
	t.Run("send/write-on-off-attribute", func(t *testing.T) {
		if _, err := b.SharedCtl.WriteAttr(ctx, t, "onoff", "on-off", "1", ep); err != nil {
			t.Fatalf("write on-off=1: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "STATE")
		if !ok {
			t.Fatalf("STATE absent on CCU after attribute write")
		}
		if !onoffIsTrue(got) {
			t.Fatalf("CCU STATE = %v after write on-off=1, want true", got)
		}
	})

	// RECEIVE — an external STATE change (device button press / CCU
	// program) must reach the controller as a proactive OnOff report.
	// The send cell above left STATE=true, so injecting false here is
	// distinct from the pre-state and the subscribe's own initial
	// report cannot pre-satisfy want(). OnOff is non-nullable: a
	// successful FindAttrBool match (rather than a TLV-null encoding,
	// which chip-tool would refuse to parse as CHIP 0x26) is itself
	// the non-nullability assertion — switch.Switch implements
	// MatterClusterServer, so the change-notifier only ever reports a
	// concrete boolean for OnOff.
	t.Run("receive/on-off", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"onoff", "on-off", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "STATE", false) },
			func(out string) bool {
				v, ok := harness.FindAttrBool(out, "OnOff")
				return ok && !v
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive OnOff=false: %v\n%s", err, out)
		}
	})

	// Housekeeping: leave the simulator OFF via one final explicit
	// invoke — mirrors the live-CCU discipline (never trust the last
	// cell's terminal state to be OFF on its own) even though this
	// suite runs against godevccu.
	if _, err := b.SharedCtl.Invoke(ctx, t, "onoff", "off", ep); err != nil {
		t.Errorf("housekeeping off: %v", err)
	}
}

// onoffIsTrue and onoffIsFalse interpret the CCU-side STATE ground
// truth returned by [harness.MockCCU.GetDPValue]. STATE is a BOOL
// paramset value, but GetDPValue's `any` return is asserted
// defensively rather than blindly type-switched, so an unexpected
// encoding fails the calling assertion instead of panicking.
func onoffIsTrue(v any) bool {
	b, ok := v.(bool)
	return ok && b
}

func onoffIsFalse(v any) bool {
	b, ok := v.(bool)
	return ok && !b
}
