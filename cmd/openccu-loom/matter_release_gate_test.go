// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

func releaseGateDescs() []hmproto.DeviceDescription {
	return []hmproto.DeviceDescription{
		{Address: "REL00001", Type: "HmIP-STH"},
		{Address: "REL00001:1", Type: "HEATING_CLIMATECONTROL_TRANSCEIVER", Parent: "REL00001"},
		{Address: "REL00002", Type: "HmIP-PS"},
		{Address: "REL00002:1", Type: "SWITCH_VIRTUAL_RECEIVER", Parent: "REL00002"},
	}
}

// TestMatterSnapshotOmitsUnreleasedDevices pins the Matter half of the
// release gate, through the function the snapshotter actually calls.
//
// A withheld device is fully materialised, so without the gate it is
// assembled into a bridged endpoint and appears on every commissioned
// controller under whatever name it was paired with. Endpoint ids are
// assigned in assembly order and persisted, so that is not cosmetic: the
// controller keeps the first identity it saw, and renaming afterwards
// does not take it back.
func TestMatterSnapshotOmitsUnreleasedDevices(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-matter-release"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	iface := hmtypes.ParseWireInterfaceID("ccu-matter-release-HmIP-RF")
	ctx := context.Background()

	// REL00001 goes through the wizard: parked, accepted, materialised —
	// and not released. REL00002 never entered the wizard at all, which
	// is what every device on an existing installation looks like.
	c.Devices.StoreDelayedDeviceDescriptions(ctx, iface, releaseGateDescs()[:2])
	_ = c.Devices.TakeDelayedDeviceDescriptions(ctx, iface, "REL00001")
	p := adapter.NewDevicePipeline(c)
	if err := p.Ingest(ctx, string(iface), hmenum.InterfaceHmIPRF, releaseGateDescs()); err != nil {
		t.Fatalf("Ingest: %v", err)
	}

	// Driven through the snapshotter the bridge actually calls. Calling
	// releasedDevicesOf directly would prove only that it CAN filter,
	// never that matterSnapshotter asks it — the bracketing defect.
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	snap := matterSnapshotter(reg, nil)

	got := addressesOf(devicesIn(snap(ctx)))
	if len(got) != 1 || got[0] != "REL00002" {
		t.Fatalf("bridged devices = %v, want only the never-held REL00002 — an unreleased device reaches every controller", got)
	}

	// Negative control: releasing must put it back into the bridged set.
	// Without this half the test would pass on a gate that withholds
	// everything, forever.
	if !adapter.ReleaseDevice(ctx, c, "REL00001") {
		t.Fatal("ReleaseDevice reported nothing to release")
	}
	got = addressesOf(devicesIn(snap(ctx)))
	if len(got) != 2 {
		t.Errorf("bridged devices after the release = %v, want both", got)
	}
}

func addressesOf(devs []*device.Device) []string {
	out := make([]string, 0, len(devs))
	for _, d := range devs {
		out = append(out, d.Address)
	}
	return out
}

// devicesIn flattens the snapshotter's per-central output.
func devicesIn(snaps []endpoint.Snapshot) []*device.Device {
	var out []*device.Device
	for i := range snaps {
		out = append(out, snaps[i].Devices...)
	}
	return out
}
