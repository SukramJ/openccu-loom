// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge_test

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
)

// boundPort returns the port the bridge's UDP listener actually bound.
func boundPort(t *testing.T, b *bridge.Bridge) uint16 {
	t.Helper()
	addr := b.LocalAddr()
	if addr == "" {
		t.Fatal("LocalAddr: bridge reports no bound address")
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	if port == 0 {
		t.Fatal("bound port is 0")
	}
	return uint16(port)
}

// advertisedPort returns the SRV port of the single published record of
// the given service type.
func advertisedPort(t *testing.T, noop *mdns.Noop, serviceType string) uint16 {
	t.Helper()
	active := noop.Active()
	for i := range active {
		if active[i].ServiceType == serviceType {
			return active[i].Port
		}
	}
	t.Fatalf("no %s record published", serviceType)
	return 0
}

// startEphemeralBridge starts a bridge on `:0` so the OS assigns the UDP
// port — the configuration an operator uses to avoid a 5540 conflict
// with a second Matter bridge on the same host.
func startEphemeralBridge(t *testing.T, noop *mdns.Noop) *bridge.Bridge {
	t.Helper()
	b, err := bridge.New(bridge.NewFakeStore(), emptySnapshotter, noop, bridge.Config{
		Listen:    ":0",
		VendorID:  0x1234,
		ProductID: 0x5678,
		NodeLabel: "test-advertised-port",
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})
	return b
}

// TestAnnounceCommissioning_AdvertisesBoundPort pins that the
// commissionable `_matterc._udp` SRV record carries the port the
// listener is actually bound to. With `listen: ":0"` the OS assigns an
// ephemeral port; advertising the 5540 default instead sends every
// commissioner's PBKDFParamRequest to a port nothing listens on, and
// pairing fails against a QR code that is perfectly valid.
func TestAnnounceCommissioning_AdvertisesBoundPort(t *testing.T) {
	t.Parallel()
	noop := mdns.NewNoop()
	b := startEphemeralBridge(t, noop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.AnnounceCommissioning(ctx, bridge.CommissioningAdvertisement{
		Discriminator: 0x0F00,
		VendorID:      0x1234,
		ProductID:     0x5678,
		NodeLabel:     "test-advertised-port",
		InstanceID:    [8]byte{1, 2, 3, 4, 5, 6, 7, 8},
	}); err != nil {
		t.Fatalf("AnnounceCommissioning: %v", err)
	}

	want := boundPort(t, b)
	if got := advertisedPort(t, noop, mdns.ServiceTypeCommissionable); got != want {
		t.Errorf("commissionable SRV port = %d, want the bound port %d", got, want)
	}
}

// TestAnnounceFabric_AdvertisesBoundPort pins the same invariant for the
// operational `_matter._tcp` record: a bridge paired by direct
// addressing becomes unreachable after a restart when the operational
// SRV names a port the listener never bound.
func TestAnnounceFabric_AdvertisesBoundPort(t *testing.T) {
	t.Parallel()
	noop := mdns.NewNoop()
	b := startEphemeralBridge(t, noop)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b.AnnounceFabric(ctx, [8]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}, 0x0102030405060708)

	want := boundPort(t, b)
	if got := advertisedPort(t, noop, mdns.ServiceTypeOperational); got != want {
		t.Errorf("operational SRV port = %d, want the bound port %d", got, want)
	}
}
