// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package lock_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Raw wire indices of the HmIP-DLD channel-1 ENUMs, in the order of the
// device's VALUE_LIST: LOCK_STATE [UNKNOWN, LOCKED, UNLOCKED] and
// ACTIVITY_STATE [UNKNOWN, UP, DOWN, STABLE].
const (
	dldLocked   int32 = 1
	dldUnlocked int32 = 2

	dldActUnknown int32 = 0
	dldActUp      int32 = 1
	dldActDown    int32 = 2
	dldActStable  int32 = 3
)

// movementStep is one recorded multicall: LOCK_STATE then ACTIVITY_STATE,
// and the (locking, unlocking, opening) triple the lock must report after it.
type movementStep struct {
	lockState, activity int32
	locking, unlocking  bool
	opening             bool
}

type movementRig struct {
	state    *generic.Sensor[int32]
	activity *generic.Sensor[int32]
}

func newMovementRig(t *testing.T) (*movementRig, *device.Channel) {
	t.Helper()
	chs := newIPLockDevice(t, "HmIP-DLD", 1, 0)
	ch := chs[1]
	state, ok := ch.Parameter(hmenum.ParameterLockState).(*generic.Sensor[int32])
	if !ok {
		t.Fatal("LOCK_STATE is not an enum sensor")
	}
	activity, ok := ch.Parameter(hmenum.ParameterActivityState).(*generic.Sensor[int32])
	if !ok {
		t.Fatal("ACTIVITY_STATE is not an enum sensor")
	}
	return &movementRig{state: state, activity: activity}, ch
}

// TestIPLockLatchOpeningIsNotReportedAsLocking replays the LOCK_STATE /
// ACTIVITY_STATE multicalls a real HmIP-DLD sent for lock, unlock and an
// opening from either bolt position, step for step as captured in an
// XML-RPC callback log.
//
// Opening the latch moves the motor UP while LOCK_STATE is already
// UNLOCKED, then DOWN back to the unlocked position, then STABLE. That DOWN
// used to be read as locking, so a client saw "unlocking → locking →
// unlocked" for an action that never touched the bolt.
func TestIPLockLatchOpeningIsNotReportedAsLocking(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		initial int32
		steps   []movementStep
	}{
		{"lock", dldUnlocked, []movementStep{
			{dldUnlocked, dldActDown, true, false, false},
			{dldLocked, dldActStable, false, false, false},
		}},
		{"unlock", dldLocked, []movementStep{
			{dldLocked, dldActUp, false, true, false},
			{dldUnlocked, dldActStable, false, false, false},
		}},
		{"open_from_unlocked", dldUnlocked, []movementStep{
			{dldUnlocked, dldActUp, false, false, true},
			{dldUnlocked, dldActUp, false, false, true},
			{dldUnlocked, dldActDown, false, false, true},
			{dldUnlocked, dldActStable, false, false, false},
		}},
		{"open_from_locked", dldLocked, []movementStep{
			{dldUnlocked, dldActUp, false, false, true},
			{dldUnlocked, dldActDown, false, false, true},
			{dldUnlocked, dldActStable, false, false, false},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r, ch := newMovementRig(t)
			l := lockOn(t, ch)

			r.state.OnEvent(tc.initial)
			r.activity.OnEvent(dldActStable)

			for i, s := range tc.steps {
				r.state.OnEvent(s.lockState)
				r.activity.OnEvent(s.activity)
				got := [3]bool{l.IsLocking(), l.IsUnlocking(), l.IsOpening()}
				want := [3]bool{s.locking, s.unlocking, s.opening}
				if got != want {
					t.Errorf("step %d (LOCK_STATE=%d, ACTIVITY_STATE=%d): (locking, unlocking, opening) = %v, want %v",
						i, s.lockState, s.activity, got, want)
				}
				st, ok := l.State().(*payload.LockState)
				if !ok {
					t.Fatalf("State() is %T, want *payload.LockState", l.State())
				}
				if [3]bool{st.IsLocking, st.IsUnlocking, st.IsOpening} != want {
					t.Errorf("step %d: payload (is_locking, is_unlocking, is_opening) = %v, want %v",
						i, [3]bool{st.IsLocking, st.IsUnlocking, st.IsOpening}, want)
				}
			}
		})
	}
}

// TestIPLockOpenMovementIndependentOfEventOrder feeds ACTIVITY_STATE before
// LOCK_STATE within each multicall. Opening from locked then sees UP while
// the bolt still reads LOCKED; the following LOCK_STATE=UNLOCKED must still
// start the open movement, so the return DOWN is not reported as locking.
func TestIPLockOpenMovementIndependentOfEventOrder(t *testing.T) {
	t.Parallel()
	r, ch := newMovementRig(t)
	l := lockOn(t, ch)

	r.state.OnEvent(dldLocked)
	r.activity.OnEvent(dldActStable)

	r.activity.OnEvent(dldActUp)
	r.state.OnEvent(dldUnlocked)
	if !l.IsOpening() || l.IsUnlocking() {
		t.Errorf("after UP + UNLOCKED: opening=%v unlocking=%v, want opening only", l.IsOpening(), l.IsUnlocking())
	}
	r.activity.OnEvent(dldActDown)
	if l.IsLocking() || !l.IsOpening() {
		t.Errorf("return DOWN: locking=%v opening=%v, want opening only", l.IsLocking(), l.IsOpening())
	}
}

// TestIPLockOpenMovementEndsOnUnknown asserts that UNKNOWN ends an open
// movement as STABLE does, so a later DOWN is a locking movement again.
func TestIPLockOpenMovementEndsOnUnknown(t *testing.T) {
	t.Parallel()
	r, ch := newMovementRig(t)
	l := lockOn(t, ch)

	r.state.OnEvent(dldUnlocked)
	r.activity.OnEvent(dldActUp)
	if !l.IsOpening() {
		t.Fatal("UP while UNLOCKED must report opening")
	}
	r.activity.OnEvent(dldActUnknown)
	if l.IsOpening() {
		t.Error("UNKNOWN must end the open movement")
	}
	r.activity.OnEvent(dldActDown)
	if !l.IsLocking() || l.IsOpening() {
		t.Errorf("DOWN after UNKNOWN: locking=%v opening=%v, want locking only", l.IsLocking(), l.IsOpening())
	}
}
