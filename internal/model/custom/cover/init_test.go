// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

// init_test.go — tests for the D.12 constructor registration.
//
// These tests verify that:
// 1. Every expected DeviceProfile has a constructor registered on the
// DefaultRegistry (contract: no silent skip in the hot path).
// 2. The constructors produce the correct concrete type when called with
// a channel that carries the relevant data points.
// 3. The Garage type satisfies device.AttachableDataPoint after the D.12
// key change.

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
)

// coverProfiles lists every DeviceProfile string the cover init()
// must register.
var coverProfiles = []hmenum.DeviceProfile{
	hmenum.DeviceProfile("IPCover"),
	hmenum.DeviceProfile("RfCover"),
	hmenum.DeviceProfile("IPHdm"),
	hmenum.DeviceProfile("IPGarage"),
}

// TestCoverConstructorsRegistered verifies that every cover profile
// has a non-nil constructor in the default registry after init().
func TestCoverConstructorsRegistered(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	for _, p := range coverProfiles {
		ctor, ok := r.Constructor(p)
		if !ok {
			t.Errorf("no constructor registered for profile %q", p)
			continue
		}
		if ctor == nil {
			t.Errorf("nil constructor registered for profile %q", p)
		}
	}
}

// TestIPCoverConstructorProducesCover verifies that calling the IPCover
// constructor on a channel with only LEVEL (no LEVEL_2) returns a *Cover.
func TestIPCoverConstructorProducesCover(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("IPCover"))
	if !ok {
		t.Fatal("IPCover constructor not registered")
	}

	ch := newChannelWithLevel(t, "HmIP-BROLL:3", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("IPCover constructor error: %v", err)
	}
	if dp == nil {
		t.Fatal("IPCover constructor returned nil")
	}
	if _, ok := dp.(*Cover); !ok {
		t.Errorf("IPCover constructor returned %T, want *Cover", dp)
	}
}

// TestIPCoverConstructorProducesBlindWhenLevel2Present verifies that the
// IPCover constructor produces a *Blind when LEVEL_2 is present on the
// channel (IP Blind devices carry both LEVEL and LEVEL_2).
func TestIPCoverConstructorProducesBlindWhenLevel2Present(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("IPCover"))
	if !ok {
		t.Fatal("IPCover constructor not registered")
	}

	ch := newChannelWithLevelAndLevel2(t, "HmIP-BBL:3", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("IPCover (blind) constructor error: %v", err)
	}
	if dp == nil {
		t.Fatal("IPCover constructor returned nil")
	}
	if _, ok := dp.(*Blind); !ok {
		t.Errorf("IPCover (blind) constructor returned %T, want *Blind", dp)
	}
}

// TestRfCoverConstructorProducesCover verifies that the RfCover constructor
// returns a *Cover for a plain shutter channel.
func TestRfCoverConstructorProducesCover(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("RfCover"))
	if !ok {
		t.Fatal("RfCover constructor not registered")
	}

	ch := newChannelWithLevel(t, "HM-LC-Bl1-FM:1", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("RfCover constructor error: %v", err)
	}
	if _, ok := dp.(*Cover); !ok {
		t.Errorf("RfCover constructor returned %T, want *Cover", dp)
	}
}

// TestIPHdmConstructorProducesBlind verifies that the IPHdm constructor
// always returns a *Blind (HDM is always a tilting blind).
func TestIPHdmConstructorProducesBlind(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("IPHdm"))
	if !ok {
		t.Fatal("IPHdm constructor not registered")
	}

	ch := newChannelWithLevel(t, "HmIP-HDM:1", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("IPHdm constructor error: %v", err)
	}
	if _, ok := dp.(*Blind); !ok {
		t.Errorf("IPHdm constructor returned %T, want *Blind", dp)
	}
}

// TestIPGarageConstructorProducesGarage verifies that the IPGarage
// constructor returns a *Garage.
func TestIPGarageConstructorProducesGarage(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("IPGarage"))
	if !ok {
		t.Fatal("IPGarage constructor not registered")
	}

	ch := newGarageChannel(t, "HmIP-MOD-HO:1", &stubWriter{})
	dp, err := ctor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("IPGarage constructor error: %v", err)
	}
	if _, ok := dp.(*Garage); !ok {
		t.Errorf("IPGarage constructor returned %T, want *Garage", dp)
	}
}

