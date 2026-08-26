// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestEnsureRootChannelCreatesOnce(t *testing.T) {
	t.Parallel()
	d := New(Config{
		InterfaceID: "Test-HmIP-RF",
		Address:     "VCU0000050",
	})
	if d.RootChannel() != nil {
		t.Fatal("fresh device must have nil RootChannel")
	}
	first := d.EnsureRootChannel()
	if first == nil {
		t.Fatal("EnsureRootChannel returned nil")
	}
	if first.Number != ChannelNumberDevice {
		t.Errorf("Number = %d, want %d (ChannelNumberDevice)", first.Number, ChannelNumberDevice)
	}
	if first.Address != "VCU0000050" {
		t.Errorf("Address = %q, want device address %q", first.Address, "VCU0000050")
	}
	// Second call returns same pointer.
	if d.EnsureRootChannel() != first {
		t.Error("EnsureRootChannel must be idempotent — second call produced a different channel")
	}
	if d.RootChannel() != first {
		t.Error("RootChannel() must return the value EnsureRootChannel created")
	}
}

func TestChannelLookupHonoursRootChannel(t *testing.T) {
	t.Parallel()
	d := New(Config{
		InterfaceID: "Test-HmIP-RF",
		Address:     "VCU0000050",
	})
	d.EnsureRootChannel()
	// Channel(d.Address) must resolve to the root pseudo-channel.
	got := d.Channel("VCU0000050")
	if got == nil {
		t.Fatal("Channel(deviceAddr) returned nil — root lookup not wired")
	}
	if got.Number != ChannelNumberDevice {
		t.Errorf("Channel(deviceAddr).Number = %d, want %d", got.Number, ChannelNumberDevice)
	}
	// Real channel lookup still works.
	d.AddChannel("VCU0000050:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	if d.Channel("VCU0000050:1") == nil {
		t.Error("real channel lookup broke after root channel was added")
	}
	// Real channels are NOT included in d.Channels() entry for root.
	for _, ch := range d.Channels() {
		if ch.Number == ChannelNumberDevice {
			t.Error("d.Channels() must NOT include the root pseudo-channel — adapters expect only real channels")
		}
	}
}

func TestRootChannelMasterParamsAreVisible(t *testing.T) {
	t.Parallel()
	d := New(Config{
		InterfaceID: "Test-BidCos-RF",
		Address:     "VCU0000050",
	})
	root := d.EnsureRootChannel()
	// Stamp a synthetic MASTER DP so we can verify lookup works
	// through both the channel object and the device.
	dp := newMasterParam("VCU0000050", "TEMPERATURE_OFFSET")
	root.PutMaster(dp)
	if root.MasterParameter(hmenum.Parameter("TEMPERATURE_OFFSET")) == nil {
		t.Error("RootChannel MASTER lookup broke")
	}
	// AllMasterDataPoints must include the root channel's DP.
	found := false
	for _, got := range d.AllMasterDataPoints() {
		if got == dp {
			found = true
			break
		}
	}
	if !found {
		t.Error("Device.AllMasterDataPoints() missed the root pseudo-channel's DP")
	}
}
