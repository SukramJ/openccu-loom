// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package lock

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type stubWriter struct {
	mu    sync.Mutex
	err   error
	calls []call
}

type call struct {
	param hmenum.Parameter
	value any
}

func (s *stubWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	if s.err != nil {
		return s.err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{p, v})
	return nil
}

func (s *stubWriter) last() call {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[len(s.calls)-1]
}

type rig struct {
	lock     *Lock
	channel  *device.Channel
	stateDP  *generic.Sensor[int32]
	dirDP    *generic.Sensor[int32]
	jammedDP *generic.BinarySensor
}

// fireLockEnum resolves label against dp's own VALUE_LIST and fires the
// resulting raw index as a wire event — mirrors how the resolver projects a
// read-only ENUM parameter (LOCK_STATE, DIRECTION, ERROR) onto an
// index-valued Sensor[int32].
func fireLockEnum(t *testing.T, dp *generic.Sensor[int32], label string) {
	t.Helper()
	idx, ok := custom.EnumLabelIndex(dp, label)
	if !ok {
		t.Fatalf("label %q not in VALUE_LIST", label)
	}
	dp.OnEvent(idx)
}

func newRig(t *testing.T, address string, kind Kind, w Writer, caps custom.LockCapabilities) *rig {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "LOCK", hmenum.ParamsetKeyValues)

	stateDP := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLockState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"UNKNOWN", "LOCKED", "UNLOCKED"},
		},
	})
	ch.Put(stateDP)

	dirDP := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterDirection),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"", "UP", "DOWN"},
		},
	})
	ch.Put(dirDP)

	jammedDP := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterErrorJammed),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(jammedDP)

	l := New(Config{Channel: ch, Writer: w, Capabilities: caps, Kind: kind})
	return &rig{
		lock:     l,
		channel:  ch,
		stateDP:  stateDP,
		dirDP:    dirDP,
		jammedDP: jammedDP,
	}
}

func TestLockIPSendsTargetLevel(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{SupportsOpen: true})

	if err := r.lock.Lock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.param != hmenum.ParameterLockTargetLevel || got.value.(string) != ipTargetLocked {
		t.Fatalf("lock wrote %+v", got)
	}
	if err := r.lock.Unlock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.value.(string) != ipTargetUnlocked {
		t.Fatalf("unlock wrote %+v", got)
	}
	if err := r.lock.Open(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if got := w.last(); got.value.(string) != ipTargetOpen {
		t.Fatalf("open wrote %+v", got)
	}
}

func TestLockRFUsesState(t *testing.T) {
	w := &stubWriter{}
	r := newRig(t, "x", KindRF, w, custom.LockCapabilities{})
	_ = r.lock.Lock(context.Background(), hmenum.CommandPriorityHigh)
	if got := w.last(); got.param != hmenum.ParameterState || got.value != false {
		t.Fatalf("RF lock wrote %+v", got)
	}
	_ = r.lock.Unlock(context.Background(), hmenum.CommandPriorityHigh)
	if got := w.last(); got.value != true {
		t.Fatalf("RF unlock wrote %+v", got)
	}
}

func TestLockOpenCapabilityGate(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{})
	if err := r.lock.Open(context.Background(), hmenum.CommandPriorityHigh); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("got %v, want ErrNotSupported", err)
	}
}

func TestLockIngestion(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{})

	// Drive the channel-side data points (production path) and
	// verify Lock observes them through its shared pointers.
	fireLockEnum(t, r.stateDP, string(StateLocked))
	locked, observed := r.lock.IsLocked()
	if !locked || !observed {
		t.Fatalf("locked=%v observed=%v", locked, observed)
	}
	fireLockEnum(t, r.dirDP, string(DirectionLocking))
	if d, ok := r.lock.Direction(); !ok || d != DirectionLocking {
		t.Fatalf("direction=%v ok=%v", d, ok)
	}
	r.jammedDP.OnEvent(true)
	if !r.lock.IsJammed() {
		t.Fatal("jammed flag lost")
	}
}

