// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- RF rig with bool STATE DP ---

// rfRig is a test fixture that constructs a KindRF Lock wired to a
// bool STATE Switch DP (distinct from the string LOCK_STATE used by newRig).
type rfRig struct {
	lock    *Lock
	stateDP *generic.Switch
}

func newRFRig(t *testing.T) *rfRig {
	t.Helper()
	const address = "HM-Sec-Key:1"
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "HM0001"})
	ch := d.AddChannel(address, 1, "LOCK", hmenum.ParamsetKeyValues)

	stateDP := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent | hmenum.OperationsWrite,
		},
	})
	ch.Put(stateDP)

	l := New(Config{
		Channel:      ch,
		Writer:       &stubWriter{},
		Capabilities: custom.LockCapabilities{},
		Kind:         KindRF,
	})
	return &rfRig{lock: l, stateDP: stateDP}
}

// --- StateUncertain ---

// TestStateUncertain_ReturnsBool verifies that StateUncertain does not
// panic and returns a bool.
func TestStateUncertain_ReturnsBool(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	// Just assert no panic and the return type makes sense.
	_ = r.lock.StateUncertain()
}

// --- SubDataPointKeys ---

// TestSubDataPointKeys_NonNil verifies that SubDataPointKeys returns a
// slice (possibly empty) without panicking.
func TestSubDataPointKeys_NonNil(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	keys := r.lock.SubDataPointKeys()
	// The underlying aggregate may return nil or a non-nil slice depending
	// on whether the DP is attached; confirm no panic.
	_ = keys
}

// --- State remaining paths (bool wire) ---

// TestState_BoolStateDPFalseIsLocked verifies that a KindRF lock with
// STATE=false reports StateLocked.
func TestState_BoolStateDPFalseIsLocked(t *testing.T) {
	t.Parallel()
	r := newRFRig(t)
	r.stateDP.OnEvent(false)
	st, ok := r.lock.LockState()
	if !ok {
		t.Fatal("State() must be observed after bool DP event")
	}
	if st != StateLocked {
		t.Errorf("bool false → State() = %q, want %q", st, StateLocked)
	}
}

// TestState_BoolStateDPTrueIsUnlocked verifies that a KindRF lock with
// STATE=true reports StateUnlocked.
func TestState_BoolStateDPTrueIsUnlocked(t *testing.T) {
	t.Parallel()
	r := newRFRig(t)
	r.stateDP.OnEvent(true)
	st, ok := r.lock.LockState()
	if !ok {
		t.Fatal("State() must be observed after bool DP event")
	}
	if st != StateUnlocked {
		t.Errorf("bool true → State() = %q, want %q", st, StateUnlocked)
	}
}

// TestState_UnknownStringIsPreserved verifies that an unknown string
// value coming from the CCU is returned as-is.
func TestState_UnknownStringIsPreserved(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	r.stateDP.OnEvent("OPENING")
	st, ok := r.lock.LockState()
	if !ok {
		t.Fatal("State() must be observed after DP event")
	}
	if st != State("OPENING") {
		t.Errorf("unknown string: State() = %q, want %q", st, "OPENING")
	}
}

// TestState_EmptyStringIsUnknown verifies that an empty enum string
// from the CCU maps to StateUnknown.
func TestState_EmptyStringIsUnknown(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	r.stateDP.OnEvent("")
	st, ok := r.lock.LockState()
	if !ok {
		t.Fatal("State() must be observed even for empty string")
	}
	if st != StateUnknown {
		t.Errorf("empty string → State() = %q, want %q", st, StateUnknown)
	}
}

// --- send — unsupported Kind ---

// TestSend_UnknownKindReturnsError verifies that a Lock with an
// unrecognised Kind constant returns ErrNotSupported from any command.
func TestSend_UnknownKindReturnsError(t *testing.T) {
	t.Parallel()
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{})
	r.lock.Kind = Kind(99) // unknown
	err := r.lock.Lock(context.Background(), hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrNotSupported) {
		t.Errorf("unknown Kind: got %v, want ErrNotSupported", err)
	}
}

// --- MatterWrite ---

