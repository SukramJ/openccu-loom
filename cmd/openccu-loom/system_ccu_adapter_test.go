// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestSystemCCUAdapterList_PopulatesReadinessFromUnit verifies that List
// stamps entry.Readiness from the registered unit's Readiness() view plus
// IsSouthboundReady, not from the config resolver — the readiness fields
// reflect live bring-up state, which the static config never carries.
func TestSystemCCUAdapterList_PopulatesReadinessFromUnit(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "ccu-alpha"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}

	unit.SetReadiness(hmenum.ReadinessLoadingDevices, 2, 4)

	a := newSystemCCUAdapter(reg, nil)
	entries := a.List(context.Background())
	if len(entries) != 1 {
		t.Fatalf("List() returned %d entries, want 1", len(entries))
	}

	got := entries[0].Readiness
	if got.Phase != string(hmenum.ReadinessLoadingDevices) {
		t.Errorf("Readiness.Phase = %q, want %q", got.Phase, hmenum.ReadinessLoadingDevices)
	}
	if got.Ready {
		t.Error("Readiness.Ready = true while phase is loading_devices, want false")
	}
	if got.InterfacesLoaded != 2 {
		t.Errorf("Readiness.InterfacesLoaded = %d, want 2", got.InterfacesLoaded)
	}
	if got.InterfacesTotal != 4 {
		t.Errorf("Readiness.InterfacesTotal = %d, want 4", got.InterfacesTotal)
	}
}

// TestSystemCCUAdapterList_ReadyMatchesSouthboundLatch verifies that once
// MarkSouthboundReady has latched the central ready, entry.Readiness.Ready
// reflects that latch even if the caller never explicitly stamped the
// "ready" phase via SetReadiness — Ready is sourced from
// IsSouthboundReady(), not from Readiness().Phase.
func TestSystemCCUAdapterList_ReadyMatchesSouthboundLatch(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "ccu-beta"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}

	unit.MarkSouthboundReady()

	a := newSystemCCUAdapter(reg, nil)
	entries := a.List(context.Background())
	if len(entries) != 1 {
		t.Fatalf("List() returned %d entries, want 1", len(entries))
	}
	if !entries[0].Readiness.Ready {
		t.Error("Readiness.Ready = false after MarkSouthboundReady, want true")
	}
}

// TestSystemCCUAdapterList_UnknownPhaseOnFreshUnit verifies that a freshly
// registered unit (no SetReadiness call yet) reports the normalized
// "unknown" phase rather than an empty string, so the SPA never has to
// special-case the zero value.
func TestSystemCCUAdapterList_UnknownPhaseOnFreshUnit(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "ccu-gamma"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}

	a := newSystemCCUAdapter(reg, nil)
	entries := a.List(context.Background())
	if len(entries) != 1 {
		t.Fatalf("List() returned %d entries, want 1", len(entries))
	}
	if got := entries[0].Readiness.Phase; got != string(hmenum.ReadinessUnknown) {
		t.Errorf("Readiness.Phase on a fresh unit = %q, want %q", got, hmenum.ReadinessUnknown)
	}
	if entries[0].Readiness.Ready {
		t.Error("Readiness.Ready = true on a fresh unit, want false")
	}
}

// TestSystemCCUAdapterList_NilRegistryReturnsNil verifies the documented
// nil-registry guard remains intact alongside the new readiness field.
func TestSystemCCUAdapterList_NilRegistryReturnsNil(t *testing.T) {
	t.Parallel()

	a := newSystemCCUAdapter(nil, nil)
	if got := a.List(context.Background()); got != nil {
		t.Errorf("List() with nil registry = %v, want nil", got)
	}
}

// TestSystemCCUAdapterList_ResolverStillPopulatesConfigFields verifies that,
// alongside the new readiness field, the pre-existing config-resolved fields
// (host, configured interfaces) still populate from the resolver.
func TestSystemCCUAdapterList_ResolverStillPopulatesConfigFields(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "ccu-delta"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}

	resolve := func(_ context.Context, name string) (config.CentralConfig, bool) {
		if name != "ccu-delta" {
			return config.CentralConfig{}, false
		}
		return config.CentralConfig{
			Name: name,
			Host: "10.0.0.5",
			Interfaces: []config.InterfaceSpec{
				{Name: "HmIP-RF"},
				{Name: "BidCos-RF"},
			},
		}, true
	}

	a := newSystemCCUAdapter(reg, resolve)
	entries := a.List(context.Background())
	if len(entries) != 1 {
		t.Fatalf("List() returned %d entries, want 1", len(entries))
	}
	got := entries[0]
	if got.Host != "10.0.0.5" {
		t.Errorf("Host = %q, want %q", got.Host, "10.0.0.5")
	}
	want := []string{"HmIP-RF", "BidCos-RF"}
	if len(got.ConfiguredInterfaces) != len(want) {
		t.Fatalf("ConfiguredInterfaces = %v, want %v", got.ConfiguredInterfaces, want)
	}
	for i, v := range want {
		if got.ConfiguredInterfaces[i] != v {
			t.Errorf("ConfiguredInterfaces[%d] = %q, want %q", i, got.ConfiguredInterfaces[i], v)
		}
	}
}