func TestLockSharesStateInstanceWithChannel(t *testing.T) {
	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{})
	chDP := r.channel.Parameter(hmenum.ParameterLockState)
	if chDP == nil {
		t.Fatal("channel must expose LOCK_STATE")
	}
	if any(r.lock.stateDp) != any(chDP) || any(r.lock.stateDp) != any(r.stateDP) {
		t.Fatalf("Lock.stateDp must be the same instance as channel parameter")
	}
}

// TestLockSubscribeReturnsCleanupClosure pins the L-A2-01 fix: Lock
// implements [device.SubscribingDataPoint] so Channel.SetCustomDataPoint
// invokes its Subscribe and the Bridge's publishCustomDPState is
// guaranteed to fire on every wire-DP change. Subscribe must return a
// non-nil unsubscribe closure that releases every registered handler.
func TestLockSubscribeReturnsCleanupClosure(t *testing.T) {
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})

	unsub := r.lock.Subscribe(r.channel)
	if unsub == nil {
		t.Fatal("Subscribe must return a non-nil cleanup closure")
	}
	// Releasing the subscription must not panic and must allow a
	// subsequent re-subscribe (idempotent unsubscribe contract).
	unsub()
	unsub2 := r.lock.Subscribe(r.channel)
	if unsub2 == nil {
		t.Fatal("re-Subscribe must return a non-nil cleanup closure")
	}
	unsub2()
}

// TestLockSubscribeNilChannel pins the defensive nil-guard.
func TestLockSubscribeNilChannel(t *testing.T) {
	l := New(Config{Channel: nil, Writer: &stubWriter{}})
	unsub := l.Subscribe(nil)
	if unsub == nil {
		t.Fatal("Subscribe(nil) must return a non-nil no-op closure")
	}
	unsub() // must be a no-op
}

// TestLockButtonKindWritesBUTTONLOCK pins that KindButton Set() writes
// BUTTON_LOCK, not STATE. HmIP-DLD ch0 has no STATE wire parameter and
// would fault with XML-RPC "parameter not found" if STATE is written.
func TestLockButtonKindWritesBUTTONLOCK(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DLD0001"})
	ch := d.AddChannel("HmIP-DLD:0", 0, "LOCK", hmenum.ParamsetKeyValues)
	btnDP := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "HmIP-DLD:0",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterButtonLock),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(btnDP)

	l := New(Config{Channel: ch, Writer: w, Kind: KindButton})

	// Button-lock semantics: lock() -> turn_on (true, keys disabled),
	// unlock() -> turn_off (false).
	if err := l.Lock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterButtonLock {
		t.Errorf("KindButton Lock() wrote param=%v, want BUTTON_LOCK", got.param)
	}
	if got.value != true {
		t.Errorf("KindButton Lock() value=%v, want true (locked)", got.value)
	}
	if st, ok := l.LockState(); !ok || st != StateLocked {
		t.Errorf("after Lock(): state=%v ok=%v, want LOCKED", st, ok)
	}

	if err := l.Unlock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got = w.last()
	if got.param != hmenum.ParameterButtonLock {
		t.Errorf("KindButton Unlock() wrote param=%v, want BUTTON_LOCK", got.param)
	}
	if got.value != false {
		t.Errorf("KindButton Unlock() value=%v, want false (unlocked)", got.value)
	}
	if st, ok := l.LockState(); !ok || st != StateUnlocked {
		t.Errorf("after Unlock(): state=%v ok=%v, want UNLOCKED", st, ok)
	}
}