// TestGarageDataPointKeyIsNonZero verifies that NewGarage correctly
// populates the DataPointKey so *Garage satisfies device.AttachableDataPoint.
func TestGarageDataPointKeyIsNonZero(t *testing.T) {
	t.Parallel()

	ch := newGarageChannel(t, "HmIP-MOD-HO:1", &stubWriter{})
	g := NewGarage(GarageConfig{Channel: ch, Writer: &stubWriter{}})
	key := g.DataPointKey()
	if key.ChannelAddress == "" {
		t.Error("Garage.DataPointKey().ChannelAddress must not be empty")
	}
	if key.Parameter == "" {
		t.Error("Garage.DataPointKey().Parameter must not be empty")
	}
}

// TestIPCoverConstructorNilChannelDoesNotPanic verifies that passing nil
// as the channel to the IPCover constructor does not panic.
func TestIPCoverConstructorNilChannelDoesNotPanic(t *testing.T) {
	t.Parallel()

	r := custom.DefaultRegistry()
	ctor, ok := r.Constructor(hmenum.DeviceProfile("IPCover"))
	if !ok {
		t.Fatal("IPCover constructor not registered")
	}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("IPCover constructor panicked with nil channel: %v", r)
		}
	}()
	dp, err := ctor(nil, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("unexpected error with nil channel: %v", err)
	}
	if dp == nil {
		t.Fatal("constructor with nil channel returned nil DP")
	}
}

// TestCoverCapabilityPresets verifies that the named cover capability preset
// vars ( CoverCaps BlindCaps GarageCaps) carry the correct
// Flags — mirrors
// GARAGE_CAPABILITIES (capabilities/cover.py:43-60).
func TestCoverCapabilityPresets(t *testing.T) {
	t.Parallel()

	// COVER_CAPABILITIES: position + stop, no tilt, no vent.
	if !CoverCaps.SupportsPosition {
		t.Error("CoverCaps: SupportsPosition must be true")
	}
	if !CoverCaps.SupportsStop {
		t.Error("CoverCaps: SupportsStop must be true")
	}
	if CoverCaps.SupportsTilt {
		t.Error("CoverCaps: SupportsTilt must be false")
	}
	if CoverCaps.SupportsVent {
		t.Error("CoverCaps: SupportsVent must be false")
	}

	// BLIND_CAPABILITIES: position + tilt + stop.
	if !BlindCaps.SupportsPosition {
		t.Error("BlindCaps: SupportsPosition must be true")
	}
	if !BlindCaps.SupportsTilt {
		t.Error("BlindCaps: SupportsTilt must be true")
	}
	if !BlindCaps.SupportsStop {
		t.Error("BlindCaps: SupportsStop must be true")
	}
	if BlindCaps.SupportsVent {
		t.Error("BlindCaps: SupportsVent must be false")
	}

	// GARAGE_CAPABILITIES: position + stop + vent (no tilt).
	if !GarageCaps.SupportsPosition {
		t.Error("GarageCaps: SupportsPosition must be true")
	}
	if !GarageCaps.SupportsStop {
		t.Error("GarageCaps: SupportsStop must be true")
	}
	if !GarageCaps.SupportsVent {
		t.Error("GarageCaps: SupportsVent must be true")
	}
	if GarageCaps.SupportsTilt {
		t.Error("GarageCaps: SupportsTilt must be false")
	}
}

// --- helpers ---

// newChannelWithLevel returns a channel that carries a LEVEL data point.
func newChannelWithLevel(t *testing.T, address string, w Writer) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	ch := d.AddChannel(address, 1, "BLIND", hmenum.ParamsetKeyValues)
	level := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(level)
	return ch
}

// newChannelWithLevelAndLevel2 returns a channel with both LEVEL and LEVEL_2
// data points (blind / tilt cover).
func newChannelWithLevelAndLevel2(t *testing.T, address string, w Writer) *device.Channel {
	t.Helper()
	ch := newChannelWithLevel(t, address, w)
	level2 := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel2),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(level2)
	return ch
}

// newGarageChannel returns a channel carrying DOOR_STATE and DOOR_COMMAND
// data points as used by garage-door profiles.
func newGarageChannel(t *testing.T, address string, w Writer) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "MOD0001"})
	ch := d.AddChannel(address, 1, "GARAGE_DOOR", hmenum.ParamsetKeyValues)

	doorState := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterDoorState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(doorState)

	doorCmd := generic.NewStringSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterDoorCommand),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeString,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(doorCmd)
	return ch
}