// TestSystemCCUAdapterList_MapsCCUReportedFacts verifies that List carries
// the CCU's own view northbound: the two security flags from SystemInfo and
// the interface list the CCU reports for itself. The CCU-reported list is
// independent of ConfiguredInterfaces — an interface present in one and not
// the other is exactly the mismatch the fleet view highlights.
func TestSystemCCUAdapterList_MapsCCUReportedFacts(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "ccu-echo"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}
	unit.SetSystemInformation(central.SystemInfo{
		Model:                "HmIP-CCU3",
		AuthEnabled:          true,
		HTTPSRedirectEnabled: true,
	})
	unit.SetCCUInterfaces([]central.CCUInterface{
		{Type: "HmIP-RF", Address: "HmIP-RF", Port: 2010, URL: "http://127.0.0.1:2010"},
		// Reported by the CCU but absent from the configured list below.
		{Type: "CUxD", Address: "CUxD", Port: 8701},
	})

	resolve := func(_ context.Context, name string) (config.CentralConfig, bool) {
		return config.CentralConfig{
			Name:       name,
			Host:       "10.0.0.9",
			Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
		}, true
	}

	entries := newSystemCCUAdapter(reg, resolve).List(context.Background())
	if len(entries) != 1 {
		t.Fatalf("List() returned %d entries, want 1", len(entries))
	}
	got := entries[0]
	if !got.AuthEnabled {
		t.Error("AuthEnabled = false, want true")
	}
	if !got.HTTPSRedirectEnabled {
		t.Error("HTTPSRedirectEnabled = false, want true")
	}
	if len(got.CCUInterfaces) != 2 {
		t.Fatalf("CCUInterfaces = %+v, want 2 entries", got.CCUInterfaces)
	}
	if got.CCUInterfaces[0].Address != "HmIP-RF" || got.CCUInterfaces[0].Port != 2010 {
		t.Errorf("CCUInterfaces[0] = %+v", got.CCUInterfaces[0])
	}
	if got.CCUInterfaces[0].URL != "http://127.0.0.1:2010" {
		t.Errorf("CCUInterfaces[0].URL = %q", got.CCUInterfaces[0].URL)
	}
	if got.CCUInterfaces[1].Address != "CUxD" {
		t.Errorf("CCUInterfaces[1] = %+v, want the CUxD entry", got.CCUInterfaces[1])
	}
	// The configured list stays the daemon's own view — the CCU-reported
	// CUxD entry must not leak into it.
	if len(got.ConfiguredInterfaces) != 1 || got.ConfiguredInterfaces[0] != "HmIP-RF" {
		t.Errorf("ConfiguredInterfaces = %v, want [HmIP-RF]", got.ConfiguredInterfaces)
	}
}

// TestSystemCCUAdapterList_OmitsCCUInterfacesBeforeFirstConnect pins that a
// central which has not completed a connect round emits no ccu_interfaces
// key at all (nil, not an empty array), so the SPA can tell "not discovered
// yet" from "the CCU reports no interfaces".
func TestSystemCCUAdapterList_OmitsCCUInterfacesBeforeFirstConnect(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: "ccu-foxtrot"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("Register: %v", err)
	}

	entries := newSystemCCUAdapter(reg, nil).List(context.Background())
	if len(entries) != 1 {
		t.Fatalf("List() returned %d entries, want 1", len(entries))
	}
	if entries[0].CCUInterfaces != nil {
		t.Errorf("CCUInterfaces = %+v, want nil before the first connect", entries[0].CCUInterfaces)
	}
	if entries[0].AuthEnabled || entries[0].HTTPSRedirectEnabled {
		t.Error("security flags default to true, want false before the first connect")
	}
}