// TestLockDataVersionBumpsOnWireUpdate pins that physical operation at the CCU
// — which fires OnConfirmedUpdate on the wire DPs — increments DataVersion.
// Without this, Matter SubscribeUpdate filters stale state and HomeKit shows
// the wrong lock state.
func TestLockDataVersionBumpsOnWireUpdate(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	before := r.lock.MatterDataVersion()

	// First CCU-confirmed value fires OnConfirmedUpdate (first observation).
	fireLockEnum(t, r.stateDP, string(StateLocked))

	after := r.lock.MatterDataVersion()
	if after <= before {
		t.Fatalf("DataVersion did not bump after wire LOCK_STATE update: before=%d after=%d", before, after)
	}

	// A value change also fires confirmed callbacks.
	mid := after
	fireLockEnum(t, r.stateDP, string(StateUnlocked))
	afterChange := r.lock.MatterDataVersion()
	if afterChange <= mid {
		t.Fatalf("DataVersion did not bump after LOCK_STATE value change: before=%d after=%d", mid, afterChange)
	}
}

// TestLockDataVersionBumpsOnJammed pins that an ERROR_JAMMED wire update also
// bumps DataVersion so the jamming state propagates to Matter subscribers.
func TestLockDataVersionBumpsOnJammed(t *testing.T) {
	t.Parallel()
	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	before := r.lock.MatterDataVersion()

	// First observation fires OnConfirmedUpdate.
	r.jammedDP.OnEvent(true)

	after := r.lock.MatterDataVersion()
	if after <= before {
		t.Fatalf("DataVersion did not bump after ERROR_JAMMED update: before=%d after=%d", before, after)
	}
}

// TestLockAddressReturnsChannelAddress verifies that Address is set from
// the channel address supplied at construction time.
func TestLockAddressReturnsChannelAddress(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-DLD:1"
	r := newRig(t, addr, KindIP, &stubWriter{}, custom.LockCapabilities{})
	if r.lock.Address != addr {
		t.Errorf("Address = %q, want %q", r.lock.Address, addr)
	}
}

// TestLockIPLockSendsTargetLevelLocked verifies that Lock() on a KindIP
// lock sends LOCK_TARGET_LEVEL = ipTargetLocked (0).
func TestLockIPLockSendsTargetLevelLocked(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{})
	if err := r.lock.Lock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterLockTargetLevel {
		t.Errorf("Lock() wrote param %q, want %q", got.param, hmenum.ParameterLockTargetLevel)
	}
	if got.value.(string) != ipTargetLocked {
		t.Errorf("Lock() wrote value %v, want %q", got.value, ipTargetLocked)
	}
}

// TestLockIPUnlockSendsTargetLevelUnlocked verifies that Unlock() on a
// KindIP lock sends LOCK_TARGET_LEVEL = ipTargetUnlocked.
func TestLockIPUnlockSendsTargetLevelUnlocked(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{})
	if err := r.lock.Unlock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.value.(string) != ipTargetUnlocked {
		t.Errorf("Unlock() wrote value %v, want %q", got.value, ipTargetUnlocked)
	}
}

// TestLockRFLockSendsStateFalse verifies that Lock() on a KindRF lock
// writes STATE = false (RF semantics: STATE=true = unlocked).
func TestLockRFLockSendsStateFalse(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HM-Sec-Key:1", KindRF, w, custom.LockCapabilities{})
	if err := r.lock.Lock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterState {
		t.Errorf("RF Lock() wrote param %q, want STATE", got.param)
	}
	if got.value != false {
		t.Errorf("RF Lock() wrote value %v, want false", got.value)
	}
}

// TestLockRFUnlockSendsStateTrue verifies that Unlock() on a KindRF
// lock writes STATE = true.
func TestLockRFUnlockSendsStateTrue(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HM-Sec-Key:1", KindRF, w, custom.LockCapabilities{})
	if err := r.lock.Unlock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterState {
		t.Errorf("RF Unlock() wrote param %q, want STATE", got.param)
	}
	if got.value != true {
		t.Errorf("RF Unlock() wrote value %v, want true", got.value)
	}
}

