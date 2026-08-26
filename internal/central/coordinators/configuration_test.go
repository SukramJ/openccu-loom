// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func newCoord(t *testing.T) (*ConfigurationCoordinator, *registry.ParamsetRegistry, *registry.DeviceDescriptionRegistry) {
	t.Helper()
	descs := registry.NewDeviceDescriptionRegistry()
	pss := registry.NewParamsetRegistry()
	devs := registry.NewDeviceRegistry()
	return NewConfigurationCoordinator(descs, pss, devs), pss, descs
}

func TestConfigurationCoordinatorGetParameterData(t *testing.T) {
	c, pss, _ := newCoord(t)
	pss.Put(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"TRANSMIT_TRY_MAX": hmproto.ParameterData{Type: hmenum.ParameterTypeInteger},
	})

	pd, ok := c.GetParameterData(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyMaster, "TRANSMIT_TRY_MAX")
	if !ok || pd.Type != hmenum.ParameterTypeInteger {
		t.Fatalf("hit miss: %+v ok=%v", pd, ok)
	}

	if _, ok := c.GetParameterData(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyMaster, "MISSING"); ok {
		t.Fatal("missing param must report ok=false")
	}
	if _, ok := c.GetParameterData(wireKey(hmenum.InterfaceHmIPRF), "OTHER:1", hmenum.ParamsetKeyMaster, "TRANSMIT_TRY_MAX"); ok {
		t.Fatal("unknown channel must report ok=false")
	}
}

func TestConfigurationCoordinatorGetChannelParamset(t *testing.T) {
	c, pss, _ := newCoord(t)
	pss.Put(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyValues, hmproto.Paramset{
		"LEVEL":   hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
		"WORKING": hmproto.ParameterData{Type: hmenum.ParameterTypeBool},
	})

	got, ok := c.GetChannelParamset(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyValues)
	if !ok || len(got) != 2 {
		t.Fatalf("got=%+v ok=%v", got, ok)
	}
	// Caller must be able to mutate the returned map without affecting
	// the cached source.
	got["LEVEL"] = hmproto.ParameterData{Type: hmenum.ParameterTypeString}
	again, _ := c.GetChannelParamset(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyValues)
	if again["LEVEL"].Type != hmenum.ParameterTypeFloat {
		t.Fatal("mutating returned copy must not bleed back into the registry")
	}
}

func TestConfigurationCoordinatorPatchOverridesRegistry(t *testing.T) {
	c, pss, _ := newCoord(t)
	pss.Put(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"TRANSMIT_TRY_MAX": hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Min: []byte("0")},
	})
	c.PatchParameter("0001ABCD:1", hmenum.ParamsetKeyMaster, "TRANSMIT_TRY_MAX",
		hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Min: []byte("1")})

	pd, ok := c.GetParameterData(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyMaster, "TRANSMIT_TRY_MAX")
	if !ok || string(pd.Min) != "1" {
		t.Fatalf("patch did not override: %+v", pd)
	}
	// Channel paramset must surface the patch as well.
	full, _ := c.GetChannelParamset(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyMaster)
	if string(full["TRANSMIT_TRY_MAX"].Min) != "1" {
		t.Fatal("channel paramset must reflect patch")
	}

	// ClearPatch reverts to the registry value.
	if !c.ClearPatch("0001ABCD:1", hmenum.ParamsetKeyMaster, "TRANSMIT_TRY_MAX") {
		t.Fatal("ClearPatch must report true on existing patch")
	}
	pd, _ = c.GetParameterData(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyMaster, "TRANSMIT_TRY_MAX")
	if string(pd.Min) != "0" {
		t.Fatalf("after clear: registry min=0 expected, got %q", pd.Min)
	}
	if c.ClearPatch("0001ABCD:1", hmenum.ParamsetKeyMaster, "TRANSMIT_TRY_MAX") {
		t.Fatal("second ClearPatch must report false")
	}
}

func TestConfigurationCoordinatorHasParameter(t *testing.T) {
	c, pss, _ := newCoord(t)
	pss.Put(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyValues, hmproto.Paramset{
		"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	})
	if !c.HasParameter(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyValues, "LEVEL") {
		t.Fatal("LEVEL must be reported as present")
	}
	if c.HasParameter(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyValues, "GHOST") {
		t.Fatal("GHOST must not be reported as present")
	}
}

func TestConfigurationCoordinatorConfigurableChannels(t *testing.T) {
	c, pss, descs := newCoord(t)

	descs.Put(wireKey(hmenum.InterfaceHmIPRF), hmproto.DeviceDescription{
		Address: "0001ABCD",
		Type:    "HmIP-STH",
	})
	descs.Put(wireKey(hmenum.InterfaceHmIPRF), hmproto.DeviceDescription{
		Address: "0001ABCD:1",
		Type:    "CLIMATE_TRANSCEIVER",
		Parent:  "0001ABCD",
	})
	descs.Put(wireKey(hmenum.InterfaceHmIPRF), hmproto.DeviceDescription{
		Address: "0001ABCD:2",
		Type:    "MAINTENANCE",
		Parent:  "0001ABCD",
	})
	pss.Put(wireKey(hmenum.InterfaceHmIPRF), "0001ABCD:1", hmenum.ParamsetKeyMaster, hmproto.Paramset{
		"TEMPERATURE_OFFSET": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat},
	})

	got := c.ConfigurableChannels(wireKey(hmenum.InterfaceHmIPRF))
	if len(got) != 1 {
		t.Fatalf("expected 1 configurable channel, got %d (%+v)", len(got), got)
	}
	if got[0].ChannelAddress != "0001ABCD:1" || got[0].ParamCount != 1 {
		t.Fatalf("ConfigurableChannel = %+v", got[0])
	}
	if got[0].DeviceAddress != "0001ABCD" {
		t.Fatalf("DeviceAddress=%q want 0001ABCD", got[0].DeviceAddress)
	}
}
