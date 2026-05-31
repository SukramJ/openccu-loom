// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ============================================================
// isCentralLinkInterface tests (pure logic, no deps)
// ============================================================

func TestIsCentralLinkInterfaceEligible(t *testing.T) {
	t.Parallel()
	eligible := []hmenum.Interface{
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
		hmenum.InterfaceHmIPRF,
	}
	for _, iface := range eligible {
		if !isCentralLinkInterface(iface) {
			t.Errorf("isCentralLinkInterface(%v) = false, want true", iface)
		}
	}
}

func TestIsCentralLinkInterfaceIneligible(t *testing.T) {
	t.Parallel()
	ineligible := []hmenum.Interface{
		hmenum.InterfaceVirtualDevices,
		hmenum.InterfaceCUxD,
	}
	for _, iface := range ineligible {
		if isCentralLinkInterface(iface) {
			t.Errorf("isCentralLinkInterface(%v) = true, want false", iface)
		}
	}
}

// ============================================================
// channelHasPressEvents tests
// ============================================================

func TestChannelHasPressEventsNilChannel(t *testing.T) {
	t.Parallel()
	if channelHasPressEvents(nil) {
		t.Error("nil channel must return false")
	}
}

func TestChannelHasPressEventsWithPressShort(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV001", InterfaceID: "BidCos-RF"})
	ch := dev.AddChannel("DEV001:1", 1, "KEY", hmenum.ParamsetKeyValues)
	pressDP := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsEvent,
		},
	})
	ch.Put(pressDP)
	if !channelHasPressEvents(ch) {
		t.Error("channel with PRESS_SHORT must return true")
	}
}

func TestChannelHasPressEventsWithPressLong(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV002", InterfaceID: "BidCos-RF"})
	ch := dev.AddChannel("DEV002:1", 1, "KEY", hmenum.ParamsetKeyValues)
	pressDP := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV002:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressLong),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsEvent,
		},
	})
	ch.Put(pressDP)
	if !channelHasPressEvents(ch) {
		t.Error("channel with PRESS_LONG must return true")
	}
}

func TestChannelHasPressEventsWithoutPressParams(t *testing.T) {
	t.Parallel()
	dev := device.New(device.Config{Address: "DEV003", InterfaceID: "BidCos-RF"})
	ch := dev.AddChannel("DEV003:1", 1, "TEMP", hmenum.ParamsetKeyValues)
	// Add a non-press parameter.
	stateDP := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "DEV003:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "TEMPERATURE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(stateDP)
	if channelHasPressEvents(ch) {
		t.Error("channel without press parameters must return false")
	}
}

// ============================================================
// CentralLinksDomain.CentralLinksStatus tests
// ============================================================

// TestCentralLinksStatusNilRegistryError verifies that a nil registry
// returns ErrNoCentralLinkBackend.
func TestCentralLinksStatusNilRegistryError(t *testing.T) {
	t.Parallel()
	d := NewCentralLinksDomain(nil, nil)
	_, err := d.CentralLinksStatus("DEV001")
	if !errors.Is(err, ErrNoCentralLinkBackend) {
		t.Fatalf("err = %v, want ErrNoCentralLinkBackend", err)
	}
}

// TestCentralLinksStatusDeviceNotFound verifies that a missing device
// returns ErrNoCentralLinkBackend.
func TestCentralLinksStatusDeviceNotFound(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)

	d := NewCentralLinksDomain(reg, nil)
	_, sErr := d.CentralLinksStatus("MISSING_DEV")
	if !errors.Is(sErr, ErrNoCentralLinkBackend) {
		t.Fatalf("err = %v, want ErrNoCentralLinkBackend", sErr)
	}
}

// TestCentralLinksStatusUnsupportedInterface verifies that a device on
// an unsupported interface reports Supported=false.
func TestCentralLinksStatusUnsupportedInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)

	dev := device.New(device.Config{
		InterfaceID: "CUxD",
		Interface:   hmenum.InterfaceCUxD,
		Address:     "CUX0001",
		Model:       "CUX-Model",
	})
	c.ModelRegistry.Put(dev)

	d := NewCentralLinksDomain(reg, nil)
	status, sErr := d.CentralLinksStatus("CUX0001")
	if sErr != nil {
		t.Fatalf("CentralLinksStatus: %v", sErr)
	}
	if status.Supported {
		t.Error("CUxD device must not be supported for central links")
	}
	if status.Reason != "interface_unsupported" {
		t.Errorf("Reason = %q, want interface_unsupported", status.Reason)
	}
}

// TestCentralLinksStatusSupportedDevice verifies that a device on a
// supported interface with press-capable channels reports correctly.
func TestCentralLinksStatusSupportedDevice(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)

	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "BRF0001",
		Model:       "HM-RC-4",
	})
	// Add a channel with PRESS_SHORT.
	ch := dev.AddChannel("BRF0001:1", 1, "KEY", hmenum.ParamsetKeyValues)
	pressDP := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: "BRF0001:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsEvent,
		},
	})
	ch.Put(pressDP)
	c.ModelRegistry.Put(dev)

	d := NewCentralLinksDomain(reg, nil)
	status, sErr := d.CentralLinksStatus("BRF0001")
	if sErr != nil {
		t.Fatalf("CentralLinksStatus: %v", sErr)
	}
	if !status.Supported {
		t.Error("BidCos-RF device must be supported for central links")
	}
	if status.EligibleChannels != 1 {
		t.Errorf("EligibleChannels = %d, want 1", status.EligibleChannels)
	}
}

// ============================================================
// CentralLinksDomain.CreateCentralLinks / RemoveCentralLinks tests
// ============================================================

// TestCentralLinksCreateNilRegistryError verifies nil registry returns error.
func TestCentralLinksCreateNilRegistryError(t *testing.T) {
	t.Parallel()
	d := NewCentralLinksDomain(nil, nil)
	_, err := d.CreateCentralLinks(context.Background(), "DEV001")
	if !errors.Is(err, ErrNoCentralLinkBackend) {
		t.Fatalf("err = %v, want ErrNoCentralLinkBackend", err)
	}
}

// TestCentralLinksRemoveNilRegistryError verifies nil registry returns error.
func TestCentralLinksRemoveNilRegistryError(t *testing.T) {
	t.Parallel()
	d := NewCentralLinksDomain(nil, nil)
	_, err := d.RemoveCentralLinks(context.Background(), "DEV001")
	if !errors.Is(err, ErrNoCentralLinkBackend) {
		t.Fatalf("err = %v, want ErrNoCentralLinkBackend", err)
	}
}

// TestCentralLinksCreateDeviceNotFound verifies missing device error.
func TestCentralLinksCreateDeviceNotFound(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	d := NewCentralLinksDomain(reg, nil)
	_, sErr := d.CreateCentralLinks(context.Background(), "MISSING")
	if !errors.Is(sErr, ErrNoCentralLinkBackend) {
		t.Fatalf("err = %v, want ErrNoCentralLinkBackend", sErr)
	}
}
