// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// central_name_propagation_test.go verifies that custom-DP constructors
// (switch, valve) and the mixin helpers (NewStateSwitch, NewLevelFloat)
// correctly propagate CentralName into the generic sub-DPs they allocate
// so that UniqueID() does not collide across multi-CCU deployments.
//
// Tests are at the custom package level (package custom_test) so they
// exercise the exported surface used by callers.
package custom_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/builtins"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/custom/valve"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// nopWriter satisfies [custom.Writer] without performing any real
// writes. Used wherever a non-nil writer is required by the constructor
// but no CCU roundtrip should happen.
type nopWriter struct{}

func (nopWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	return nil
}

// makeChannelWithCentralName builds a minimal device/channel pair and
// stamps the given centralName on the channel — mirroring what the
// device pipeline does during hydrateChannel.
func makeChannelWithCentralName(t *testing.T, addr, centralName string) *device.Channel {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0001"})
	ch := d.AddChannel(addr, 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetCentralName(centralName)
	return ch
}

// makeChannelWithStateDPAndCentralName builds a channel, stamps centralName on
// it, and registers a STATE *generic.Switch wire-DP carrying both centralName
// and the given writer. Used by tests that verify CentralName propagation
// through switchdev.New(ch, custom.RebasedChannelGroupConfig{}) / valve.NewIrrigation(ch, custom.RebasedChannelGroupConfig{}).
func makeChannelWithStateDPAndCentralName(t *testing.T, addr, centralName string, w custom.Writer) *device.Channel {
	t.Helper()
	ch := makeChannelWithCentralName(t, addr, centralName)
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		CentralName: centralName,
		Writer:      w,
	})
	ch.Put(dp)
	return ch
}

// makeChannelWithLevelDPAndCentralName builds a channel, stamps centralName on
// it, and registers a LEVEL *generic.Float wire-DP carrying both centralName
// and the given writer. Used by tests that verify CentralName propagation
// through valve.NewModulating(ch, custom.RebasedChannelGroupConfig{}).
func makeChannelWithLevelDPAndCentralName(t *testing.T, addr, centralName string, w custom.Writer) *device.Channel {
	t.Helper()
	ch := makeChannelWithCentralName(t, addr, centralName)
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		CentralName: centralName,
		Writer:      w,
	})
	ch.Put(dp)
	return ch
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 1 – NewStateSwitch propagates CentralName into the embedded *generic.Switch
// ─────────────────────────────────────────────────────────────────────────────

