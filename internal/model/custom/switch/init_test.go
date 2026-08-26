// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package switchdev

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newSwitchInitChannel builds a minimal device + channel with a STATE
// *generic.Switch wire-DP suitable for constructor tests. No writer is
// installed on the DP; Channel.Writer() returns nil, which is valid at
// constructor time (the writer is wired later during device hydration).
func newSwitchInitChannel(t *testing.T, address string) *device.Channel {
	t.Helper()
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "VCU0001",
	})
	ch := d.AddChannel(address, 4, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return ch
}

// TestIPSwitchConstructorReturnsSwitch verifies that ipSwitchConstructor
// produces a non-nil *Switch with the correct address embedded.
func TestIPSwitchConstructorReturnsSwitch(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-PS:3"
	ch := newSwitchInitChannel(t, addr)

	dp, err := ipSwitchConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("ipSwitchConstructor() error = %v", err)
	}
	sw, ok := dp.(*Switch)
	if !ok {
		t.Fatalf("ipSwitchConstructor() type = %T, want *Switch", dp)
	}
	if got := sw.Address(); got != addr {
		t.Errorf("Switch.Address() = %q, want %q", got, addr)
	}
}

// TestRfSwitchConstructorReturnsSwitch verifies that rfSwitchConstructor
// produces a non-nil *Switch with the correct address.
func TestRfSwitchConstructorReturnsSwitch(t *testing.T) {
	t.Parallel()

	const addr = "VCU2128127:1"
	ch := newSwitchInitChannel(t, addr)

	dp, err := rfSwitchConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("rfSwitchConstructor() error = %v", err)
	}
	sw, ok := dp.(*Switch)
	if !ok {
		t.Fatalf("rfSwitchConstructor() type = %T, want *Switch", dp)
	}
	if got := sw.Address(); got != addr {
		t.Errorf("Switch.Address() = %q, want %q", got, addr)
	}
}

// TestSwitchConstructorGroupStateIsNonNil verifies that the Switch
// returned by the constructor has a non-nil GroupState tracker.
func TestSwitchConstructorGroupStateIsNonNil(t *testing.T) {
	t.Parallel()

	ch := newSwitchInitChannel(t, "HmIP-BS2:4")
	dp, err := ipSwitchConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("ipSwitchConstructor() error = %v", err)
	}
	sw := dp.(*Switch)
	if sw.GroupState() == nil {
		t.Error("Switch.GroupState() = nil, want non-nil tracker")
	}
}

// TestSwitchConstructorsRegisteredInDefaultRegistry verifies that both
// switch constructors are present in the DefaultRegistry after the
// package is imported (i.e. the init() block ran).
func TestSwitchConstructorsRegisteredInDefaultRegistry(t *testing.T) {
	t.Parallel()

	reg := custom.DefaultRegistry()
	profiles := []hmenum.DeviceProfile{
		hmenum.DeviceProfileIPSwitch,
		hmenum.DeviceProfileRfSwitch,
	}
	for _, p := range profiles {
		if _, ok := reg.Constructor(p); !ok {
			t.Errorf("DefaultRegistry missing constructor for profile %q", p)
		}
	}
}

// TestIPSwitchConstructorDataPointKeyCarriesAddress verifies that the
// DataPointKey returned by the constructor has the correct
// ChannelAddress set. Switch uses its embedded generic.Switch's key
// which is built without InterfaceID (by design — NewStateSwitch does
// not receive InterfaceID).
func TestIPSwitchConstructorDataPointKeyCarriesAddress(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-PS:3"
	ch := newSwitchInitChannel(t, addr)

	dp, err := ipSwitchConstructor(ch, custom.RebasedChannelGroupConfig{})
	if err != nil {
		t.Fatalf("ipSwitchConstructor() error = %v", err)
	}
	key := dp.DataPointKey()
	if key.ChannelAddress != addr {
		t.Errorf("DataPointKey().ChannelAddress = %q, want %q", key.ChannelAddress, addr)
	}
}