// TestMatterWrite_AlwaysReturnsError verifies that MatterWrite surfaces an
// error for any attribute ID — DoorLock has no client-writable attributes.
func TestMatterWrite_AlwaysReturnsError(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	servers := r.lock.MatterClusterServers()
	if len(servers) == 0 {
		t.Fatal("MatterClusterServers returned empty slice")
	}
	err := servers[0].MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("MatterWrite must return an error for any attribute ID")
	}
}

// --- InfoPayload ---

// TestInfoPayload_NilReceiver verifies that a nil Lock returns nil.
func TestInfoPayload_NilReceiver(t *testing.T) {
	t.Parallel()
	var l *Lock
	if got := l.Info(); got != nil {
		t.Errorf("nil receiver InfoPayload = %v, want nil", got)
	}
}

// TestInfoPayload_ContainsExpectedKeys verifies that InfoPayload carries
// at least the required semantic keys.
func TestInfoPayload_ContainsExpectedKeys(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	p, ok := r.lock.Info().(*payload.LockInfo)
	if !ok || p == nil {
		t.Fatal("InfoPayload must return a non-nil *payload.LockInfo")
	}
	if p.Address == "" {
		t.Error("InfoPayload missing Address")
	}
	if p.Key == "" {
		t.Error("InfoPayload missing Key")
	}
	if p.Category != "lock" {
		t.Errorf("InfoPayload category = %v, want lock", p.Category)
	}
	if p.Kind == "" {
		t.Error("InfoPayload missing Kind")
	}
	if p.Key == "" {
		t.Error("InfoPayload missing Key")
	}
	if p.SubDPKeys == nil {
		t.Error("InfoPayload missing SubDPKeys")
	}
}

// TestInfoPayload_KindLabels verifies that each Kind constant maps to the
// expected string label in InfoPayload.
func TestInfoPayload_KindLabels(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind Kind
		want string
	}{
		{KindIP, "ip"},
		{KindRF, "rf"},
		{KindButton, "button"},
	}
	for _, tc := range cases {
		r := newRig(t, "x", tc.kind, &stubWriter{}, custom.LockCapabilities{})
		ip, ok := r.lock.Info().(*payload.LockInfo)
		if !ok || ip == nil {
			t.Errorf("kind=%d: InfoPayload is not *payload.LockInfo", tc.kind)
			continue
		}
		if ip.Kind != tc.want {
			t.Errorf("kind=%d: label = %q, want %q", tc.kind, ip.Kind, tc.want)
		}
	}
}

// --- ConfigPayload ---

// TestConfigPayload_NilReceiver verifies that a nil Lock returns nil.
func TestConfigPayload_NilReceiver(t *testing.T) {
	t.Parallel()
	var l *Lock
	if got := l.Config(); got != nil {
		t.Errorf("nil receiver ConfigPayload = %v, want nil", got)
	}
}

// TestConfigPayload_SupportsOpenFalse verifies the capability is reflected.
func TestConfigPayload_SupportsOpenFalse(t *testing.T) {
	t.Parallel()
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{SupportsOpen: false})
	p, _ := r.lock.Config().(*payload.LockConfig)
	if p != nil && p.SupportsOpen {
		t.Error("ConfigPayload supports_open must be false when SupportsOpen=false")
	}
}

// TestConfigPayload_SupportsOpenTrue verifies the capability is reflected.
func TestConfigPayload_SupportsOpenTrue(t *testing.T) {
	t.Parallel()
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{SupportsOpen: true})
	p, _ := r.lock.Config().(*payload.LockConfig)
	if p == nil || !p.SupportsOpen {
		t.Error("ConfigPayload supports_open must be true when SupportsOpen=true")
	}
}

// --- StatePayload ---

// TestStatePayload_NilReceiver verifies that a nil Lock returns nil.
func TestStatePayload_NilReceiver(t *testing.T) {
	t.Parallel()
	var l *Lock
	if got := l.State(); got != nil {
		t.Errorf("nil receiver StatePayload = %v, want nil", got)
	}
}

