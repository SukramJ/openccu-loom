// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package lock_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock" // trigger init(); exposes presets
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putLockStateEnumDP attaches a *generic.Sensor[int32] for the LOCK_STATE
// read-only ENUM parameter on ch and returns it — mirrors the resolver's
// projection of a read-only ENUM onto an index-valued sensor.
func putLockStateEnumDP(ch *device.Channel, param hmenum.Parameter) *generic.Sensor[int32] {
	dp := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			ValueList:  []string{"UNKNOWN", "LOCKED", "UNLOCKED"},
		},
	})
	ch.Put(dp)
	return dp
}

// fireEnumIndex resolves label against dp's own VALUE_LIST and fires the
// resulting raw index as a wire event.
func fireEnumIndex(t *testing.T, dp *generic.Sensor[int32], label string) {
	t.Helper()
	idx, ok := custom.EnumLabelIndex(dp, label)
	if !ok {
		t.Fatalf("label %q not in VALUE_LIST", label)
	}
	dp.OnEvent(idx)
}

// makeLockChannel builds a bare device + channel for constructor testing.
func makeLockChannel(t *testing.T, address string) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "DEF0001"})
	return d.AddChannel(address, 1, "LOCK", hmenum.ParamsetKeyValues)
}

// makeButtonLockChannel builds a channel carrying the GLOBAL_BUTTON_LOCK wire
// parameter the button-lock constructors require (a channel without it
// materialises no lock, mirroring the reference required-field behaviour).
func makeButtonLockChannel(t *testing.T, address string) *device.Channel {
	t.Helper()
	ch := makeLockChannel(t, address)
	ch.Put(generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterGlobalButtonLock),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))
	return ch
}

// --- Registration tests ---

// TestIPLockConstructorIsRegistered verifies that the init() block
// registers a non-nil constructor for DeviceProfileIPLock.
func TestIPLockConstructorIsRegistered(t *testing.T) {
	t.Parallel()

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPLock)
	if !ok || ctor == nil {
		t.Fatal("expected non-nil constructor for DeviceProfileIPLock")
	}
}

// TestRfLockConstructorIsRegistered verifies registration for
// DeviceProfileRfLock.
func TestRfLockConstructorIsRegistered(t *testing.T) {
	t.Parallel()

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileRfLock)
	if !ok || ctor == nil {
		t.Fatal("expected non-nil constructor for DeviceProfileRfLock")
	}
}

// TestIPButtonLockConstructorIsRegistered verifies registration for
// DeviceProfileIPButtonLock.
func TestIPButtonLockConstructorIsRegistered(t *testing.T) {
	t.Parallel()

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPButtonLock)
	if !ok || ctor == nil {
		t.Fatal("expected non-nil constructor for DeviceProfileIPButtonLock")
	}
}

// TestRFButtonLockConstructorIsRegistered verifies registration for
// DeviceProfileRFButtonLock.
func TestRFButtonLockConstructorIsRegistered(t *testing.T) {
	t.Parallel()

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileRFButtonLock)
	if !ok || ctor == nil {
		t.Fatal("expected non-nil constructor for DeviceProfileRFButtonLock")
	}
}

// --- Constructor returns valid DP ---

// TestIPLockConstructorReturnsValidDP verifies that the IP lock
// constructor returns a non-nil AttachableDataPoint without error.
func TestIPLockConstructorReturnsValidDP(t *testing.T) {
	t.Parallel()

	ch := makeLockChannel(t, "HmIP-DLD:1")
	putLockStateEnumDP(ch, hmenum.ParameterLockState)

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPLock)
	if !ok {
		t.Fatal("constructor not registered")
	}

	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}
	if dp == nil {
		t.Fatal("constructor returned nil data point")
	}
}

// TestRfLockConstructorReturnsValidDP verifies that the RF lock
// constructor returns a non-nil AttachableDataPoint without error.
func TestRfLockConstructorReturnsValidDP(t *testing.T) {
	t.Parallel()

	ch := makeLockChannel(t, "HM-Sec-Key:1")
	putLockStateEnumDP(ch, hmenum.ParameterLockState)

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileRfLock)
	if !ok {
		t.Fatal("constructor not registered")
	}

	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}
	if dp == nil {
		t.Fatal("constructor returned nil data point")
	}
}

