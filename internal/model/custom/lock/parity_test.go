// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Parity tests for the Lock custom data point.
// Each test function maps to one semantic from the Python reference.

package lock

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestParityRFLockUnlockWritesStateTrue verifies RF unlock writes STATE=true.
// Mirrors test_rf_lock_functionality → "unlock() → STATE=True".
func TestParityRFLockUnlockWritesStateTrue(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "VCU0000146:1", KindRF, w, custom.LockCapabilities{})
	if err := r.lock.Unlock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterState || got.value != true {
		t.Fatalf("RF unlock wrote %+v, want STATE=true", got)
	}
}

// TestParityRFLockLockWritesStateFalse verifies RF lock writes STATE=false.
// Mirrors test_rf_lock_functionality → "lock() → STATE=False".
func TestParityRFLockLockWritesStateFalse(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "VCU0000146:1", KindRF, w, custom.LockCapabilities{})
	if err := r.lock.Lock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterState || got.value != false {
		t.Fatalf("RF lock wrote %+v, want STATE=false", got)
	}
}

// TestParityRFLockOpenUsesOpenParameter verifies RF open writes OPEN=true
// when the capability allows it. Mirrors test_rf_lock_functionality →
// "open() → OPEN=True".
func TestParityRFLockOpenUsesOpenParameter(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "VCU0000146:1", KindRF, w, custom.LockCapabilities{SupportsOpen: true})
	if err := r.lock.Open(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterOpen || got.value != true {
		t.Fatalf("RF open wrote %+v, want OPEN=true", got)
	}
}

// TestParityRFLockOpenGated verifies that Open returns ErrNotSupported when
// the capability is absent. Mirrors test_rf_lock → "open without capability".
func TestParityRFLockOpenGated(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindRF, &stubWriter{}, custom.LockCapabilities{})
	if err := r.lock.Open(context.Background(), hmenum.CommandPriorityHigh); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("got %v, want ErrNotSupported", err)
	}
}

// TestParityIPLockLockWritesTargetLevelLocked verifies IP lock writes the
// correct LOCK_TARGET_LEVEL for the locked state. Mirrors
// test_ip_lock_functionality → "lock() → LOCK_TARGET_LEVEL=ipTargetLocked".
func TestParityIPLockLockWritesTargetLevelLocked(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{SupportsOpen: true})
	if err := r.lock.Lock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterLockTargetLevel || got.value.(string) != ipTargetLocked {
		t.Fatalf("IP lock wrote %+v, want LOCK_TARGET_LEVEL=%q", got, ipTargetLocked)
	}
}

// TestParityIPLockUnlockWritesTargetLevelUnlocked verifies IP unlock writes
// LOCK_TARGET_LEVEL=ipTargetUnlocked. Mirrors test_ip_lock → unlock.
func TestParityIPLockUnlockWritesTargetLevelUnlocked(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{SupportsOpen: true})
	if err := r.lock.Unlock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.value.(string) != ipTargetUnlocked {
		t.Fatalf("IP unlock wrote %+v, want LOCK_TARGET_LEVEL=%q", got, ipTargetUnlocked)
	}
}

// TestParityIPLockOpenWritesTargetLevelOpen verifies IP open writes
// LOCK_TARGET_LEVEL=ipTargetOpen. Mirrors test_ip_lock → open.
func TestParityIPLockOpenWritesTargetLevelOpen(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{SupportsOpen: true})
	if err := r.lock.Open(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.value.(string) != ipTargetOpen {
		t.Fatalf("IP open wrote %+v, want LOCK_TARGET_LEVEL=%q", got, ipTargetOpen)
	}
}

