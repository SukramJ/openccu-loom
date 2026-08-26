// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

const (
	testChannel = "ABC0001:1"
	testParam   = "LEVEL"
)

// testIface is the registry key the coordinator is exercised with — the wire
// id, not the bare interface name.
var testIface = wireKey(hmenum.InterfaceHmIPRF)

// newTestConfCoord builds a coordinator seeded with one channel and one
// VALUES paramset containing a single LEVEL parameter.
func newTestConfCoord() (*ConfigurationCoordinator, *registry.DeviceDescriptionRegistry, *registry.ParamsetRegistry) {
	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	// Register a root device and a channel.
	descs.Put(testIface, hmproto.DeviceDescription{Address: "ABC0001", Type: "HM-CC-RT-DN"})
	descs.Put(testIface, hmproto.DeviceDescription{Address: testChannel, Parent: "ABC0001", Type: "THERMALCONTROL_TRANSMIT"})

	paramset := hmproto.Paramset{
		testParam: hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	}
	ps.Put(testIface, testChannel, hmenum.ParamsetKeyValues, paramset)

	cc := NewConfigurationCoordinator(descs, ps, devs)
	return cc, descs, ps
}

func TestConfigurationGetParameterDataRegistryFallback(t *testing.T) {
	t.Parallel()
	cc, _, _ := newTestConfCoord()

	pd, ok := cc.GetParameterData(testIface, testChannel, hmenum.ParamsetKeyValues, testParam)
	if !ok {
		t.Fatal("GetParameterData: expected ok=true for known parameter")
	}
	if pd.Type != hmenum.ParameterTypeFloat {
		t.Fatalf("Type=%v want %v", pd.Type, hmenum.ParameterTypeFloat)
	}
}

func TestConfigurationPatchOverridesRegistry(t *testing.T) {
	t.Parallel()
	cc, _, _ := newTestConfCoord()

	patch := hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Unit: "°C"}
	cc.PatchParameter(testChannel, hmenum.ParamsetKeyValues, testParam, patch)

	pd, ok := cc.GetParameterData(testIface, testChannel, hmenum.ParamsetKeyValues, testParam)
	if !ok {
		t.Fatal("GetParameterData: expected ok=true after patch")
	}
	if pd.Type != hmenum.ParameterTypeInteger {
		t.Fatalf("Type=%v want INTEGER (patch should win)", pd.Type)
	}
	if pd.Unit != "°C" {
		t.Fatalf("Unit=%q want °C", pd.Unit)
	}
}

func TestConfigurationClearPatchRestoresFallback(t *testing.T) {
	t.Parallel()
	cc, _, _ := newTestConfCoord()

	patch := hmproto.ParameterData{Type: hmenum.ParameterTypeInteger}
	cc.PatchParameter(testChannel, hmenum.ParamsetKeyValues, testParam, patch)

	// Confirm patch is active.
	pd, _ := cc.GetParameterData(testIface, testChannel, hmenum.ParamsetKeyValues, testParam)
	if pd.Type != hmenum.ParameterTypeInteger {
		t.Fatal("patch should be active before ClearPatch")
	}

	// Clear must report true on first call, false on second.
	if !cc.ClearPatch(testChannel, hmenum.ParamsetKeyValues, testParam) {
		t.Fatal("ClearPatch: expected true when patch existed")
	}
	if cc.ClearPatch(testChannel, hmenum.ParamsetKeyValues, testParam) {
		t.Fatal("ClearPatch: expected false when patch already gone")
	}

	// After clear, the registry value must be back.
	pd, ok := cc.GetParameterData(testIface, testChannel, hmenum.ParamsetKeyValues, testParam)
	if !ok {
		t.Fatal("GetParameterData should still hit registry after patch is cleared")
	}
	if pd.Type != hmenum.ParameterTypeFloat {
		t.Fatalf("Type=%v want FLOAT (registry value) after clear", pd.Type)
	}
}

