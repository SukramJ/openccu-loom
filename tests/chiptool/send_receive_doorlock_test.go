// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package chiptool

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// doorlockTarget pairs a bridged DoorLock (0x0101) endpoint with the
// CCU channel address that backs the real lock's LOCK_STATE /
// LOCK_TARGET_LEVEL parameter.
type doorlockTarget struct {
	ep      uint16
	address string
}

// TestSendReceive_DoorLock is the DoorLock (0x0101) cell of the
// chip-tool <-> daemon <-> godevccu send/receive matrix.
//
// DoorLock is mounted for TWO distinct reasons and both surface the
// 0x0101 cluster, so the endpoint set is deliberately disambiguated:
//
//   - The real HmIP-DLD lock drive (lock.Lock, KindIP) projects
//     LOCK_TARGET_LEVEL/LOCK_STATE on its DOOR_LOCK_STATE_TRANSMITTER
//     channel — the door lock we actually test.
//   - GLOBAL_BUTTON_LOCK — a device's child-lock — is an
//     aiohomematic-parity ButtonLock lock entity (DeviceProfileIP/
//     RFButtonLock) that rides channel 0 on many HmIP devices (the
//     wall thermostat included). It legitimately materialises a
//     DoorLock endpoint too, but carries no LOCK_STATE.
//
// The two are indistinguishable on the Matter wire (same cluster, same
// SerialNumber for a device that has both), so we filter to endpoints
// whose CCU device exposes a LOCK_STATE/LOCK_TARGET_LEVEL row and, for
// RECEIVE, trial each until the injected LOCK_STATE surfaces.
//
//   - SEND (negative): every DoorLock attribute is read-only — a
//     controller WRITE must be rejected by the dispatcher's read-only
//     gate, never reach the Lock model.
//   - RECEIVE: a device-originated LOCK_STATE push must reach the
//     controller as a PROACTIVE LockState report.
//
// Writes go against the in-process simulator; the live CCU is never
// touched.
func TestSendReceive_DoorLock(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0101, 0)
	if len(eps) == 0 {
		t.Skip("no DoorLock endpoint — godevccu fleet lacks a HmIP-DLD device")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// Keep only endpoints whose device backs a real door lock
	// (LOCK_TARGET_LEVEL / LOCK_STATE), dropping the GLOBAL_BUTTON_LOCK
	// child-lock endpoints. The preferDPKeys argument steers
	// ResolveCCUAddress to the real-lock channel when a device carries
	// both; a device with only a button-lock resolves to BUTTON_LOCK and
	// is filtered out here.
	var locks []doorlockTarget
	for _, ep := range eps {
		addr, dpKey, ok := b.ResolveCCUAddress(ctx, t, ep, 0x0101, "LOCK_TARGET_LEVEL", "LOCK_STATE")
		if ok && (dpKey == "LOCK_TARGET_LEVEL" || dpKey == "LOCK_STATE") {
			locks = append(locks, doorlockTarget{ep: ep, address: addr})
		}
	}
	if len(locks) == 0 {
		t.Skip("no real door-lock endpoint — godevccu fleet exposes only GLOBAL_BUTTON_LOCK ButtonLock endpoints")
	}

	// SEND — `lock-door` must reach the CCU as LOCK_TARGET_LEVEL="LOCKED".
	t.Run("send/lock-door", func(t *testing.T) {
		t.Skip("WIP: DoorLock LockDoor/UnlockDoor require a Timed Invoke per Matter spec; the harness Invoke does not send --timedInteractionTimeoutMs so chip-tool exits non-zero. Deferred to a follow-up that adds a timed-invoke helper.")
	})

	// SEND — `unlock-door` must reach the CCU as LOCK_TARGET_LEVEL="UNLOCKED".
	t.Run("send/unlock-door", func(t *testing.T) {
		t.Skip("WIP: DoorLock UnlockDoor requires a Timed Invoke per Matter spec; see send/lock-door — deferred to a timed-invoke helper follow-up.")
	})

	// SEND (negative) — every DoorLock attribute is read-only; a
	// controller WRITE must be rejected before it ever reaches the
	// Lock model, not silently accepted. Any real-lock endpoint hosts
	// the same read-only gate.
	t.Run("send/write-rejected", func(t *testing.T) {
		out, _ := b.SharedCtl.WriteAttr(ctx, t, "doorlock", "lock-state", "1", locks[0].ep)
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
		if err := doorlockAwaitReceive(ctx, t, b, locks, "LOCKED", 1); err != nil {
			t.Fatalf("await proactive LockState=1 (Locked): %v", err)
		}
	})

	// RECEIVE — an external LOCK_STATE="UNLOCKED" push must reach the
	// controller as a proactive LockState=2 (Unlocked) report. Fired
	// after the "locked" cell above so the pre-state (Locked) differs
	// from the injected value and the subscribe's initial report cannot
	// pre-satisfy want().
	t.Run("receive/lock-state-unlocked", func(t *testing.T) {
		if err := doorlockAwaitReceive(ctx, t, b, locks, "UNLOCKED", 2); err != nil {
			t.Fatalf("await proactive LockState=2 (Unlocked): %v", err)
		}
	})
}

// doorlockAwaitReceive injects LOCK_STATE on each real-lock candidate's
// CCU channel and finds the Matter endpoint that surfaces the resulting
// proactive LockState report. A door-lock device can expose more than
// one DoorLock endpoint (its GLOBAL_BUTTON_LOCK child-lock plus the real
// LOCK_TARGET_LEVEL both mount 0x0101), indistinguishable on the wire —
// only the endpoint backing the injected channel reports the change —
// so each candidate is trialled until one matches. Every candidate on a
// given device resolves to the same real-lock channel, so the injection
// target is stable across the trial.
func doorlockAwaitReceive(ctx context.Context, t *testing.T, b *harness.Bridge, locks []doorlockTarget, lockState string, want int64) error {
	t.Helper()
	var lastErr error
	for _, l := range locks {
		out, err := harness.AwaitProactiveReport(ctx, t, b.SharedCtl,
			"doorlock", "lock-state", l.ep,
			func() error { return b.FireDeviceEventEnum(t, l.address, "LOCK_STATE", lockState) },
			func(out string) bool {
				v, ok := harness.FindAttrUint(out, "LockState")
				return ok && v == want
			},
			20*time.Second)
		if err == nil {
			return nil
		}
		lastErr = fmt.Errorf("endpoint %d (%s): %w\n%s", l.ep, l.address, err, out)
	}
	return lastErr
}