// TestParityLockIsLockedAfterStateLocked verifies that OnEvent on the state
// sensor updates IsLocked correctly. Mirrors test_rf_lock → is_locked checks.
func TestParityLockIsLockedAfterStateLocked(t *testing.T) {
	t.Parallel()

	r := newRig(t, "VCU0000146:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	fireLockEnum(t, r.stateDP, string(StateLocked))
	if locked, ok := r.lock.IsLocked(); !locked || !ok {
		t.Fatalf("IsLocked()=%v ok=%v after StateLocked event, want (true, true)", locked, ok)
	}
	fireLockEnum(t, r.stateDP, string(StateUnlocked))
	if locked, _ := r.lock.IsLocked(); locked {
		t.Error("IsLocked() must be false after StateUnlocked event")
	}
}

// TestParityLockIsLockingFromDirection verifies the locking/unlocking
// direction detection. Mirrors test_rf_lock → is_locking / is_unlocking.
func TestParityLockIsLockingFromDirection(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{})

	fireLockEnum(t, r.dirDP, string(DirectionLocking))
	if !r.lock.IsLocking() {
		t.Error("IsLocking() must be true when direction=locking")
	}
	if r.lock.IsUnlocking() {
		t.Error("IsUnlocking() must be false when direction=locking")
	}

	fireLockEnum(t, r.dirDP, string(DirectionUnlock))
	if r.lock.IsLocking() {
		t.Error("IsLocking() must be false when direction=unlocking")
	}
	if !r.lock.IsUnlocking() {
		t.Error("IsUnlocking() must be true when direction=unlocking")
	}

	fireLockEnum(t, r.dirDP, string(DirectionNone))
	if r.lock.IsLocking() || r.lock.IsUnlocking() {
		t.Error("Both IsLocking and IsUnlocking must be false when direction=none")
	}
}

// TestParityLockIsJammed verifies that ERROR_JAMMED events propagate.
// Mirrors test_rf_lock → is_jammed check.
func TestParityLockIsJammed(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{})
	if r.lock.IsJammed() {
		t.Error("IsJammed() must be false before any event")
	}
	r.jammedDP.OnEvent(true)
	if !r.lock.IsJammed() {
		t.Error("IsJammed() must be true after ERROR_JAMMED=true event")
	}
	r.jammedDP.OnEvent(false)
	if r.lock.IsJammed() {
		t.Error("IsJammed() must be false after ERROR_JAMMED=false event")
	}
}

// TestParityLockIsRefreshed verifies IsRefreshed returns false before and
// true after a wire event.
//
// Pins the availability gate to its primary state carrier (LOCK_STATE for
// the IP variant); see docs/parity/by_design.md.
func TestParityLockIsRefreshed(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{})
	if r.lock.IsRefreshed() {
		t.Error("IsRefreshed() must be false before any wire event")
	}
	fireLockEnum(t, r.stateDP, string(StateLocked))
	if !r.lock.IsRefreshed() {
		t.Error("IsRefreshed() must be true after state event")
	}
}

// TestParityLockRFStateFromBoolDP verifies the RF lock's bool-STATE
// inversion: after Lock(), State()=locked; after Unlock(), State()=unlocked.
func TestParityLockRFStateFromBoolDP(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "VCU0000146:1", KindRF, w, custom.LockCapabilities{})

	// RF lock writes STATE=false → the optimistic value → IsLocked()=true.
	if err := r.lock.Lock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterState || got.value != false {
		t.Fatalf("RF Lock() wrote %+v, want STATE=false", got)
	}

	// RF unlock writes STATE=true → IsLocked()=false.
	if err := r.lock.Unlock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterState || got.value != true {
		t.Fatalf("RF Unlock() wrote %+v, want STATE=true", got)
	}
}

// ─── RF error-aware rig ──────────────────────────────────────────────────────

// rfErrorRig holds a KindRF Lock wired with a string ERROR data point so the
// IsJammed kind-aware path can be exercised directly.
type rfErrorRig struct {
	lock    *Lock
	channel *device.Channel
	errorDP *generic.Sensor[int32]
}

