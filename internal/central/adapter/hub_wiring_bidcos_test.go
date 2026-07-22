// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// hub_wiring_bidcos_test.go covers loadBidcosInterfaces and its gateway
// aggregation: the periodic BidCos radio-utilisation poll that fills the
// HubCoordinator's per-interface duty-cycle / carrier-sense cache. It uses a
// fake lister (no CCU) and asserts interface selection, aggregation, cache
// population, and error propagation.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// fakeBidcosLister is a test double for bidcosInterfaceLister. It records the
// interface ids requested and replies per interface from a canned map.
type fakeBidcosLister struct {
	replies   map[string][]jsonrpc.BidcosInterface
	err       error
	requested []string
}

func (f *fakeBidcosLister) ListBidcosInterfaces(_ context.Context, iface string) ([]jsonrpc.BidcosInterface, error) {
	f.requested = append(f.requested, iface)
	if f.err != nil {
		return nil, f.err
	}
	return f.replies[iface], nil
}

func newBidcosTestUnit(t *testing.T, ifaces ...hmenum.Interface) *central.Unit {
	t.Helper()
	cc := coordinators.NewClientCoordinator()
	for _, iface := range ifaces {
		if err := cc.Register(&coordinators.ClientEntry{
			InterfaceID: string(iface),
			Interface:   iface,
		}); err != nil {
			t.Fatalf("register %s: %v", iface, err)
		}
	}
	return &central.Unit{
		Clients: cc,
		Hub:     coordinators.NewHubCoordinator("c", events.NewBus()),
	}
}

// TestLoadBidcosInterfacesPopulatesCache verifies that a BidCos-RF interface's
// gateways are aggregated and stored in the HubCoordinator cache while a
// non-BidCos interface is skipped.
func TestLoadBidcosInterfacesPopulatesCache(t *testing.T) {
	t.Parallel()
	unit := newBidcosTestUnit(t, hmenum.InterfaceBidCosRF, hmenum.InterfaceHmIPRF)
	lister := &fakeBidcosLister{
		replies: map[string][]jsonrpc.BidcosInterface{
			"BidCos-RF": {
				{Address: "OEQ1234567", Type: "CCU2", DutyCycle: 42, CarrierSense: -1, Connected: true, Default: true},
			},
		},
	}

	if err := loadBidcosInterfaces(context.Background(), lister, unit); err != nil {
		t.Fatalf("loadBidcosInterfaces: %v", err)
	}

	// Only the BidCos-RF interface should have been queried.
	if len(lister.requested) != 1 || lister.requested[0] != "BidCos-RF" {
		t.Fatalf("requested = %v, want [BidCos-RF]", lister.requested)
	}

	info, ok := unit.Hub.BidcosInterface("BidCos-RF")
	if !ok {
		t.Fatal("expected BidCos-RF snapshot in cache")
	}
	if info.DutyCycle != 42 || info.Address != "OEQ1234567" || !info.Connected {
		t.Fatalf("info = %+v", info)
	}

	// HmIP-RF is never polled and stays absent.
	if _, ok := unit.Hub.BidcosInterface("HmIP-RF"); ok {
		t.Fatal("HmIP-RF must have no snapshot")
	}
}

// TestLoadBidcosInterfacesPrefersDefaultGateway verifies the aggregation rule:
// the default gateway wins even when another gateway shows a higher duty cycle.
func TestLoadBidcosInterfacesPrefersDefaultGateway(t *testing.T) {
	t.Parallel()
	unit := newBidcosTestUnit(t, hmenum.InterfaceBidCosRF)
	lister := &fakeBidcosLister{
		replies: map[string][]jsonrpc.BidcosInterface{
			"BidCos-RF": {
				{Address: "LGW", Type: "HM-LGW", DutyCycle: 90, Default: false},
				{Address: "CCU", Type: "CCU2", DutyCycle: 30, Default: true},
			},
		},
	}
	if err := loadBidcosInterfaces(context.Background(), lister, unit); err != nil {
		t.Fatalf("loadBidcosInterfaces: %v", err)
	}
	info, ok := unit.Hub.BidcosInterface("BidCos-RF")
	if !ok {
		t.Fatal("expected snapshot")
	}
	if info.Address != "CCU" || info.DutyCycle != 30 {
		t.Fatalf("info = %+v, want the default CCU gateway", info)
	}
}