// TestLockOpenForwardsOpenParameter verifies that Open() on a KindRF lock
// with SupportsOpen capability writes OPEN=true.
func TestLockOpenForwardsOpenParameter(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HM-Sec-Key:1", KindRF, w, custom.LockCapabilities{SupportsOpen: true})
	if err := r.lock.Open(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	got := w.last()
	if got.param != hmenum.ParameterOpen {
		t.Errorf("Open() wrote param %q, want OPEN", got.param)
	}
	if got.value != true {
		t.Errorf("Open() wrote value %v, want true", got.value)
	}
}

// TestLockOpenCapabilityRequiredForRFAndIP verifies that Open() returns
// ErrNotSupported when the lock is not configured with SupportsOpen.
func TestLockOpenCapabilityRequiredForRFAndIP(t *testing.T) {
	t.Parallel()

	for _, kind := range []Kind{KindIP, KindRF, KindButton} {
		r := newRig(t, "x", kind, &stubWriter{}, custom.LockCapabilities{SupportsOpen: false})
		err := r.lock.Open(context.Background(), hmenum.CommandPriorityHigh)
		if !errors.Is(err, ErrNotSupported) {
			t.Errorf("kind=%d: Open without SupportsOpen got %v, want ErrNotSupported", kind, err)
		}
	}
}

// TestLockGetCurrentStateReflectsDP verifies that Lock.State() reflects
// events pushed onto the underlying LOCK_STATE DP.
func TestLockGetCurrentStateReflectsDP(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})

	// Not yet observed.
	_, ok := r.lock.LockState()
	if ok {
		t.Error("State() should not be observed before any DP event")
	}

	fireLockEnum(t, r.stateDP, string(StateLocked))
	st, ok := r.lock.LockState()
	if !ok {
		t.Fatal("State() must be observed after event")
	}
	if st != StateLocked {
		t.Errorf("State() = %q, want %q", st, StateLocked)
	}

	fireLockEnum(t, r.stateDP, string(StateUnlocked))
	st, ok = r.lock.LockState()
	if !ok || st != StateUnlocked {
		t.Errorf("State() after unlock event = %q ok=%v, want unlocked", st, ok)
	}
}

// TestLockObservesCommandOptimistically verifies that after Lock() the
// wrapper optimistically reflects the locked state via IsLocked.
func TestLockObservesCommandOptimistically(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{})

	if err := r.lock.Lock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	locked, ok := r.lock.IsLocked()
	if !ok {
		t.Fatal("IsLocked should be observed after Lock() command")
	}
	if !locked {
		t.Error("IsLocked should return true immediately after Lock() command")
	}
}

// TestLockErrorStateReflectsJammedDP verifies that IsJammed reflects the
// ERROR_JAMMED binary sensor.
func TestLockErrorStateReflectsJammedDP(t *testing.T) {
	t.Parallel()

	r := newRig(t, "HmIP-DLD:1", KindIP, &stubWriter{}, custom.LockCapabilities{})
	if r.lock.IsJammed() {
		t.Error("IsJammed must be false initially")
	}
	r.jammedDP.OnEvent(true)
	if !r.lock.IsJammed() {
		t.Error("IsJammed must be true after ERROR_JAMMED event")
	}
	r.jammedDP.OnEvent(false)
	if r.lock.IsJammed() {
		t.Error("IsJammed must be false after ERROR_JAMMED cleared")
	}
}

// TestLockNilChannelGracefullyDegrades verifies that a Lock constructed
// with a nil channel does not panic and returns safe zero values.
// State/direction/jammed accessors all degrade gracefully. Commands with
// a non-nil writer still succeed (nil channel only means no DP pointers,
// not a broken writer path).
func TestLockNilChannelGracefullyDegrades(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	l := New(Config{Channel: nil, Writer: w, Kind: KindIP})
	if l.Address != "" {
		t.Errorf("nil-channel lock address = %q, want empty", l.Address)
	}
	_, ok := l.LockState()
	if ok {
		t.Error("LockState() should not be observed when channel is nil")
	}
	if l.IsJammed() {
		t.Error("IsJammed should be false when channel is nil")
	}
	// Command with a valid writer should succeed without panic even when
	// the internal DP pointers are nil.
	if err := l.Lock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Errorf("Lock() with nil channel but valid writer must not error: %v", err)
	}
	// Verify the writer was actually called.
	if len(w.calls) == 0 {
		t.Error("Lock() must have forwarded the command to the writer")
	}
}

