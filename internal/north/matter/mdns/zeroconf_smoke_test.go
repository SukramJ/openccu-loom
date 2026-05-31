// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mdns_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/grandcat/zeroconf"

	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
)

// TestZeroconf_OperationalRecordRoundTrip verifies that publishing
// an operational service via the bridge's [mdns.Zeroconf] advertiser
// produces a record that a zeroconf client browse can find on the
// loopback adapter. End-to-end smoke for the post-AddNOC mDNS
// announcement code path that chip-tool's
// `FindOperationalForStayActive` step queries.
//
// Skipped under -short because mDNS browse needs ~3 s to settle
// even on loopback and CI runners with restricted multicast may not
// see the record at all. Local-dev smoke; not gated in `make test`.
func TestZeroconf_OperationalRecordRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("multicast smoke; skipped under -short")
	}
	t.Parallel()

	z := mdns.NewZeroconf()
	t.Cleanup(func() { _ = z.Close() })

	const (
		instance = "5E115606031B7608-0000000000001234"
		port     = 55401
	)
	svc := mdns.BuildOperationalService(mdns.OperationalServiceConfig{
		CompressedFabricID: [8]byte{0x5E, 0x11, 0x56, 0x06, 0x03, 0x1B, 0x76, 0x08},
		NodeID:             0x1234,
		Port:               port,
		HostName:           "matter-smoke-test",
	})
	if svc.InstanceName != instance {
		t.Fatalf("instance name: got %q want %q", svc.InstanceName, instance)
	}
	if err := z.Publish(context.Background(), svc); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		t.Skipf("zeroconf resolver init failed: %v (likely sandbox without multicast)", err)
	}

	entries := make(chan *zeroconf.ServiceEntry, 8)
	browseCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go func() {
		_ = resolver.Browse(browseCtx, mdns.ServiceTypeOperational, "local.", entries)
	}()

	deadline := time.NewTimer(3500 * time.Millisecond)
	defer deadline.Stop()
	for {
		select {
		case ent, ok := <-entries:
			if !ok {
				t.Skip("resolver closed entry channel without finding our record (sandbox multicast limit)")
			}
			if strings.EqualFold(ent.Instance, instance) {
				if ent.Port != port {
					t.Errorf("port: got %d want %d", ent.Port, port)
				}
				return // ✓
			}
		case <-deadline.C:
			t.Skip("no matching mDNS record observed within 3.5 s — multicast on loopback is unreliable, smoke is best-effort")
		}
	}
}
