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

// TestSendReceive_DoorLock is the DoorLock (0x0101) cell of the
// chip-tool <-> daemon <-> godevccu send/receive matrix. HmIP-DLD
// (lock.Lock KindIP) is the only godevccu fleet device that reaches
// DoorLock.
//
//   - SEND: `lock-door` / `unlock-door` must land on the simulated CCU
//     as LOCK_TARGET_LEVEL ("LOCKED"/"UNLOCKED"), asserted via
//     GetDPValue ground truth.
//   - SEND (negative): every DoorLock attribute is read-only — a
//     controller WRITE must be rejected by the dispatcher's read-only
//     gate, never reach the Lock model.
//   - RECEIVE: a device-originated LOCK_STATE push must reach the
//     controller as a PROACTIVE LockState report (via
//     AwaitProactiveReport — subscribe first, then fire, so a broken
//     change-notifier is caught rather than masked by the subscribe's
//     initial read).
//
// Writes go against the in-process simulator; the live CCU is never
// touched.
func TestSendReceive_DoorLock(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0101, 1)
	if len(eps) == 0 {
		t.Skip("no DoorLock endpoint — godevccu fleet lacks a HmIP-DLD device")
	}
	ep := eps[0]
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	address, _, ok := b.ResolveCCUAddress(ctx, t, ep)
	if !ok {
		t.Fatalf("could not resolve CCU address for doorlock endpoint %d", ep)
	}

	// SEND — `lock-door` must reach the CCU as LOCK_TARGET_LEVEL="LOCKED".
	t.Run("send/lock-door", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "doorlock", "lock-door", ep); err != nil {
			t.Fatalf("invoke lock-door: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "LOCK_TARGET_LEVEL")
		if !ok {
			t.Fatalf("LOCK_TARGET_LEVEL absent on CCU after lock-door")
		}
		if s, isStr := got.(string); !isStr || s != "LOCKED" {
			t.Fatalf("CCU LOCK_TARGET_LEVEL = %v (%T), want LOCKED", got, got)
		}
	})

	// SEND — `unlock-door` must reach the CCU as LOCK_TARGET_LEVEL="UNLOCKED".
	t.Run("send/unlock-door", func(t *testing.T) {
		if _, err := b.SharedCtl.Invoke(ctx, t, "doorlock", "unlock-door", ep); err != nil {
			t.Fatalf("invoke unlock-door: %v", err)
		}
		got, ok := b.CCU.GetDPValue(address, "LOCK_TARGET_LEVEL")
		if !ok {
			t.Fatalf("LOCK_TARGET_LEVEL absent on CCU after unlock-door")
		}
		if s, isStr := got.(string); !isStr || s != "UNLOCKED" {
			t.Fatalf("CCU LOCK_TARGET_LEVEL = %v (%T), want UNLOCKED", got, got)
		}
	})

	// SEND (negative) — every DoorLock attribute is read-only; a
	// controller WRITE must be rejected before it ever reaches the
	// Lock model, not silently accepted.
	t.Run("send/write-rejected", func(t *testing.T) {
		out, _ := b.SharedCtl.WriteAttr(ctx, t, "doorlock", "lock-state", "1", ep)
		statusHex, ok := harness.WriteStatus(out)
		if !ok {
			t.Fatalf("no IM status parsed from doorlock LockState write attempt:\n%s", out)
		}
		if statusHex == "0x0" {
			t.Fatalf("doorlock LockState write unexpectedly succeeded (status=0x0) — attribute must be read-only:\n%s", out)
		}
		t.Logf("doorlock LockState write rejected with status %s (expect 0x88 UNSUPPORTED_WRITE)", statusHex)
	})

	// RECEIVE — an external LOCK_STATE="LOCKED" push must reach the
	// controller as a proactive LockState=1 (Locked) report.
	t.Run("receive/lock-state-locked", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"doorlock", "lock-state", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "LOCK_STATE", "LOCKED") },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "LockState")
				return ok && v == 1
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive LockState=1 (Locked): %v\n%s", err, out)
		}
	})

	// RECEIVE — an external LOCK_STATE="UNLOCKED" push must reach the
	// controller as a proactive LockState=2 (Unlocked) report. Fired
	// after the "locked" cell above so the pre-state (Locked) differs
	// from the injected value and the subscribe's initial report
	// cannot pre-satisfy want().
	t.Run("receive/lock-state-unlocked", func(t *testing.T) {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"doorlock", "lock-state", ep,
			func() error { return b.CCU.FireDeviceEvent(address, "LOCK_STATE", "UNLOCKED") },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "LockState")
				return ok && v == 2
			},
			30*time.Second)
		if err != nil {
			t.Fatalf("await proactive LockState=2 (Unlocked): %v\n%s", err, out)
		}
	})
}
