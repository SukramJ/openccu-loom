// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

// Test-side stand-in for the composition the daemon performs in
// cmd/openccu-loom/daemon_matter.go: the host walks its device model,
// assembles a topology from it, and hands the bridge the result. The
// bridge itself neither assembles nor knows the model, so tests that
// want a populated topology have to do the same thing the daemon does.
//
// Lives in `package bridge` so both the white-box tests in this
// directory and the black-box tests in `package bridge_test` can reach
// it through the exported constructors, the same arrangement
// [NewFakeStore] uses. Because the file ends in `_test.go` the
// dependency on the daemon's model packages never reaches the shipped
// binary — which is the whole reason the assembler moved host-side.

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// Identity the test assembler stamps on the root endpoint. Any non-zero
// vendor / product pair and a non-empty label satisfy the assembler's
// validation; the values match the ones the bridge tests already pass in
// their [Config] so a reader sees one identity, not two.
const (
	testAssemblerVendorID  uint16 = 0x1234
	testAssemblerProductID uint16 = 0x5678
	testAssemblerNodeLabel        = "test-bridge"
)

// NewAssemblingSnapshotter returns a [Snapshotter] that assembles walk's
// device snapshots through one assembler backed by one in-memory store.
// Endpoint ids therefore persist across repeated calls exactly as they do
// in a running daemon, which is what makes it usable for reassembly
// tests that compare ids between two assemblies.
//
// A nil walk yields the empty fleet: root plus aggregator, no bridged
// endpoints.
func NewAssemblingSnapshotter(walk func(context.Context) []matteradapter.DeviceSnapshot) Snapshotter {
	return newAssemblingSnapshotter(walk, false)
}

// NewMeasuringSnapshotter is [NewAssemblingSnapshotter] with standalone
// measurement endpoints turned on, the assembly mode the scenario
// topologies are written against.
func NewMeasuringSnapshotter(walk func(context.Context) []matteradapter.DeviceSnapshot) Snapshotter {
	return newAssemblingSnapshotter(walk, true)
}

func newAssemblingSnapshotter(walk func(context.Context) []matteradapter.DeviceSnapshot, includeMeasurements bool) Snapshotter {
	asm, err := matteradapter.New(NewFakeStore(), matteradapter.Config{
		VendorID:            testAssemblerVendorID,
		ProductID:           testAssemblerProductID,
		NodeLabel:           testAssemblerNodeLabel,
		IncludeMeasurements: includeMeasurements,
	}, nil)
	return func(ctx context.Context) (*endpoint.Topology, error) {
		if err != nil {
			return nil, err
		}
		if walk == nil {
			return asm.AssembleDevices(ctx, nil)
		}
		return asm.AssembleDevices(ctx, walk(ctx))
	}
}

// NewEmptySnapshotter returns a [Snapshotter] over an empty fleet.
func NewEmptySnapshotter() Snapshotter {
	return NewAssemblingSnapshotter(nil)
}