// TestLockButtonKindPostfix verifies the NamePostfix for KindButton.
func TestLockButtonKindPostfix(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindButton, &stubWriter{}, custom.LockCapabilities{})
	if got := r.lock.NamePostfix(); got != "BUTTON_LOCK" {
		t.Errorf("NamePostfix() = %q, want %q", got, "BUTTON_LOCK")
	}
}

// TestLockIPKindPostfix verifies the NamePostfix for KindIP.
func TestLockIPKindPostfix(t *testing.T) {
	t.Parallel()

	r := newRig(t, "x", KindIP, &stubWriter{}, custom.LockCapabilities{})
	if got := r.lock.NamePostfix(); got != "" {
		t.Errorf("NamePostfix() for KindIP = %q, want empty", got)
	}
}

// paramsetStubWriter extends stubWriter with PutParamset recording so
// tests can assert MASTER-paramset routing.
type paramsetStubWriter struct {
	stubWriter
	mu2  sync.Mutex
	puts []putCall
}

type putCall struct {
	channel string
	key     hmenum.ParamsetKey
	values  map[string]any
}

func (s *paramsetStubWriter) PutParamset(_ context.Context, ch string, key hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	s.mu2.Lock()
	defer s.mu2.Unlock()
	s.puts = append(s.puts, putCall{ch, key, values})
	return nil
}

// TestLockButtonGlobalButtonLockFromMaster pins the shipping button-lock
// wiring: the wire parameter is GLOBAL_BUTTON_LOCK in the MASTER paramset
// (HmIP thermostats ch0). The state must read off the seeded MASTER value
// (true = LOCKED) and writes must route through
// put_paramset — setValue on a MASTER parameter faults with XML-RPC -5.
func TestLockButtonGlobalButtonLockFromMaster(t *testing.T) {
	t.Parallel()
	w := &paramsetStubWriter{}

	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "BWTH0001"})
	ch := d.AddChannel("BWTH0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	gblDP := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "BWTH0001:0",
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(hmenum.ParameterGlobalButtonLock),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.PutMaster(gblDP)

	l := New(Config{Channel: ch, Writer: w, Kind: KindButton})

	// Pre-seed: no value observed yet → unknown.
	if _, ok := l.LockState(); ok {
		t.Error("LockState() ok before any MASTER seed, want unknown")
	}

	// seedMasterValues delivers GLOBAL_BUTTON_LOCK=true → LOCKED.
	gblDP.OnWireValue(true)
	if st, ok := l.LockState(); !ok || st != StateLocked {
		t.Errorf("after MASTER seed true: state=%v ok=%v, want LOCKED", st, ok)
	}

	// Unlock writes GLOBAL_BUTTON_LOCK=false via put_paramset MASTER.
	if err := l.Unlock(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("Unlock(): %d PutParamset calls, want 1 (MASTER routing)", len(w.puts))
	}
	put := w.puts[0]
	if put.key != hmenum.ParamsetKeyMaster {
		t.Errorf("Unlock() paramset=%v, want MASTER", put.key)
	}
	if v, okv := put.values[string(hmenum.ParameterGlobalButtonLock)]; !okv || v != false {
		t.Errorf("Unlock() values=%v, want GLOBAL_BUTTON_LOCK=false", put.values)
	}
	if st, ok := l.LockState(); !ok || st != StateUnlocked {
		t.Errorf("after Unlock(): state=%v ok=%v, want UNLOCKED (optimistic echo)", st, ok)
	}

	// Open is not supported on button locks.
	if err := l.Open(context.Background(), hmenum.CommandPriorityHigh); err == nil {
		t.Error("Open() on a button lock must fail")
	}
}
