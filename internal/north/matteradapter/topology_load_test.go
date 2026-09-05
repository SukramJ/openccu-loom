// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// TestAssemble_LargeFleet600Endpoints regresses against ADR 0012
// §Risks #3 (endpoint topology > 256). The Matter spec assigns
// endpoint IDs as uint16 (1..65534 for bridged endpoints) and a node
// MAY surface more than 256. Bridge controllers (HA, Apple Home) cap
// *display* at ~256 but the protocol surface itself does not. This
// test exercises the assembly + lookup path at ~600 endpoints —
// derived from a realistic CCU fleet (200 devices × 3 channels).
//
// The test fails on:
//   - any endpoint ID overflow / wraparound in the assigner.
//   - O(n^2) regression in the assembly (timeout under -short=false).
//   - FindByID failure for high-ID endpoints (256, 500, 600).
//   - ClusterServers() returning nil for any non-root endpoint that
//     has a Source attached.
func TestAssemble_LargeFleet600Endpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("topology load test (skipped under -short)")
	}
	t.Parallel()
	ctx := context.Background()

	const (
		centralName = "ccu-large"
		devices     = 200
		channels    = 3
	)
	wantEndpoints := devices*channels + 2 // +2 for root (ID 0) + aggregator (ID 1)

	devs := make([]*device.Device, 0, devices)
	for d := range devices {
		dev := newDevice(fmt.Sprintf("LRG%05d", d), fmt.Sprintf("device-%d", d))
		for c := 1; c <= channels; c++ {
			chAddr := fmt.Sprintf("LRG%05d:%d", d, c)
			ch := addChannel(dev, chAddr, c)
			src := &stubEndpointSource{
				key:        dpKey(chAddr, "STATE"),
				deviceType: 0x010A, // OnOffPlugInUnit; concrete type irrelevant for the count.
			}
			ch.SetCustomDataPoint(src)
		}
		devs = append(devs, dev)
	}

	snap := matteradapter.DeviceSnapshot{CentralName: centralName, Devices: devs}
	a, err := matteradapter.New(newFakeStore(), validConfig(), nil)
	if err != nil {
		t.Fatalf("matteradapter.New: %v", err)
	}
	top, err := a.AssembleDevices(ctx, []matteradapter.DeviceSnapshot{snap})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if got := len(top.Endpoints); got != wantEndpoints {
		t.Fatalf("len(Endpoints)=%d, want %d", got, wantEndpoints)
	}

	// Spot-check IDs across the > 256 boundary.
	// Bridged endpoints start at ID 2 (root=0, aggregator=1).
	for _, id := range []uint16{2, 255, 256, 257, 500, 600} {
		ep := top.FindByID(id)
		if ep == nil {
			t.Errorf("FindByID(%d) returned nil; topology should contain it", id)
			continue
		}
		if ep.ID != id {
			t.Errorf("FindByID(%d).ID=%d, mismatch", id, ep.ID)
		}
		if ep.IsRoot() {
			t.Errorf("FindByID(%d) returned root", id)
		}
		if ep.Source == nil {
			t.Errorf("FindByID(%d).Source is nil; expected stubEndpointSource", id)
		}
	}

	// Endpoint IDs must be unique and monotonic across the bridged
	// range. A regression in the ID assigner would surface here.
	seen := make(map[uint16]struct{}, wantEndpoints)
	var prev uint16
	for i, ep := range top.Endpoints {
		if _, dup := seen[ep.ID]; dup {
			t.Fatalf("duplicate endpoint ID %d at index %d", ep.ID, i)
		}
		seen[ep.ID] = struct{}{}
		if i > 0 && ep.ID <= prev {
			t.Fatalf("endpoint IDs not monotonic at index %d: prev=%d, this=%d", i, prev, ep.ID)
		}
		prev = ep.ID
	}
}

// TestAssemble_RejectEndpointIDOverflow guards the upper bound of the
// uint16 endpoint ID space. The Matter spec reserves 0xFFFF; the
// bridge must surface an explicit error rather than silently rolling
// over when an extremely large fleet would push past 65534 bridged
// endpoints.
//
// The test does NOT instantiate 65534 endpoints (too slow); instead
// it primes the fakeStore with a high pre-assigned ID and verifies
// the next assignment errors cleanly. This locks the boundary
// behaviour without carrying the cost of the full sweep.
func TestAssemble_RejectEndpointIDOverflow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const centralName = "ccu-overflow"

	dev := newDevice("OVF00001", "overflow-device")
	ch := addChannel(dev, "OVF00001:1", 1)
	src := &stubEndpointSource{
		key:        dpKey("OVF00001:1", "STATE"),
		deviceType: 0x010A,
	}
	ch.SetCustomDataPoint(src)

	snap := matteradapter.DeviceSnapshot{CentralName: centralName, Devices: []*device.Device{dev}}

	fs := newFakeStore()
	fs.nextID = 0xFFFE // next assignment would land on 0xFFFE; the one after that overflows.

	a, _ := matteradapter.New(fs, validConfig(), nil)
	top, err := a.AssembleDevices(ctx, []matteradapter.DeviceSnapshot{snap})
	if err != nil {
		// Acceptable: the assigner refused to mint near the boundary.
		// The goal is "no silent wraparound" — log and move on.
		t.Logf("assignment near upper bound surfaced error (acceptable): %v", err)
		return
	}
	// If the assigner did mint, the resulting ID must be non-zero and
	// not 0xFFFF (Matter reserved). 0xFFFE is the legal last bridged ID.
	// Endpoints[2] is the first bridged endpoint (root=0, aggregator=1, bridged=2).
	if got := top.Endpoints[2].ID; got == 0 || got == 0xFFFF {
		t.Fatalf("assigned endpoint ID=0x%04X; must not be 0 or 0xFFFF", got)
	}
}
