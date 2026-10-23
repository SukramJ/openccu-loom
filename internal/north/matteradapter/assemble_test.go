// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"sync"

	"github.com/SukramJ/go-fabric/endpoint"

	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// The walk's tests look at the endpoints it describes through an
// endpoint assembly, as the node's Aggregator performs one over the
// described endpoints: go-fabric's endpoint assembler over the test's
// store, with the bridge identity the tests used to configure and the
// bridged vendor name the walk states per endpoint. One assembler per
// walk, so a second assembly keeps the endpoint-hosted state of the
// first — the endpoint numbers and the data versions — as a reassembly
// in the node does.

// testEndpointConfig is the endpoint assembly the tests run the walk's
// output through.
func testEndpointConfig() endpoint.Config {
	return endpoint.Config{VendorID: 0x1234, ProductID: 0x5678, NodeLabel: "TestBridge", VendorName: "eQ-3"}
}

var (
	assembliesMu sync.Mutex
	assemblies   = map[*matteradapter.Assembler]*endpoint.Assembler{}
)

// describeSnapshots is the walk's output as the endpoint assembler takes
// it: one snapshot per central.
func describeSnapshots(ctx context.Context, a *matteradapter.Assembler, snapshots []matteradapter.DeviceSnapshot) ([]endpoint.Snapshot, error) {
	described, err := a.DescribeDevices(ctx, snapshots)
	if err != nil {
		return nil, err
	}
	out := make([]endpoint.Snapshot, 0, len(described))
	for _, d := range described {
		out = append(out, endpoint.Snapshot{Scope: d.Scope, Endpoints: d.Endpoints, ModelComplete: d.ModelComplete})
	}
	return out, nil
}

// assembleDevicesWith describes snapshots with a and assembles them over
// store with cfg; later calls for the same a reuse the first assembly.
func assembleDevicesWith(ctx context.Context, a *matteradapter.Assembler, store endpoint.Store, cfg endpoint.Config, snapshots []matteradapter.DeviceSnapshot) (*endpoint.Topology, error) {
	assembliesMu.Lock()
	inner, ok := assemblies[a]
	if !ok {
		var err error
		inner, err = endpoint.New(store, cfg, nil)
		if err != nil {
			assembliesMu.Unlock()
			return nil, err
		}
		assemblies[a] = inner
	}
	assembliesMu.Unlock()
	snaps, err := describeSnapshots(ctx, a, snapshots)
	if err != nil {
		return nil, err
	}
	return inner.Assemble(ctx, snaps)
}