// TestLoadBidcosInterfacesMaxWhenNoDefault verifies that, absent a default
// gateway, the highest duty cycle is chosen (worst-case for the warning badge).
func TestLoadBidcosInterfacesMaxWhenNoDefault(t *testing.T) {
	t.Parallel()
	unit := newBidcosTestUnit(t, hmenum.InterfaceBidCosRF)
	lister := &fakeBidcosLister{
		replies: map[string][]jsonrpc.BidcosInterface{
			"BidCos-RF": {
				{Address: "A", DutyCycle: 12},
				{Address: "B", DutyCycle: 55},
				{Address: "C", DutyCycle: 33},
			},
		},
	}
	if err := loadBidcosInterfaces(context.Background(), lister, unit); err != nil {
		t.Fatalf("loadBidcosInterfaces: %v", err)
	}
	info, _ := unit.Hub.BidcosInterface("BidCos-RF")
	if info.Address != "B" || info.DutyCycle != 55 {
		t.Fatalf("info = %+v, want the max-duty gateway B", info)
	}
}

// TestLoadBidcosInterfacesErrorPropagates verifies that a lister error is
// surfaced and the cache is left empty for that interface.
func TestLoadBidcosInterfacesErrorPropagates(t *testing.T) {
	t.Parallel()
	unit := newBidcosTestUnit(t, hmenum.InterfaceBidCosRF)
	lister := &fakeBidcosLister{err: errors.New("boom")}
	if err := loadBidcosInterfaces(context.Background(), lister, unit); err == nil {
		t.Fatal("expected error to propagate")
	}
	if _, ok := unit.Hub.BidcosInterface("BidCos-RF"); ok {
		t.Fatal("cache must stay empty when the poll failed")
	}
}

// TestLoadBidcosInterfacesNoBidcosInterfacePresent verifies that a central
// with only HmIP interfaces never calls the lister and leaves the cache
// empty without error — the common pure-HmIP deployment shape.
func TestLoadBidcosInterfacesNoBidcosInterfacePresent(t *testing.T) {
	t.Parallel()
	unit := newBidcosTestUnit(t, hmenum.InterfaceHmIPRF)
	lister := &fakeBidcosLister{}
	if err := loadBidcosInterfaces(context.Background(), lister, unit); err != nil {
		t.Fatalf("loadBidcosInterfaces: %v", err)
	}
	if len(lister.requested) != 0 {
		t.Fatalf("requested = %v, want no calls for a pure-HmIP central", lister.requested)
	}
	if _, ok := unit.Hub.BidcosInterface("HmIP-RF"); ok {
		t.Fatal("HmIP-RF must have no snapshot")
	}
}

// TestLoadBidcosInterfacesEmptyGatewayListSkipsCache verifies that a
// successful poll reporting zero gateways leaves that interface absent from
// the cache (rather than storing a zero-value snapshot), and clears any
// stale entry from a previous poll.
func TestLoadBidcosInterfacesEmptyGatewayListSkipsCache(t *testing.T) {
	t.Parallel()
	unit := newBidcosTestUnit(t, hmenum.InterfaceBidCosRF)
	unit.Hub.SetBidcosInterfaces(map[string]coordinators.BidcosInterfaceInfo{
		"BidCos-RF": {Address: "STALE", DutyCycle: 50},
	})
	lister := &fakeBidcosLister{replies: map[string][]jsonrpc.BidcosInterface{
		"BidCos-RF": {},
	}}
	if err := loadBidcosInterfaces(context.Background(), lister, unit); err != nil {
		t.Fatalf("loadBidcosInterfaces: %v", err)
	}
	if _, ok := unit.Hub.BidcosInterface("BidCos-RF"); ok {
		t.Fatal("expected the stale snapshot to be cleared, not kept, on an empty gateway reply")
	}
}

// TestLoadBidcosInterfacesNilGuards verifies the no-op guards.
func TestLoadBidcosInterfacesNilGuards(t *testing.T) {
	t.Parallel()
	if err := loadBidcosInterfaces(context.Background(), nil, nil); err != nil {
		t.Fatalf("nil unit: %v", err)
	}
	unit := newBidcosTestUnit(t, hmenum.InterfaceBidCosRF)
	if err := loadBidcosInterfaces(context.Background(), nil, unit); err != nil {
		t.Fatalf("nil lister: %v", err)
	}
}

// TestAggregateBidcosGatewaysEmpty verifies the empty-list contract.
func TestAggregateBidcosGatewaysEmpty(t *testing.T) {
	t.Parallel()
	if _, ok := aggregateBidcosGateways(nil); ok {
		t.Fatal("expected ok=false for an empty gateway list")
	}
}
