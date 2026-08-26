// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package valve_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/valve" // trigger init()
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putBoolDP attaches a *generic.Switch (STATE parameter) for param on ch.
func putBoolDP(ch *device.Channel, param hmenum.Parameter) *generic.Switch {
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// makeValveChannel builds a bare device + channel for constructor testing.
func makeValveChannel(t *testing.T, address string) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "JKL0001"})
	return d.AddChannel(address, 4, "SWITCH", hmenum.ParamsetKeyValues)
}

// --- Registration tests ---

// TestIPIrrigationValveConstructorIsRegistered verifies that the
// init() block registers a non-nil constructor for
// DeviceProfileIPIrrigationValve.
func TestIPIrrigationValveConstructorIsRegistered(t *testing.T) {
	t.Parallel()

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPIrrigationValve)
	if !ok || ctor == nil {
		t.Fatal("expected non-nil constructor for DeviceProfileIPIrrigationValve")
	}
}

// --- Constructor returns valid DP ---

// TestIPIrrigationValveConstructorReturnsValidDP verifies that the
// constructor returns a non-nil AttachableDataPoint without error.
func TestIPIrrigationValveConstructorReturnsValidDP(t *testing.T) {
	t.Parallel()

	ch := makeValveChannel(t, "HmIP-WSM:4")
	putBoolDP(ch, hmenum.ParameterState)

	ctor, ok := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPIrrigationValve)
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

// --- DataPointKey tests ---

// TestIPIrrigationValveConstructorDataPointKeyUsesChannelAddress
// verifies that the DataPointKey exposes the correct channel address.
// The key comes from the embedded *generic.Switch (STATE / VALUES).
func TestIPIrrigationValveConstructorDataPointKeyUsesChannelAddress(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-WSM:4"
	ch := makeValveChannel(t, addr)
	putBoolDP(ch, hmenum.ParameterState)

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPIrrigationValve)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	key := dp.DataPointKey()
	if key.ChannelAddress != addr {
		t.Errorf("DataPointKey().ChannelAddress = %q, want %q", key.ChannelAddress, addr)
	}
}

// TestIPIrrigationValveConstructorDataPointKeyParameterIsState verifies
// that the DataPointKey is keyed on STATE — the primary write parameter
// for irrigation valves. Mirrors the backing *generic.Switch key.
func TestIPIrrigationValveConstructorDataPointKeyParameterIsState(t *testing.T) {
	t.Parallel()

	ch := makeValveChannel(t, "HmIP-WSM:4")
	putBoolDP(ch, hmenum.ParameterState)

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPIrrigationValve)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	key := dp.DataPointKey()
	if key.Parameter != string(hmenum.ParameterState) {
		t.Errorf("DataPointKey().Parameter = %q, want %q",
			key.Parameter, hmenum.ParameterState)
	}
}

// TestIPIrrigationValveConstructorCanAttachToChannel verifies that the
// constructed DP can be attached to a channel via SetCustomDataPoint
// and retrieved back.
func TestIPIrrigationValveConstructorCanAttachToChannel(t *testing.T) {
	t.Parallel()

	ch := makeValveChannel(t, "HmIP-WSM:4")
	putBoolDP(ch, hmenum.ParameterState)

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPIrrigationValve)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor returned error: %v", err)
	}

	ch.SetCustomDataPoint(dp)
	if got := ch.CustomDataPoint(); got == nil {
		t.Fatal("channel must hold the attached custom DP")
	}
}

// TestIPIrrigationValveConstructorNilWriterIsSafe verifies that the
// constructor succeeds even when the STATE wire-DP has a nil writer
// installed, returning a functional *Irrigation. This is the typical
// state at hydration time before SetWriter() is called.
func TestIPIrrigationValveConstructorNilWriterIsSafe(t *testing.T) {
	t.Parallel()

	ch := makeValveChannel(t, "HmIP-WSM:4")
	// Register the STATE wire-DP with a nil writer — the constructor
	// must handle this gracefully (writer is wired later by the pipeline).
	putBoolDP(ch, hmenum.ParameterState)

	ctor, _ := custom.DefaultRegistry().Constructor(hmenum.DeviceProfileIPIrrigationValve)
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("constructor must not error for nil writer: %v", err)
	}
	if dp == nil {
		t.Fatal("constructor must return non-nil DP even without writer")
	}
}
