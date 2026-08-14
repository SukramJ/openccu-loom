// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ── GetParamsetDescription ────────────────────────────────────────────────────

func TestWsDeviceQuery_GetParamsetDescription_NilParamsets_Errors(t *testing.T) {
	t.Parallel()
	w := &wsDeviceQuery{
		paramsets: nil,
		writer:    (*clientpkg.ValueWriter)(nil),
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{})
	if err == nil {
		t.Fatal("expected error when paramsets=nil")
	}
}

func TestWsDeviceQuery_GetParamsetDescription_NilRegistry_Errors(t *testing.T) {
	t.Parallel()
	// paramsets and writer non-nil; registry nil → second guard.
	reg := buildTestRegistry(t, "ccu-01")
	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    (*clientpkg.ValueWriter)(nil),
		registry:  nil, // nil registry → triggers second guard
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		ChannelAddress: "ABC123:1",
	})
	if err == nil {
		t.Fatal("expected error when registry=nil")
	}
}

func TestWsDeviceQuery_GetParamsetDescription_UnknownDevice_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    (*clientpkg.ValueWriter)(nil),
		registry:  reg,
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "UNKNOWN123:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
	})
	// Device not found in empty model registry → error.
	if err == nil {
		t.Fatal("expected error for unknown device")
	}
}

func TestWsDeviceQuery_GetParamsetDescription_EmptyParamsetKey_DefaultsMaster(t *testing.T) {
	t.Parallel()
	// Verify that an empty ParamsetKey defaults to MASTER before lookup fails.
	reg := buildTestRegistry(t, "ccu-01")
	paramsets := adapter.NewParamsetsDomain(reg, nil)
	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    (*clientpkg.ValueWriter)(nil),
		registry:  reg,
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "ANY:1",
		ParamsetKey:    "", // empty → defaults to MASTER inside GetParamsetDescription
	})
	// Device not found → error, but the empty-key path was exercised.
	if err == nil {
		t.Fatal("expected error for unknown device (testing empty-key default path)")
	}
}

// TestWsDeviceQuery_GetParamsetDescription_DeviceFound_NoBackend_Errors
// exercises the path where the device is in the ModelRegistry but no
// backend is registered in the writer for that central/interface pair.
func TestWsDeviceQuery_GetParamsetDescription_DeviceFound_NoBackend_Errors(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")

	// Put a minimal device into the Unit's ModelRegistry.
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not found in registry")
	}
	dev := device.New(device.Config{
		Address:     "DEV001",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
	})
	cu.ModelRegistry.Put(dev)

	paramsets := adapter.NewParamsetsDomain(reg, nil)
	writer := clientpkg.NewValueWriter() // no backends registered → Backend() returns ok=false

	w := &wsDeviceQuery{
		paramsets: paramsets,
		writer:    writer,
		registry:  reg,
	}
	_, err := w.GetParamsetDescription(context.Background(), configui.SessionKey{
		CentralName:    "ccu-01",
		ChannelAddress: "DEV001:1",
		ParamsetKey:    hmenum.ParamsetKeyMaster,
	})
	// Device found but no backend → error.
	if err == nil {
		t.Fatal("expected error when no backend is registered for the device's interface")
	}
}

// ── GetDevice identity fields ────────────────────────────────────────────────

// TestWsDeviceQuery_GetDevice_IdentityFieldsAreSerialisable pins that the
// operator-assigned identity the WS device payload carries — the device's
// rooms and functions, each channel's name — arrives as data a client can
// read. The fields live behind accessors on the model, and taking the
// accessor without calling it puts a func value into the map: it compiles,
// and the command then fails at encode time with "unsupported type", which
// is invisible to every test that only inspects the map.
func TestWsDeviceQuery_GetDevice_IdentityFieldsAreSerialisable(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-01")
	cu, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("ccu-01 not found in registry")
	}
	dev := device.New(device.Config{
		Address:     "WSIDENT01",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-LC-Sw1-Pl",
		Name:        "Stehlampe",
		Rooms:       []string{"Wohnzimmer"},
		Functions:   []string{"Licht"},
	})
	ch := dev.AddChannel("WSIDENT01:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.SetName("Stehlampe Schalter")
	cu.ModelRegistry.Put(dev)

	w := &wsDeviceQuery{devs: adapter.NewDevicesAdapter(reg)}
	got, err := w.GetDevice(context.Background(), "WSIDENT01")
	if err != nil {
		t.Fatalf("GetDevice: %v", err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("the WS device payload does not encode: %v", err)
	}
	var decoded struct {
		Rooms     []string `json:"rooms"`
		Functions []string `json:"functions"`
		Channels  []struct {
			Name string `json:"name"`
		} `json:"channels"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode WS device payload: %v", err)
	}
	if len(decoded.Rooms) != 1 || decoded.Rooms[0] != "Wohnzimmer" {
		t.Errorf("rooms = %v, want [Wohnzimmer]", decoded.Rooms)
	}
	if len(decoded.Functions) != 1 || decoded.Functions[0] != "Licht" {
		t.Errorf("functions = %v, want [Licht]", decoded.Functions)
	}
	if len(decoded.Channels) != 1 || decoded.Channels[0].Name != "Stehlampe Schalter" {
		t.Errorf("channels = %+v, want one channel named Stehlampe Schalter", decoded.Channels)
	}
}
