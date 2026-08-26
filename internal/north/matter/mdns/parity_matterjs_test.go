// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Parity tests for mDNS goodbye behaviour against matter.js HEAD.
//
// matter.js reference:
//   packages/protocol/src/mdns/MdnsServer.ts::close — calls
//   this.#records.close() which sends TTL=0 DNS records (goodbye
//   multicast) before tearing down the server.
//
// openccu-loom mapping:
//   [Noop] advertiser implements Withdraw via in-memory delete only
//   (no multicast TTL=0 packets — Noop is test / boot-phase only).
//   [Zeroconf] calls grandcat/zeroconf's server.Shutdown(), which sends
//   mDNS goodbye packets (TTL=0) before dismantling the DNS responder.
//   The production goodbye contract is validated end-to-end via the
//   commissioning integration tests. This file covers the structural
//   contract that [Advertiser.Withdraw] removes the record from the
//   active set so subsequent [Advertiser.Active] no longer lists it —
//   the semantic invariant that any goodbye-capable advertiser must
//   satisfy.

package mdns_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
)

// TestParityMatterJS_Goodbye_WithdrawRemovesFromActive verifies that
// Withdraw removes the service from the active set.
//
// Mirrors matter.js MdnsServer.ts::close — after a goodbye the record
// MUST NOT be served any longer; Active() reflects this immediately.
func TestParityMatterJS_Goodbye_WithdrawRemovesFromActive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	adv := mdns.NewNoop()

	svc := buildGoodbyeTestService(t)
	if err := adv.Publish(ctx, svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	if len(adv.Active()) != 1 {
		t.Fatalf("Active() after Publish: len=%d, want 1", len(adv.Active()))
	}

	if err := adv.Withdraw(ctx, svc.InstanceName, svc.ServiceType); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}

	if len(adv.Active()) != 0 {
		t.Errorf("Active() after Withdraw: len=%d, want 0 (record must be gone after goodbye)", len(adv.Active()))
	}
}

// TestParityMatterJS_Goodbye_WithdrawUnknownReturnsError verifies that
// Withdraw on an instance that was never published returns an error.
// matter.js's close path silently ignores non-existent records; we
// surface an error so callers can detect double-withdraw bugs early.
func TestParityMatterJS_Goodbye_WithdrawUnknownReturnsError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	adv := mdns.NewNoop()
	err := adv.Withdraw(ctx, "UNKNOWN-INSTANCE", mdns.ServiceTypeOperational)
	if err == nil {
		t.Fatal("Withdraw on unknown instance should return error, got nil")
	}
	if !errors.Is(err, mdns.ErrServiceNotFound) {
		t.Errorf("error = %v, want ErrServiceNotFound in chain", err)
	}
}

// TestParityMatterJS_Goodbye_CloseDoesNotError verifies that Close on a
// live advertiser returns nil.
//
// Mirrors matter.js MdnsServer.ts::close — close() resolves without error.
// The production [Zeroconf] advertiser additionally sends goodbye packets
// (TTL=0) for all active records via grandcat/zeroconf's Shutdown path,
// which clears the active set. The [Noop] advertiser is test / boot-phase
// only and does not perform multicast — the no-TTL-0 divergence is
// documented in notes/parity/by_design.md.
func TestParityMatterJS_Goodbye_CloseDoesNotError(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	adv := mdns.NewNoop()

	// Publish two independent services (different NodeIDs produce different instance names).
	svc1 := mdns.BuildOperationalService(mdns.OperationalServiceConfig{
		CompressedFabricID:    [8]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x11},
		NodeID:                0x0000000011111111,
		Port:                  5540,
		HostName:              "testbridge",
		Addresses:             []net.IP{net.ParseIP("192.0.2.1")},
		SessionIdleInterval:   5000,
		SessionActiveInterval: 300,
	})
	svc2 := mdns.BuildOperationalService(mdns.OperationalServiceConfig{
		CompressedFabricID:    [8]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x22},
		NodeID:                0x0000000022222222,
		Port:                  5540,
		HostName:              "testbridge",
		Addresses:             []net.IP{net.ParseIP("192.0.2.1")},
		SessionIdleInterval:   5000,
		SessionActiveInterval: 300,
	})
	if err := adv.Publish(ctx, svc1); err != nil {
		t.Fatalf("Publish svc1: %v", err)
	}
	if err := adv.Publish(ctx, svc2); err != nil {
		t.Fatalf("Publish svc2: %v", err)
	}

	if len(adv.Active()) != 2 {
		t.Fatalf("Active() before Close: len=%d, want 2", len(adv.Active()))
	}

	if err := adv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestParityMatterJS_Goodbye_CloseIsIdempotent verifies that calling Close
// twice does not panic or return an error.
// Mirrors matter.js MdnsServer.ts::close which guards against double-close
// via an internal isInitialized flag.
func TestParityMatterJS_Goodbye_CloseIsIdempotent(t *testing.T) {
	t.Parallel()

	adv := mdns.NewNoop()
	if err := adv.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := adv.Close(); err != nil {
		t.Fatalf("second Close (idempotent): %v", err)
	}
}

// TestParityMatterJS_Goodbye_WithdrawOperationalLeavesCommissionable
// verifies that withdrawing one service type does not affect a
// co-published service of a different type.
//
// matter.js publishes operational and commissionable records independently;
// close() on one path does not affect the other.
func TestParityMatterJS_Goodbye_WithdrawOperationalLeavesCommissionable(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	adv := mdns.NewNoop()

	opSvc := buildGoodbyeTestService(t)
	commSvc := mdns.BuildCommissionableService(mdns.CommissionableServiceConfig{
		InstanceID:        [8]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x33},
		Discriminator:     0x100,
		VendorID:          0xFFF1,
		ProductID:         0x8001,
		CommissioningMode: 1,
		DeviceTypeID:      0x000E,
		Port:              5540,
		HostName:          "testbridge",
		Addresses:         []net.IP{net.ParseIP("192.0.2.1")},
	})

	if err := adv.Publish(ctx, opSvc); err != nil {
		t.Fatalf("Publish operational: %v", err)
	}
	if err := adv.Publish(ctx, commSvc); err != nil {
		t.Fatalf("Publish commissionable: %v", err)
	}
	if len(adv.Active()) != 2 {
		t.Fatalf("Active() before Withdraw: len=%d, want 2", len(adv.Active()))
	}

	// Withdraw only the operational record.
	if err := adv.Withdraw(ctx, opSvc.InstanceName, opSvc.ServiceType); err != nil {
		t.Fatalf("Withdraw operational: %v", err)
	}

	active := adv.Active()
	if len(active) != 1 {
		t.Fatalf("Active() after Withdraw operational: len=%d, want 1", len(active))
	}
	if active[0].ServiceType != mdns.ServiceTypeCommissionable {
		t.Errorf("remaining service type=%q, want %q",
			active[0].ServiceType, mdns.ServiceTypeCommissionable)
	}
}

// buildGoodbyeTestService builds a minimal valid operational service
// record for use in goodbye parity tests.
func buildGoodbyeTestService(t *testing.T) mdns.Service {
	t.Helper()
	return mdns.BuildOperationalService(mdns.OperationalServiceConfig{
		CompressedFabricID:    [8]byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x00, 0x11},
		NodeID:                0x0000000012345678,
		Port:                  5540,
		HostName:              "testbridge",
		Addresses:             []net.IP{net.ParseIP("192.0.2.1")},
		SessionIdleInterval:   5000,
		SessionActiveInterval: 300,
	})
}