// TestNewStateSwitchPropagatesCentralName verifies that the
// *generic.Switch returned by [custom.NewStateSwitch] carries the
// CentralName in its UniqueID, making it distinguishable from an
// identical channel address on a different CCU.
func TestNewStateSwitchPropagatesCentralName(t *testing.T) {
	t.Parallel()

	const (
		addr        = "VCU0001:3"
		centralName = "ccu-living-room"
	)

	sw := custom.NewStateSwitch(addr, centralName, nopWriter{})
	if sw == nil {
		t.Fatal("NewStateSwitch returned nil")
	}

	uid := sw.UniqueID()
	if uid == "" {
		t.Fatal("UniqueID() is empty")
	}
	// UniqueID format: "<central>:<address>:<parameter>"
	// The leading segment must be centralName.
	if got := sw.Central(); got != centralName {
		t.Errorf("Central() = %q, want %q (UniqueID = %q)", got, centralName, uid)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 2 – NewLevelFloat propagates CentralName into the embedded *generic.Float
// ─────────────────────────────────────────────────────────────────────────────

// TestNewLevelFloatPropagatesCentralName verifies that the
// *generic.Float returned by [custom.NewLevelFloat] carries the
// CentralName in its UniqueID.
func TestNewLevelFloatPropagatesCentralName(t *testing.T) {
	t.Parallel()

	const (
		addr        = "VCU0002:1"
		centralName = "ccu-garage"
	)

	lf := custom.NewLevelFloat(addr, centralName, nopWriter{})
	if lf == nil {
		t.Fatal("NewLevelFloat returned nil")
	}

	if got := lf.Central(); got != centralName {
		t.Errorf("Central() = %q, want %q (UniqueID = %q)", got, centralName, lf.UniqueID())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 3 – switchdev.New propagates CentralName (via NewStateSwitch)
// ─────────────────────────────────────────────────────────────────────────────

// TestSwitchDevNewPropagatesCentralName verifies that [switchdev.New]
// stamps CentralName on the embedded *generic.Switch so that
// UniqueID() is properly scoped to the originating CCU.
func TestSwitchDevNewPropagatesCentralName(t *testing.T) {
	t.Parallel()

	const (
		addr        = "HmIP-PS:4"
		centralName = "ccu-office"
	)

	ch := makeChannelWithStateDPAndCentralName(t, addr, centralName, nopWriter{})
	sw := switchdev.New(ch, custom.RebasedChannelGroupConfig{})
	if sw == nil {
		t.Fatal("switchdev.New returned nil")
	}

	if got := sw.Central(); got != centralName {
		t.Errorf("Switch.Central() = %q, want %q (UniqueID = %q)", got, centralName, sw.UniqueID())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 4 – valve.NewIrrigation propagates CentralName
// ─────────────────────────────────────────────────────────────────────────────

// TestIrrigationValveNewPropagatesCentralName verifies that
// [valve.NewIrrigation] propagates CentralName into the embedded
// *generic.Switch (via [custom.NewStateSwitch]).
func TestIrrigationValveNewPropagatesCentralName(t *testing.T) {
	t.Parallel()

	const (
		addr        = "HmIP-IRRIG:1"
		centralName = "ccu-garden"
	)

	ch := makeChannelWithStateDPAndCentralName(t, addr, centralName, nopWriter{})
	v := valve.NewIrrigation(ch, custom.RebasedChannelGroupConfig{})
	if v == nil {
		t.Fatal("valve.NewIrrigation returned nil")
	}

	if got := v.Central(); got != centralName {
		t.Errorf("Irrigation.Central() = %q, want %q (UniqueID = %q)", got, centralName, v.UniqueID())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 5 – valve.NewModulating propagates CentralName
// ─────────────────────────────────────────────────────────────────────────────

// TestModulatingValveNewPropagatesCentralName verifies that
// [valve.NewModulating] propagates CentralName into the embedded
// *generic.Float (via [custom.NewLevelFloat]).
func TestModulatingValveNewPropagatesCentralName(t *testing.T) {
	t.Parallel()

	const (
		addr        = "HmIP-FALMOT:3"
		centralName = "ccu-hvac"
	)

	ch := makeChannelWithLevelDPAndCentralName(t, addr, centralName, nopWriter{})
	v := valve.NewModulating(ch, custom.RebasedChannelGroupConfig{})
	if v == nil {
		t.Fatal("valve.NewModulating returned nil")
	}

	if got := v.Central(); got != centralName {
		t.Errorf("Modulating.Central() = %q, want %q (UniqueID = %q)", got, centralName, v.UniqueID())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test 6 – init.go constructor propagates ch.CentralName() to the sub-DP
// ─────────────────────────────────────────────────────────────────────────────

// TestCustomMaterializerPropagatesCentralName exercises the full
// materializer path: a channel whose CentralName has been stamped by
// the pipeline produces a custom DP whose sub-DP carries that name.
// This validates the end-to-end flow:
//
//	channel.SetCentralName → init.go constructor → switchdev.New
//	→ NewStateSwitch → generic.Spec.CentralName → UniqueID().
func TestCustomMaterializerPropagatesCentralName(t *testing.T) {
	t.Parallel()

	const (
		addr        = "HmIP-PS:1"
		centralName = "ccu-main"
	)

	// The STATE wire-DP must be present on the channel before the constructor
	// runs because switchdev.New(ch, custom.RebasedChannelGroupConfig{}) retrieves it via SwitchField rather than
	// allocating a fresh one.
	ch := makeChannelWithStateDPAndCentralName(t, addr, centralName, nopWriter{})

	// Build a registry with the IPSwitch constructor and a minimal
	// profile so CreateCustomDataPoint can materialise it.
	reg := custom.NewRegistry()

	profile := custom.Profile{
		Name:       hmenum.DeviceProfileIPSwitch,
		DeviceType: "HmIP-PS",
		Category:   hmenum.DataPointCategorySwitch,
		Channels:   []custom.ChannelRoleAssignment{{Channel: 0, Role: custom.ChannelRolePrimary}},
		Config: &custom.ProfileConfig{
			ProfileType: hmenum.DeviceProfileIPSwitch,
			ChannelGroup: custom.ChannelGroupConfig{
				PrimaryChannel:    1,
				PrimaryChannelSet: true,
			},
		},
	}
	if err := reg.Register(profile); err != nil {
		t.Fatalf("Register profile: %v", err)
	}

	// Use the real IPSwitch constructor from the switch sub-package.
	// switchdev.New(ch, custom.RebasedChannelGroupConfig{}) retrieves the channel's existing STATE wire-DP, so
	// CentralName propagates through the DP's Config.CentralName field (set
	// above in makeChannelWithStateDPAndCentralName) rather than via a fresh
	// allocation.
	ctor := func(ch *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
		return switchdev.New(ch, custom.RebasedChannelGroupConfig{}), nil
	}
	if err := reg.RegisterConstructor(hmenum.DeviceProfileIPSwitch, ctor); err != nil {
		t.Fatalf("RegisterConstructor: %v", err)
	}

	dev := ch.Device()
	if err := custom.CreateCustomDataPoint(dev, ch, profile, reg); err != nil {
		t.Fatalf("CreateCustomDataPoint: %v", err)
	}

	cdp := ch.CustomDataPoint()
	if cdp == nil {
		t.Fatal("channel has no custom DP after materialisation")
	}

	sw, ok := cdp.(*switchdev.Switch)
	if !ok {
		t.Fatalf("custom DP type = %T, want *switchdev.Switch", cdp)
	}

	if got := sw.Central(); got != centralName {
		t.Errorf("Switch.Central() = %q, want %q", got, centralName)
	}
}