func TestConfigurationHasParameter(t *testing.T) {
	t.Parallel()
	cc, _, _ := newTestConfCoord()

	if !cc.HasParameter(testIface, testChannel, hmenum.ParamsetKeyValues, testParam) {
		t.Fatal("HasParameter: expected true for known parameter")
	}
	if cc.HasParameter(testIface, testChannel, hmenum.ParamsetKeyValues, "NO_SUCH_PARAM") {
		t.Fatal("HasParameter: expected false for unknown parameter")
	}
}

func TestConfigurationGetChannelParamset(t *testing.T) {
	t.Parallel()
	cc, _, _ := newTestConfCoord()

	ps, ok := cc.GetChannelParamset(testIface, testChannel, hmenum.ParamsetKeyValues)
	if !ok {
		t.Fatal("GetChannelParamset: expected ok=true for registered channel")
	}
	if _, exists := ps[testParam]; !exists {
		t.Fatalf("GetChannelParamset: expected %q in returned paramset", testParam)
	}

	// Verify the caller receives a copy — mutating it must not affect the registry.
	ps[testParam] = hmproto.ParameterData{Type: hmenum.ParameterTypeString}
	orig, _ := cc.GetChannelParamset(testIface, testChannel, hmenum.ParamsetKeyValues)
	if orig[testParam].Type == hmenum.ParameterTypeString {
		t.Fatal("GetChannelParamset must return a copy; mutation affected internal state")
	}

	// Unknown channel → not found.
	_, ok = cc.GetChannelParamset(testIface, "UNKNOWN:1", hmenum.ParamsetKeyValues)
	if ok {
		t.Fatal("GetChannelParamset: expected ok=false for unknown channel")
	}
}

func TestConfigurationConfigurableChannels(t *testing.T) {
	t.Parallel()
	descs := registry.NewDeviceDescriptionRegistry()
	ps := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()

	// Two channels: one has a MASTER paramset (configurable), one does not.
	descs.Put(testIface, hmproto.DeviceDescription{Address: "DEV0001"})
	descs.Put(testIface, hmproto.DeviceDescription{Address: "DEV0001:0", Parent: "DEV0001", Type: "KEY"})
	descs.Put(testIface, hmproto.DeviceDescription{Address: "DEV0001:1", Parent: "DEV0001", Type: "THERMALCONTROL_TRANSMIT"})

	// Only DEV0001:1 gets a MASTER paramset.
	ps.Put(testIface, "DEV0001:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"MIN_TEMP": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	})

	cc := NewConfigurationCoordinator(descs, ps, devs)
	channels := cc.ConfigurableChannels(testIface)

	if len(channels) != 1 {
		t.Fatalf("ConfigurableChannels: got %d want 1", len(channels))
	}
	if channels[0].ChannelAddress != "DEV0001:1" {
		t.Fatalf("ChannelAddress=%q want DEV0001:1", channels[0].ChannelAddress)
	}
	if channels[0].ParamCount != 1 {
		t.Fatalf("ParamCount=%d want 1", channels[0].ParamCount)
	}
}

func TestConfigurationNilRegistriesSafeReturnEmpty(t *testing.T) {
	t.Parallel()
	// All-nil coordinator must not panic on any public method.
	cc := NewConfigurationCoordinator(nil, nil, nil)

	_, ok := cc.GetParameterData(testIface, testChannel, hmenum.ParamsetKeyValues, testParam)
	if ok {
		t.Fatal("GetParameterData with nil registry: expected ok=false")
	}

	_, ok = cc.GetChannelParamset(testIface, testChannel, hmenum.ParamsetKeyValues)
	if ok {
		t.Fatal("GetChannelParamset with nil registry: expected ok=false")
	}

	if cc.HasParameter(testIface, testChannel, hmenum.ParamsetKeyValues, testParam) {
		t.Fatal("HasParameter with nil registry: expected false")
	}

	if got := cc.ConfigurableChannels(testIface); got != nil {
		t.Fatalf("ConfigurableChannels with nil registries: expected nil, got %v", got)
	}
}
