// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// errWriter always fails the write — used to exercise the rollback path
// on a rejected CCU command (matterOnWithTimedOff's transactional
// revert, mirroring matter.js's synchronous state mutations that only
// commit once on()/off() succeeds).
type errWriter struct{ err error }

func (w *errWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	return w.err
}

// timedRig builds a dimmable-light rig with the countdown wall-clock
// loop disabled, so tests drive time deterministically via
// l.matterTimedAdvance instead of racing a real 100 ms ticker. The
// loop must be disabled before anything arms a countdown.
func timedRig(t *testing.T, w Writer) *Light {
	t.Helper()
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.timed.loopDisabled = true
	return l
}

// TestOnWithTimedOffArmsAndExpiryTurnsOff verifies the timed-on
// countdown decrements one tenth-of-a-second per matterTimedAdvance
// step and autonomously turns the light off at expiry, clearing
// OffWaitTime along with it. Mirrors matter.js OnOffServer.ts:198-225
// (onWithTimedOff) and :239-253 (#timedOnTick).
func TestOnWithTimedOffArmsAndExpiryTurnsOff(t *testing.T) {
	w := &stubWriter{}
	l := timedRig(t, w)
	l.OnLevel(0.5)

	srv := onOffServer(t, l)
	fields := map[uint8]any{0: uint64(0), 1: uint64(3), 2: uint64(2)}
	if _, err := srv.MatterInvoke(context.Background(), 0x42, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("OnWithTimedOff arm error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 3 {
		t.Fatalf("OnTime after arm = (%v, %v), want (3, true)", v, ok)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 2 {
		t.Fatalf("OffWaitTime after arm = (%v, %v), want (2, true)", v, ok)
	}
	if on, _ := l.IsOn(); !on {
		t.Fatal("light must stay on immediately after arming")
	}

	l.matterTimedAdvance(context.Background())
	l.matterTimedAdvance(context.Background())
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 1 {
		t.Fatalf("OnTime after 2 advances = (%v, %v), want (1, true)", v, ok)
	}

	l.matterTimedAdvance(context.Background())
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 0 {
		t.Fatalf("OnTime after expiry = (%v, %v), want (0, true)", v, ok)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 0 {
		t.Fatalf("OffWaitTime after expiry = (%v, %v), want (0, true)", v, ok)
	}
	if w.last != 0.0 {
		t.Fatalf("expiry did not turn the light off: writer saw %v, want 0.0", w.last)
	}
}

// TestOnWithTimedOffAcceptOnlyWhenOnWhileOffIsNoOp verifies the
// AcceptOnlyWhenOn (OnOffControl bit 0) gate: while the device is off,
// OnWithTimedOff is a silent Success no-op — no write, no countdown
// armed. Mirrors matter.js OnOffServer.ts:199-201.
func TestOnWithTimedOffAcceptOnlyWhenOnWhileOffIsNoOp(t *testing.T) {
	w := &stubWriter{last: -1} // sentinel: distinguishes "no write" from "wrote 0"
	l := timedRig(t, w)
	l.OnLevel(0.0)

	srv := onOffServer(t, l)
	fields := map[uint8]any{0: uint64(1), 1: uint64(5), 2: uint64(5)}
	if _, err := srv.MatterInvoke(context.Background(), 0x42, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("OnWithTimedOff while off error: %v", err)
	}
	if w.last != -1 {
		t.Fatalf("AcceptOnlyWhenOn while off must not write, writer saw %v", w.last)
	}
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 0 {
		t.Fatalf("OnTime after no-op = (%v, %v), want (0, true)", v, ok)
	}
}

// TestOnWithTimedOffAcceptOnlyWhenOnWhileOnExecutes verifies the
// AcceptOnlyWhenOn gate passes through when the device is already on,
// arming the countdown exactly as a plain OnWithTimedOff would. Mirrors
// matter.js OnOffServer.ts:199-201 (the inverse of the off-gate case).
func TestOnWithTimedOffAcceptOnlyWhenOnWhileOnExecutes(t *testing.T) {
	w := &stubWriter{}
	l := timedRig(t, w)
	l.OnLevel(0.5)

	srv := onOffServer(t, l)
	fields := map[uint8]any{0: uint64(1), 1: uint64(6), 2: uint64(9)}
	if _, err := srv.MatterInvoke(context.Background(), 0x42, fields, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("OnWithTimedOff while on error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 6 {
		t.Fatalf("OnTime after execute = (%v, %v), want (6, true)", v, ok)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 9 {
		t.Fatalf("OffWaitTime after execute = (%v, %v), want (9, true)", v, ok)
	}
	if on, _ := l.IsOn(); !on {
		t.Fatal("light must remain on")
	}
}

// TestOnWithTimedOffDelayedOffGuard walks the full delayed-off guard
// life cycle: an Off while OffWaitTime > 0 parks the device off and
// starts the guard countdown; a subsequent gated OnWithTimedOff while
// off is a no-op; an ungated OnWithTimedOff while off only lowers
// OffWaitTime and never turns the device back on; the guard ends on
// its own expiry without a turn-on. Mirrors matter.js
// OnOffServer.ts:119-139 (off) and :203-214 (onWithTimedOff's
// off-guard branch).
func TestOnWithTimedOffDelayedOffGuard(t *testing.T) {
	w := &stubWriter{}
	l := timedRig(t, w)
	l.OnLevel(0.5)
	srv := onOffServer(t, l)

	arm := map[uint8]any{0: uint64(0), 1: uint64(5), 2: uint64(10)}
	if _, err := srv.MatterInvoke(context.Background(), 0x42, arm, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("arm error: %v", err)
	}

	if _, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Off error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 0 {
		t.Fatalf("OnTime after Off = (%v, %v), want (0, true)", v, ok)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 10 {
		t.Fatalf("OffWaitTime after Off = (%v, %v), want (10, true)", v, ok)
	}
	if on, _ := l.IsOn(); on {
		t.Fatal("light must be off after Off")
	}
	if w.last != 0.0 {
		t.Fatalf("Off did not write LEVEL=0, writer saw %v", w.last)
	}

	// AcceptOnlyWhenOn while off inside the guard → no-op.
	gated := map[uint8]any{0: uint64(1), 1: uint64(0), 2: uint64(3)}
	if _, err := srv.MatterInvoke(context.Background(), 0x42, gated, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("gated OnWithTimedOff error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 10 {
		t.Fatalf("OffWaitTime after gated no-op = (%v, %v), want (10, true) unchanged", v, ok)
	}

	// Ungated OnWithTimedOff while off only lowers OffWaitTime.
	lower := map[uint8]any{0: uint64(0), 1: uint64(0), 2: uint64(4)}
	if _, err := srv.MatterInvoke(context.Background(), 0x42, lower, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("lowering OnWithTimedOff error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 4 {
		t.Fatalf("OffWaitTime after lowering = (%v, %v), want (4, true)", v, ok)
	}
	if on, _ := l.IsOn(); on {
		t.Fatal("device must stay off during the guard")
	}
	if w.last != 0.0 {
		t.Fatalf("lowering OffWaitTime must not turn the device on, writer saw %v", w.last)
	}

	for range 4 {
		l.matterTimedAdvance(context.Background())
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 0 {
		t.Fatalf("OffWaitTime after guard expiry = (%v, %v), want (0, true)", v, ok)
	}
	if on, _ := l.IsOn(); on {
		t.Fatal("guard expiry must not turn the device back on")
	}
	if w.last != 0.0 {
		t.Fatalf("guard expiry must not write, writer saw %v", w.last)
	}
}

// TestOnCancelsDelayedOffGuard verifies a plain On clears the guard's
// OffWaitTime immediately, without waiting for the countdown to
// expire. Mirrors matter.js OnOffServer.ts:97-112 (on()).
func TestOnCancelsDelayedOffGuard(t *testing.T) {
	w := &stubWriter{}
	l := timedRig(t, w)
	l.OnLevel(0.5)
	srv := onOffServer(t, l)

	arm := map[uint8]any{0: uint64(0), 1: uint64(5), 2: uint64(8)}
	if _, err := srv.MatterInvoke(context.Background(), 0x42, arm, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("arm error: %v", err)
	}
	if _, err := srv.MatterInvoke(context.Background(), 0x00, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Off error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 0 {
		t.Fatalf("precondition: OnTime after Off = (%v, %v), want (0, true)", v, ok)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 8 {
		t.Fatalf("precondition: OffWaitTime after Off = (%v, %v), want (8, true)", v, ok)
	}

	if _, err := srv.MatterInvoke(context.Background(), 0x01, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("On error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 0 {
		t.Fatalf("OffWaitTime after On = (%v, %v), want (0, true)", v, ok)
	}
}

// TestOnWithTimedOffOnTimeTakesMaxOfRequestAndCurrent verifies a
// re-arm never shortens a running countdown: OnTime becomes
// max(request, current), only rising when the request exceeds it.
// Mirrors matter.js OnOffServer.ts:216.
func TestOnWithTimedOffOnTimeTakesMaxOfRequestAndCurrent(t *testing.T) {
	w := &stubWriter{}
	l := timedRig(t, w)
	l.OnLevel(0.5)
	srv := onOffServer(t, l)

	first := map[uint8]any{0: uint64(0), 1: uint64(10), 2: uint64(0)}
	if _, err := srv.MatterInvoke(context.Background(), 0x42, first, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("first arm error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 10 {
		t.Fatalf("OnTime after first arm = (%v, %v), want (10, true)", v, ok)
	}

	lower := map[uint8]any{0: uint64(0), 1: uint64(4), 2: uint64(0)}
	if _, err := srv.MatterInvoke(context.Background(), 0x42, lower, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("lower re-arm error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 10 {
		t.Fatalf("OnTime after lower re-arm = (%v, %v), want (10, true) unchanged", v, ok)
	}

	higher := map[uint8]any{0: uint64(0), 1: uint64(20), 2: uint64(0)}
	if _, err := srv.MatterInvoke(context.Background(), 0x42, higher, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("higher re-arm error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 20 {
		t.Fatalf("OnTime after higher re-arm = (%v, %v), want (20, true)", v, ok)
	}
}

// TestOnTimeAttributeWriteStopsCountdownAtHoldOrZero verifies an
// OnTime attribute write parks (or ends) a running countdown without
// ever starting one: the 0xFFFF hold and a plain 0 both stop the
// timed-on tick, and a write on a fresh (never-armed) light leaves
// OnTime untouched by later advances since there was never a running
// countdown to stop. Mirrors matter.js OnOffServer.ts:66-84
// (#stopHeldTimer).
func TestOnTimeAttributeWriteStopsCountdownAtHoldOrZero(t *testing.T) {
	t.Run("hold", func(t *testing.T) {
		w := &stubWriter{}
		l := timedRig(t, w)
		l.OnLevel(0.5)
		srv := onOffServer(t, l)
		arm := map[uint8]any{0: uint64(0), 1: uint64(5), 2: uint64(0)}
		if _, err := srv.MatterInvoke(context.Background(), 0x42, arm, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("arm error: %v", err)
		}
		if err := srv.MatterWrite(context.Background(), 0x4001, uint64(0xFFFF), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("OnTime hold write error: %v", err)
		}
		l.matterTimedAdvance(context.Background())
		l.matterTimedAdvance(context.Background())
		if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 0xFFFF {
			t.Fatalf("OnTime after hold write + advance = (%v, %v), want (0xFFFF, true)", v, ok)
		}
	})

	t.Run("zero", func(t *testing.T) {
		w := &stubWriter{}
		l := timedRig(t, w)
		l.OnLevel(0.5)
		srv := onOffServer(t, l)
		arm := map[uint8]any{0: uint64(0), 1: uint64(5), 2: uint64(0)}
		if _, err := srv.MatterInvoke(context.Background(), 0x42, arm, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("arm error: %v", err)
		}
		if err := srv.MatterWrite(context.Background(), 0x4001, uint64(0), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("OnTime zero write error: %v", err)
		}
		w.last = -1 // sentinel: any further write would overwrite this
		l.matterTimedAdvance(context.Background())
		if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 0 {
			t.Fatalf("OnTime after zero write + advance = (%v, %v), want (0, true)", v, ok)
		}
		if w.last != -1 {
			t.Fatalf("parked-at-zero countdown must not trigger a write, writer saw %v", w.last)
		}
	})

	t.Run("write never starts a countdown", func(t *testing.T) {
		w := &stubWriter{}
		l := timedRig(t, w)
		srv := onOffServer(t, l)
		if err := srv.MatterWrite(context.Background(), 0x4001, uint64(5), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("OnTime write error: %v", err)
		}
		l.matterTimedAdvance(context.Background())
		l.matterTimedAdvance(context.Background())
		if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 5 {
			t.Fatalf("OnTime after write-only + advances = (%v, %v), want (5, true) unchanged", v, ok)
		}
	})
}

// TestOnOffTimedAttributeWriteReadRoundTrip round-trips OffWaitTime
// and the nullable StartUpOnOff attribute through MatterWrite /
// MatterRead. StartUpOnOff constraint is enum8 0..2 per matter.js
// on-off.element.ts:33-36.
func TestOnOffTimedAttributeWriteReadRoundTrip(t *testing.T) {
	l := timedRig(t, &stubWriter{})
	srv := onOffServer(t, l)

	if err := srv.MatterWrite(context.Background(), 0x4002, uint64(7), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("OffWaitTime write error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 7 {
		t.Fatalf("OffWaitTime read = (%v, %v), want (7, true)", v, ok)
	}

	if err := srv.MatterWrite(context.Background(), 0x4003, uint64(1), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("StartUpOnOff write(1) error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4003); !ok || v.(uint8) != 1 {
		t.Fatalf("StartUpOnOff read = (%v, %v), want (1, true)", v, ok)
	}

	if err := srv.MatterWrite(context.Background(), 0x4003, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("StartUpOnOff write(null) error: %v", err)
	}
	if v, ok := srv.MatterRead(0x4003); !ok || v != nil {
		t.Fatalf("StartUpOnOff read after null write = (%v, %v), want (nil, true)", v, ok)
	}

	err := srv.MatterWrite(context.Background(), 0x4003, uint64(3), hmenum.CommandPriorityHigh)
	if err == nil || !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("StartUpOnOff write(3) error = %v, want a constraint error", err)
	}
}

// TestOnOffTimedMinWritePrivilege verifies StartUpOnOff requires
// Manage (4) while the countdown attributes stay at the RW VO default
// (Operate, 3). matter.js on-off.element.ts:34.
func TestOnOffTimedMinWritePrivilege(t *testing.T) {
	l := timedRig(t, &stubWriter{})
	srv := onOffServer(t, l)
	if got := srv.MinWritePrivilege(0x4003); got != 4 {
		t.Fatalf("StartUpOnOff MinWritePrivilege = %d, want 4", got)
	}
	if got := srv.MinWritePrivilege(0x4001); got != 3 {
		t.Fatalf("OnTime MinWritePrivilege = %d, want 3", got)
	}
}

// TestOnWithTimedOffConstraintErrorsOnCommandFields verifies the
// OnOffControl and OnTime/OffWaitTime command-field constraints reject
// out-of-range values before the engine executes. matter.js
// on-off.element.ts:50-55.
func TestOnWithTimedOffConstraintErrorsOnCommandFields(t *testing.T) {
	l := timedRig(t, &stubWriter{})
	srv := onOffServer(t, l)

	_, err := srv.MatterInvoke(context.Background(), 0x42, map[uint8]any{0: uint64(2)}, hmenum.CommandPriorityHigh)
	if err == nil || !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("OnOffControl=2 error = %v, want a constraint error", err)
	}

	_, err = srv.MatterInvoke(context.Background(), 0x42, map[uint8]any{1: uint64(0xFFFF)}, hmenum.CommandPriorityHigh)
	if err == nil || !strings.Contains(err.Error(), "constraint") {
		t.Fatalf("OnTime=0xFFFF error = %v, want a constraint error", err)
	}
}

// TestOnWithTimedOffTurnOnFailureRollsBackArm verifies a rejected CCU
// write rolls the just-armed countdown back to its pre-arm state —
// matter.js's synchronous state mutations only commit once on()
// succeeds, so a failed write must not leave a ticking countdown
// behind.
func TestOnWithTimedOffTurnOnFailureRollsBackArm(t *testing.T) {
	w := &errWriter{err: errors.New("ccu unreachable")}
	l, _ := newLightRig(t, "HmIP-BDT:4", w, custom.LightCapabilities{Dimmable: true})
	l.timed.loopDisabled = true

	srv := onOffServer(t, l)
	fields := map[uint8]any{0: uint64(0), 1: uint64(5), 2: uint64(0)}
	_, err := srv.MatterInvoke(context.Background(), 0x42, fields, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected the write failure to propagate")
	}
	if v, ok := srv.MatterRead(0x4001); !ok || v.(uint16) != 0 {
		t.Fatalf("OnTime after rollback = (%v, %v), want (0, true) — no ticking countdown left behind", v, ok)
	}
	if v, ok := srv.MatterRead(0x4002); !ok || v.(uint16) != 0 {
		t.Fatalf("OffWaitTime after rollback = (%v, %v), want (0, true)", v, ok)
	}
}