func newRFErrorRig(t *testing.T, address string) *rfErrorRig {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "LOCK", hmenum.ParamsetKeyValues)

	// RF locks use bool STATE for locking/unlocking.
	stateDp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: &stubWriter{},
	})
	ch.Put(stateDp)

	// RF locks expose ERROR as a read-only ENUM (index sensor) for jam
	// detection.
	errorDP := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterError),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"NO_ERROR", "CLUTCH_FAILURE", "MOTOR_ABORTED"},
		},
	})
	ch.Put(errorDP)

	l := New(Config{Channel: ch, Writer: &stubWriter{}, Capabilities: custom.LockCapabilities{}, Kind: KindRF})
	return &rfErrorRig{lock: l, channel: ch, errorDP: errorDP}
}

// TestIsJammedRFNoError verifies that IsJammed returns false when the RF lock
// reports NO_ERROR.
func TestIsJammedRFNoError(t *testing.T) {
	t.Parallel()

	r := newRFErrorRig(t, "HmSecKey:1")
	fireLockEnum(t, r.errorDP, string(LockErrorNoError))

	if r.lock.IsJammed() {
		t.Error("IsJammed() must be false when ERROR = NO_ERROR")
	}
}

// TestIsJammedRFClutchFailure verifies that IsJammed returns true when the RF
// lock reports CLUTCH_FAILURE.
func TestIsJammedRFClutchFailure(t *testing.T) {
	t.Parallel()

	r := newRFErrorRig(t, "HmSecKey:2")
	fireLockEnum(t, r.errorDP, string(LockErrorClutchFail))

	if !r.lock.IsJammed() {
		t.Errorf("IsJammed() must be true when ERROR = %s", LockErrorClutchFail)
	}
}

// TestIsJammedRFMotorAborted verifies that IsJammed returns true for
// MOTOR_ABORTED.
func TestIsJammedRFMotorAborted(t *testing.T) {
	t.Parallel()

	r := newRFErrorRig(t, "HmSecKey:3")
	fireLockEnum(t, r.errorDP, string(LockErrorMotorAborted))

	if !r.lock.IsJammed() {
		t.Errorf("IsJammed() must be true when ERROR = %s", LockErrorMotorAborted)
	}
}

// TestIsJammedRFUnobserved verifies that IsJammed returns false when the ERROR
// DP has not been observed yet.
func TestIsJammedRFUnobserved(t *testing.T) {
	t.Parallel()

	r := newRFErrorRig(t, "HmSecKey:4")
	// No errorDP.OnEvent call — unobserved state.

	if r.lock.IsJammed() {
		t.Error("IsJammed() must be false when ERROR not yet observed")
	}
}

// TestIsJammedIPUsesJammedFlag verifies that KindIP still uses the ERROR_JAMMED
// binary flag, not the string ERROR parameter.
func TestIsJammedIPUsesJammedFlag(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{SupportsOpen: true})
	r.jammedDP.OnEvent(true)

	if !r.lock.IsJammed() {
		t.Error("IsJammed() must be true for KindIP when ERROR_JAMMED is true")
	}

	r.jammedDP.OnEvent(false)
	if r.lock.IsJammed() {
		t.Error("IsJammed() must be false for KindIP when ERROR_JAMMED is false")
	}
}

// TestLockBaseDPMethodsExist verifies that Lock embeds custom.BaseDP and
// exposes its observability methods without panicking.
func TestLockBaseDPMethodsExist(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{})

	// Must compile and return zero values before any event.
	_, _ = r.lock.ModifiedAt()
	_, _ = r.lock.RefreshedAt()
	_ = r.lock.UnconfirmedLastValuesSend()

	r.lock.MarkModified()
	r.lock.MarkRefreshed()

	if _, ok := r.lock.ModifiedAt(); !ok {
		t.Error("ModifiedAt() must be non-zero after MarkModified()")
	}
	if _, ok := r.lock.RefreshedAt(); !ok {
		t.Error("RefreshedAt() must be non-zero after MarkRefreshed()")
	}
}