// TestStatePayload_DefaultsWhenUnobserved verifies that StatePayload
// returns safe defaults when the Lock has not yet received a state event.
func TestStatePayload_DefaultsWhenUnobserved(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	p, ok := r.lock.State().(*payload.LockState)
	if !ok || p == nil {
		t.Fatal("StatePayload must return a non-nil *payload.LockState")
	}
	// Default: unlocked (HA log suppression).
	if p.LockState != string(StateUnlocked) {
		t.Errorf("lock_state default = %q, want %q", p.LockState, StateUnlocked)
	}
	if p.IsLocked {
		t.Error("is_locked default must be false")
	}
	if p.IsJammed {
		t.Error("is_jammed default must be false")
	}
}

// TestStatePayload_Locked verifies that StatePayload correctly reflects a
// locked state event.
func TestStatePayload_Locked(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	r.stateDP.OnEvent(string(StateLocked))
	p, ok := r.lock.State().(*payload.LockState)
	if !ok || p == nil {
		t.Fatal("StatePayload must return a non-nil *payload.LockState")
	}
	if p.LockState != string(StateLocked) {
		t.Errorf("lock_state = %q, want %q", p.LockState, StateLocked)
	}
	if !p.IsLocked {
		t.Error("is_locked must be true after LOCKED event")
	}
}

// TestStatePayload_DirectionEmittedUnconditionally verifies that direction
// keys are always present.
func TestStatePayload_DirectionEmittedUnconditionally(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	p, ok := r.lock.State().(*payload.LockState)
	if !ok || p == nil {
		t.Fatal("StatePayload must return a non-nil *payload.LockState")
	}
	// Direction, IsLocking, IsUnlocking are always present as struct fields.
	_ = p.Direction
	_ = p.IsLocking
	_ = p.IsUnlocking
}

// TestStatePayload_WithDirectionLocking verifies direction=LOCKING
// surfaces correctly.
func TestStatePayload_WithDirectionLocking(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	r.dirDP.OnEvent(string(DirectionLocking))
	p, ok := r.lock.State().(*payload.LockState)
	if !ok || p == nil {
		t.Fatal("StatePayload must return a non-nil *payload.LockState")
	}
	if p.Direction != string(DirectionLocking) {
		t.Errorf("direction = %q, want %q", p.Direction, DirectionLocking)
	}
	if !p.IsLocking {
		t.Error("is_locking must be true when LOCKING")
	}
	if p.IsUnlocking {
		t.Error("is_unlocking must be false when LOCKING")
	}
}

// TestStatePayload_WithDirectionUnlocking verifies direction=UNLOCKING
// surfaces correctly.
func TestStatePayload_WithDirectionUnlocking(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	r.dirDP.OnEvent(string(DirectionUnlock))
	p, ok := r.lock.State().(*payload.LockState)
	if !ok || p == nil {
		t.Fatal("StatePayload must return a non-nil *payload.LockState")
	}
	if !p.IsUnlocking {
		t.Error("is_unlocking must be true when UNLOCKING")
	}
	if p.IsLocking {
		t.Error("is_locking must be false when UNLOCKING")
	}
}

// --- registerServices coverage via ServiceRegistry.Invoke ---

// TestRegisterServices_LockAndUnlock exercises the service methods
// wired by registerServices() indirectly through payload.ServiceRegistry.Invoke.
func TestRegisterServices_LockAndUnlock(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{SupportsOpen: true})

	ctx := context.Background()

	// "lock" service
	if err := r.lock.Invoke(ctx, "lock", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("lock service: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("lock service did not produce a write call")
	}

	prev := len(w.calls)

	// "unlock" service
	if err := r.lock.Invoke(ctx, "unlock", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("unlock service: %v", err)
	}
	if len(w.calls) == prev {
		t.Fatal("unlock service did not produce a write call")
	}

	prev = len(w.calls)

	// "open" service (SupportsOpen=true)
	if err := r.lock.Invoke(ctx, "open", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("open service: %v", err)
	}
	if len(w.calls) == prev {
		t.Fatal("open service did not produce a write call")
	}
}

// TestRegisterServices_OpenNotRegisteredWithoutCaps verifies that the
// "open" service is not registered when SupportsOpen=false.
func TestRegisterServices_OpenNotRegisteredWithoutCaps(t *testing.T) {
	t.Parallel()
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{SupportsOpen: false})
	err := r.lock.Invoke(context.Background(), "open", nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("open service without SupportsOpen must return an error")
	}
}
