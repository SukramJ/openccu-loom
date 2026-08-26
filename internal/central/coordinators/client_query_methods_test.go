// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// client_query_methods_test.go covers ClientCoordinator query methods:
// HasClient, HasClients, InterfaceIDs, and Interfaces.
package coordinators

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestClientCoordinatorHasClient(t *testing.T) {
	c := NewClientCoordinator()
	if c.HasClient("HmIP-RF.local") {
		t.Fatal("HasClient must return false for unregistered ID")
	}
	entry := &ClientEntry{
		InterfaceID: "HmIP-RF.local",
		Interface:   hmenum.InterfaceHmIPRF,
	}
	_ = c.Register(entry)
	if !c.HasClient("HmIP-RF.local") {
		t.Fatal("HasClient must return true after Register")
	}
	c.Remove("HmIP-RF.local")
	if c.HasClient("HmIP-RF.local") {
		t.Fatal("HasClient must return false after Remove")
	}
}

func TestClientCoordinatorHasClients(t *testing.T) {
	c := NewClientCoordinator()
	if c.HasClients() {
		t.Fatal("HasClients must return false on empty coordinator")
	}
	entry := &ClientEntry{InterfaceID: "HmIP-RF.local", Interface: hmenum.InterfaceHmIPRF}
	_ = c.Register(entry)
	if !c.HasClients() {
		t.Fatal("HasClients must return true after first Register")
	}
	c.Remove("HmIP-RF.local")
	if c.HasClients() {
		t.Fatal("HasClients must return false after all clients removed")
	}
}

func TestClientCoordinatorInterfaceIDs(t *testing.T) {
	c := NewClientCoordinator()
	if ids := c.InterfaceIDs(); len(ids) != 0 {
		t.Fatalf("InterfaceIDs on empty coordinator = %v, want empty", ids)
	}
	_ = c.Register(&ClientEntry{InterfaceID: "Z.local", Interface: hmenum.InterfaceHmIPRF})
	_ = c.Register(&ClientEntry{InterfaceID: "A.local", Interface: hmenum.InterfaceBidCosRF})

	ids := c.InterfaceIDs()
	if len(ids) != 2 {
		t.Fatalf("InterfaceIDs()=%v, want 2", ids)
	}
	if ids[0] != "A.local" || ids[1] != "Z.local" {
		t.Fatalf("InterfaceIDs()=%v, want [A.local, Z.local]", ids)
	}
}

func TestClientCoordinatorInterfaces(t *testing.T) {
	c := NewClientCoordinator()
	if ifaces := c.Interfaces(); len(ifaces) != 0 {
		t.Fatalf("Interfaces on empty coordinator = %v, want empty", ifaces)
	}
	_ = c.Register(&ClientEntry{InterfaceID: "hmip1", Interface: hmenum.InterfaceHmIPRF})
	_ = c.Register(&ClientEntry{InterfaceID: "hmip2", Interface: hmenum.InterfaceHmIPRF}) // duplicate interface
	_ = c.Register(&ClientEntry{InterfaceID: "bidcos1", Interface: hmenum.InterfaceBidCosRF})

	ifaces := c.Interfaces()
	if len(ifaces) != 2 {
		t.Fatalf("Interfaces()=%v, want 2 unique values", ifaces)
	}
	// BidCosRF < HmIPRF numerically.
	if ifaces[0] != hmenum.InterfaceBidCosRF || ifaces[1] != hmenum.InterfaceHmIPRF {
		t.Fatalf("Interfaces()=%v, want [BidCosRF, HmIPRF]", ifaces)
	}
}
