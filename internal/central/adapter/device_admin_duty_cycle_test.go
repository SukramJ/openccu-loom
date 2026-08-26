// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// buildDutyCycleFixture wires a central with one BidCos-RF device seeded
// into the model registry and returns the domain plus the central so the
// caller can populate the hub's BidCos utilisation cache.
func buildDutyCycleFixture(t *testing.T) (*DeviceAdminDomain, *central.Unit) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	c.ModelRegistry.Put(device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "LEQ0012345",
		Model:       "HM-LC-Sw1-Pl",
		Name:        "Lamp",
	}))
	return NewDeviceAdminDomain(reg, client.NewValueWriter()), c
}

func TestDeviceAdminInterfaceDutyCycleKnown(t *testing.T) {
	t.Parallel()
	dom, c := buildDutyCycleFixture(t)
	c.Hub.SetBidcosInterfaces(map[string]coordinators.BidcosInterfaceInfo{
		"BidCos-RF": {Address: "OEQ1234567", DutyCycle: 85, CarrierSense: 12, Connected: true},
	})
	dc, ok := dom.InterfaceDutyCycle("LEQ0012345")
	if !ok || dc != 85 {
		t.Fatalf("expected (85, true), got (%d, %v)", dc, ok)
	}
}

func TestDeviceAdminInterfaceDutyCycleNoCacheEntry(t *testing.T) {
	t.Parallel()
	dom, _ := buildDutyCycleFixture(t)
	// No SetBidcosInterfaces call: HmIP-style interfaces and un-polled
	// BidCos interfaces have no cache entry.
	if dc, ok := dom.InterfaceDutyCycle("LEQ0012345"); ok {
		t.Fatalf("expected unknown, got (%d, %v)", dc, ok)
	}
}

func TestDeviceAdminInterfaceDutyCycleUnreportedValue(t *testing.T) {
	t.Parallel()
	dom, c := buildDutyCycleFixture(t)
	// A cached entry whose DutyCycle is the -1 "unreported" sentinel is
	// treated as unknown, not as a zero duty cycle.
	c.Hub.SetBidcosInterfaces(map[string]coordinators.BidcosInterfaceInfo{
		"BidCos-RF": {Address: "OEQ1234567", DutyCycle: -1, CarrierSense: -1},
	})
	if dc, ok := dom.InterfaceDutyCycle("LEQ0012345"); ok {
		t.Fatalf("expected unknown for -1 sentinel, got (%d, %v)", dc, ok)
	}
}

func TestDeviceAdminInterfaceDutyCycleUnknownDevice(t *testing.T) {
	t.Parallel()
	dom, c := buildDutyCycleFixture(t)
	c.Hub.SetBidcosInterfaces(map[string]coordinators.BidcosInterfaceInfo{
		"BidCos-RF": {Address: "OEQ1234567", DutyCycle: 85},
	})
	if dc, ok := dom.InterfaceDutyCycle("DOES-NOT-EXIST"); ok {
		t.Fatalf("expected unknown for missing device, got (%d, %v)", dc, ok)
	}
}

func TestDeviceAdminInterfaceDutyCycleNilRegistry(t *testing.T) {
	t.Parallel()
	dom := NewDeviceAdminDomain(nil, client.NewValueWriter())
	if dc, ok := dom.InterfaceDutyCycle("LEQ0012345"); ok {
		t.Fatalf("expected unknown for nil registry, got (%d, %v)", dc, ok)
	}
}

func TestDeviceAdminInterfaceDutyCycleNilHub(t *testing.T) {
	t.Parallel()
	dom, c := buildDutyCycleFixture(t)
	// A central without a hub coordinator (not reachable via central.New,
	// but the field is a plain pointer) must report unknown rather than
	// panic on the nil dereference.
	c.Hub = nil
	if dc, ok := dom.InterfaceDutyCycle("LEQ0012345"); ok {
		t.Fatalf("expected unknown for nil hub, got (%d, %v)", dc, ok)
	}
}
