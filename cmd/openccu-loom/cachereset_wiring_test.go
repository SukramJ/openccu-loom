// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCacheResetTopologySeesRuntimeAdoptedCentral pins that the cache-reset
// topology reports a CCU the operator adopted at runtime.
//
// cfg.Centrals is materialised once, when the config is loaded, and the adopt
// path deliberately never appends to it — the adopted CCU lives in the
// registry and in SQLite. A topology that reads only cfg.Centrals therefore
// expands a scope to zero units for that CCU, and cachereset.Service.Clear
// loops over an empty set: no cache is cleared, no bring-up is re-inited, and
// the handler still answers HTTP 200 with an all-zero report. The operator's
// primary recovery action silently does nothing until the daemon restarts.
func TestCacheResetTopologySeesRuntimeAdoptedCentral(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Centrals: []config.CentralConfig{{
			Name:       "ccu-boot",
			Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}, {Name: "BidCos-RF"}},
		}},
	}

	reg := central.NewRegistry()
	adopted, err := central.New(central.Config{Name: "ccu-adopted"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	adopted.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "ccu-adopted-HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
	})
	if err := reg.Register(adopted); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	topo := daemonTopology{cfg: cfg, reg: reg}

	centrals := topo.Centrals()
	if !slices.Contains(centrals, "ccu-adopted") {
		t.Errorf("runtime-adopted central missing from topology: %v", centrals)
	}
	if !slices.Contains(centrals, "ccu-boot") {
		t.Errorf("boot-configured central missing from topology: %v", centrals)
	}

	if ifaces := topo.Interfaces("ccu-adopted"); !slices.Contains(ifaces, "HmIP-RF") {
		t.Errorf("runtime-adopted central's interfaces missing: %v", ifaces)
	}
	// A boot-configured central keeps reporting its configured interfaces even
	// when no client has come up yet — otherwise a cache reset issued while the
	// CCU is unreachable would clear nothing.
	ifaces := topo.Interfaces("ccu-boot")
	if !slices.Contains(ifaces, "HmIP-RF") || !slices.Contains(ifaces, "BidCos-RF") {
		t.Errorf("boot-configured interfaces missing: %v", ifaces)
	}
}

// TestCacheResetTopologyDeduplicatesInterfaces guards that a central present
// in both config and registry does not report an interface twice, which would
// make the service clear the same unit twice.
func TestCacheResetTopologyDeduplicatesInterfaces(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Centrals: []config.CentralConfig{{
			Name:       "ccu1",
			Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
		}},
	}
	reg := central.NewRegistry()
	u, err := central.New(central.Config{Name: "ccu1"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	u.Clients.Register(&coordinators.ClientEntry{
		InterfaceID: "ccu1-HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
	})
	if err := reg.Register(u); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	topo := daemonTopology{cfg: cfg, reg: reg}

	if got := topo.Centrals(); len(got) != 1 {
		t.Errorf("central listed twice: %v", got)
	}
	if got := topo.Interfaces("ccu1"); len(got) != 1 {
		t.Errorf("interface listed twice: %v", got)
	}
}