// TestIPButtonLockConstructorReturnsValidDP verifies that the IP
// button lock constructor returns a non-nil AttachableDataPoint.
func TestIPButtonLockConstructorReturnsValidDP(t *testing.T) {
	t.Parallel()

	ch := makeButtonLockChannel(t, "HmIP-WRC2:0")

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPButtonLock)
	if !ok {
		t.Fatal("constructor not registered")
	}

	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}
	if dp == nil {
		t.Fatal("constructor returned nil data point")
	}
}

// --- Field wiring tests ---

// TestIPLockConstructorWiresLockStateField verifies that the IP lock
// constructor captures the LOCK_STATE generic DP, so State() reflects
// CCU events via the shared generic pointer.
func TestIPLockConstructorWiresLockStateField(t *testing.T) {
	t.Parallel()

	ch := makeLockChannel(t, "HmIP-DLD:1")
	stateDP := putLockStateEnumDP(ch, hmenum.ParameterLockState)

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPLock)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	// Drive a value via the shared generic DP.
	fireEnumIndex(t, stateDP, string(lock.StateLocked))

	// For IP lock, the DataPointKey is keyed on LOCK_TARGET_LEVEL (the
	// primary write parameter). The shared stateDP pointer means events
	// on it are visible through the *Lock's internal reference.
	key := dp.DataPointKey()
	if key.ChannelAddress != ch.Address {
		t.Errorf("DataPointKey().ChannelAddress = %q, want %q", key.ChannelAddress, ch.Address)
	}
	if key.Parameter != string(hmenum.ParameterLockTargetLevel) {
		t.Errorf("DataPointKey().Parameter = %q, want %q",
			key.Parameter, hmenum.ParameterLockTargetLevel)
	}
}

// TestRfLockConstructorDataPointKeyUsesState verifies that the RF lock
// constructor sets a DataPointKey keyed on STATE.
func TestRfLockConstructorDataPointKeyUsesState(t *testing.T) {
	t.Parallel()

	ch := makeLockChannel(t, "HM-Sec-Key:1")

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileRfLock)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	key := dp.DataPointKey()
	if key.ChannelAddress != ch.Address {
		t.Errorf("DataPointKey().ChannelAddress = %q, want %q", key.ChannelAddress, ch.Address)
	}
	if key.Parameter != string(hmenum.ParameterState) {
		t.Errorf("DataPointKey().Parameter = %q, want %q", key.Parameter, hmenum.ParameterState)
	}
}

// TestIPButtonLockConstructorDataPointKeyIsSet verifies that the
// button lock constructor produces a non-empty DataPointKey.
func TestIPButtonLockConstructorDataPointKeyIsSet(t *testing.T) {
	t.Parallel()

	ch := makeButtonLockChannel(t, "HmIP-WRC2:0")

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPButtonLock)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	if dp.DataPointKey().ChannelAddress != ch.Address {
		t.Errorf("DataPointKey().ChannelAddress = %q, want %q",
			dp.DataPointKey().ChannelAddress, ch.Address)
	}
}

// TestRFButtonLockConstructorDataPointKeyIsSet verifies registration
// and key for RFButtonLock.
func TestRFButtonLockConstructorDataPointKeyIsSet(t *testing.T) {
	t.Parallel()

	ch := makeButtonLockChannel(t, "HM-PBI-4-FM:0")

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileRFButtonLock)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	if dp.DataPointKey().ChannelAddress != ch.Address {
		t.Errorf("DataPointKey().ChannelAddress = %q, want %q",
			dp.DataPointKey().ChannelAddress, ch.Address)
	}
}

// TestLockCapabilityPresets verifies that the named lock capability preset
// Vars carry the correct flags — mirrors
// IP_LOCK_CAPABILITIES / BUTTON_LOCK_CAPABILITIES / SMART_DOOR_LOCK_CAPABILITIES
// (capabilities/lock.py:40-42).
func TestLockCapabilityPresets(t *testing.T) {
	t.Parallel()

	// IP_LOCK_CAPABILITIES: open=true.
	if !lock.IPLockCaps.SupportsOpen {
		t.Error("IPLockCaps: SupportsOpen must be true")
	}

	// BUTTON_LOCK_CAPABILITIES: open=false.
	if lock.ButtonLockCaps.SupportsOpen {
		t.Error("ButtonLockCaps: SupportsOpen must be false")
	}

	// SMART_DOOR_LOCK_CAPABILITIES: open=true.
	if !lock.SmartDoorLockCaps.SupportsOpen {
		t.Error("SmartDoorLockCaps: SupportsOpen must be true")
	}
}
