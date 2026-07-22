// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// interfaces_test.go covers InterfacesAdapter's enrichment of InterfaceState
// with the HubCoordinator's cached BidCos duty-cycle / carrier-sense snapshot.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestInterfacesAdapterSurfacesDutyCycle verifies that a BidCos interface with
// a cached radio-utilisation snapshot exposes duty_cycle (including a
// legitimate 0) while an interface without a snapshot leaves the fields nil.
func TestInterfacesAdapterSurfacesDutyCycle(t *testing.T) {
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: string(hmenum.InterfaceBidCosRF),
		Interface:   hmenum.InterfaceBidCosRF,
	}); err != nil {
		t.Fatalf("register BidCos-RF: %v", err)
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: string(hmenum.InterfaceHmIPRF),
		Interface:   hmenum.InterfaceHmIPRF,
	}); err != nil {
		t.Fatalf("register HmIP-RF: %v", err)
	}
	c.Hub.SetBidcosInterfaces(map[string]coordinators.BidcosInterfaceInfo{
		"BidCos-RF": {Address: "OEQ1", DutyCycle: 0, CarrierSense: -1, Connected: true},
	})

	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register central: %v", err)
	}
	a := NewInterfacesAdapter(reg, nil)

	byID := map[string]struct{ duty, carrier *int }{}
	for _, s := range a.Interfaces() {
		byID[s.ID] = struct{ duty, carrier *int }{s.DutyCycle, s.CarrierSense}
	}

	bidcos, ok := byID["BidCos-RF"]
	if !ok {
		t.Fatal("BidCos-RF missing from adapter output")
	}
	if bidcos.duty == nil || *bidcos.duty != 0 {
		t.Fatalf("BidCos-RF duty_cycle = %v, want 0 (present)", bidcos.duty)
	}
	// carrier_sense was -1 (unknown) → must stay nil.
	if bidcos.carrier != nil {
		t.Fatalf("BidCos-RF carrier_sense = %v, want nil", *bidcos.carrier)
	}

	hmip, ok := byID["HmIP-RF"]
	if !ok {
		t.Fatal("HmIP-RF missing from adapter output")
	}
	if hmip.duty != nil || hmip.carrier != nil {
		t.Fatalf("HmIP-RF should carry no radio-utilisation fields, got duty=%v carrier=%v", hmip.duty, hmip.carrier)
	}
}

// TestInterfacesAdapterSurfacesCarrierSenseWhenPresent verifies that a
// present (non-negative) CarrierSense value is surfaced as carrier_sense —
// the counterpart to TestInterfacesAdapterSurfacesDutyCycle, which only
// exercises the -1 (unknown → nil) branch.
func TestInterfacesAdapterSurfacesCarrierSenseWhenPresent(t *testing.T) {
	c, err := central.New(central.Config{Name: "ccu-02"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: string(hmenum.InterfaceBidCosRF),
		Interface:   hmenum.InterfaceBidCosRF,
	}); err != nil {
		t.Fatalf("register BidCos-RF: %v", err)
	}
	c.Hub.SetBidcosInterfaces(map[string]coordinators.BidcosInterfaceInfo{
		"BidCos-RF": {Address: "OEQ1", DutyCycle: 42, CarrierSense: 17, Connected: true},
	})

	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register central: %v", err)
	}
	a := NewInterfacesAdapter(reg, nil)

	state, ok := a.Interface("BidCos-RF")
	if !ok {
		t.Fatal("BidCos-RF missing from adapter output")
	}
	if state.CarrierSense == nil || *state.CarrierSense != 17 {
		t.Fatalf("carrier_sense = %v, want 17 (present)", state.CarrierSense)
	}
	if state.DutyCycle == nil || *state.DutyCycle != 42 {
		t.Fatalf("duty_cycle = %v, want 42", state.DutyCycle)
	}
}

// TestInterfacesAdapterNilHubSkipsEnrichment verifies that a unit with no
// HubCoordinator (Hub == nil) is handled defensively: Interfaces() returns
// the base interface state without radio-utilisation enrichment instead of
// panicking.
func TestInterfacesAdapterNilHubSkipsEnrichment(t *testing.T) {
	c, err := central.New(central.Config{Name: "ccu-03"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	if err := c.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: string(hmenum.InterfaceBidCosRF),
		Interface:   hmenum.InterfaceBidCosRF,
	}); err != nil {
		t.Fatalf("register BidCos-RF: %v", err)
	}
	c.Hub = nil

	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register central: %v", err)
	}
	a := NewInterfacesAdapter(reg, nil)

	state, ok := a.Interface("BidCos-RF")
	if !ok {
		t.Fatal("BidCos-RF missing from adapter output")
	}
	if state.DutyCycle != nil || state.CarrierSense != nil {
		t.Fatalf("expected no radio-utilisation fields with a nil Hub, got duty=%v carrier=%v", state.DutyCycle, state.CarrierSense)
	}
}
