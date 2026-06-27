// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------------------------------------------------------------------------
// intOrNil
// ---------------------------------------------------------------------------

func TestIntOrNil(t *testing.T) {
	t.Parallel()
	if got := intOrNil(nil); got != nil {
		t.Fatalf("intOrNil(nil) = %v, want nil", got)
	}
	v := -72
	if got := intOrNil(&v); got != -72 {
		t.Fatalf("intOrNil(&-72) = %v, want -72", got)
	}
}

// putRSSIDevice adds the channel-0 MAINTENANCE channel to dev and seeds an
// RSSI float data point with value v.
func putRSSIDevice(t *testing.T, dev *device.Device, param hmenum.Parameter, v float64) {
	t.Helper()
	ch0 := dev.Channel(dev.Address + ":0")
	if ch0 == nil {
		ch0 = dev.AddChannel(dev.Address+":0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	}
	dp := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    dev.InterfaceID,
			ChannelAddress: dev.Address + ":0",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch0.Put(dp)
	dp.OnEvent(v)
}

func TestRSSIInfoDomain_NilRegistryReturnsEmpty(t *testing.T) {
	t.Parallel()
	out, err := NewRSSIInfoDomain(nil).RSSIInfo(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if devs := out["devices"].([]map[string]any); len(devs) != 0 {
		t.Fatalf("want 0 devices, got %d", len(devs))
	}
}

func TestRSSIInfoDomain_PerDeviceRSSI(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)

	// Device WITH RSSI (HmIP — proves the per-device path is interface-agnostic).
	withRSSI := device.New(device.Config{
		Address:     "HMIP001",
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Model:       "HmIP-STHD",
		Name:        "Bedroom Sensor",
	})
	putRSSIDevice(t, withRSSI, hmenum.ParameterRSSIDevice, -65)
	putRSSIDevice(t, withRSSI, hmenum.ParameterRSSIPeer, -70)
	c.ModelRegistry.Put(withRSSI)

	// Device WITHOUT any RSSI reading — must be skipped.
	noRSSI := device.New(device.Config{
		Address:     "WIRED001",
		InterfaceID: "BidCos-Wired",
		Interface:   hmenum.InterfaceBidCosWired,
		Model:       "HMW-X",
		Name:        "Wired Switch",
	})
	c.ModelRegistry.Put(noRSSI)

	out, err := NewRSSIInfoDomain(reg).RSSIInfo(context.Background())
	if err != nil {
		t.Fatalf("RSSIInfo: %v", err)
	}
	devs := out["devices"].([]map[string]any)
	if len(devs) != 1 {
		t.Fatalf("want exactly 1 device (the one with RSSI), got %d", len(devs))
	}
	d := devs[0]
	if d["address"] != "HMIP001" || d["name"] != "Bedroom Sensor" {
		t.Errorf("identity wrong: %v / %v", d["address"], d["name"])
	}
	if d["interface_id"] != "HmIP-RF" || d["central"] != "ccu-01" {
		t.Errorf("scoping wrong: iface=%v central=%v", d["interface_id"], d["central"])
	}
	if d["rssi_device"] != -65 {
		t.Errorf("rssi_device = %v, want -65", d["rssi_device"])
	}
	if d["rssi_peer"] != -70 {
		t.Errorf("rssi_peer = %v, want -70", d["rssi_peer"])
	}
	if d["reachable"] != true {
		t.Errorf("reachable = %v, want true", d["reachable"])
	}
}

func TestRSSIInfoDomain_PeerOnlyStillIncluded(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)

	dev := device.New(device.Config{
		Address:     "DEV002",
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Model:       "HM-X",
		Name:        "Hallway",
	})
	putRSSIDevice(t, dev, hmenum.ParameterRSSIPeer, -80) // only peer, no device RSSI
	c.ModelRegistry.Put(dev)

	out, _ := NewRSSIInfoDomain(reg).RSSIInfo(context.Background())
	devs := out["devices"].([]map[string]any)
	if len(devs) != 1 {
		t.Fatalf("want 1 device (peer-only counts), got %d", len(devs))
	}
	if devs[0]["rssi_device"] != nil {
		t.Errorf("rssi_device = %v, want nil", devs[0]["rssi_device"])
	}
	if devs[0]["rssi_peer"] != -80 {
		t.Errorf("rssi_peer = %v, want -80", devs[0]["rssi_peer"])
	}
}
