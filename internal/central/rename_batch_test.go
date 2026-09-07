// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// renameBatchDevice registers a three-channel device the rename tests
// operate on.
func renameBatchDevice(t *testing.T, c *Unit, addr string) *device.Device {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     addr,
		Model:       "HmIP-BSM",
	})
	for _, no := range []int{1, 2, 3} {
		dev.AddChannel(addr+":"+strconv.Itoa(no), no, "SWITCH_VIRTUAL_RECEIVER", hmenum.ParamsetKeyValues)
	}
	c.ModelRegistry.Put(dev)
	return dev
}

// TestRenameDeviceWithChannelsUsesTheBatchHookOnce is the cost guard on
// the central side of the batched rename.
//
// The CCU exposes no address→ise-id method, so the wired hook resolves
// every address by fetching the whole inventory. Handing the device and
// its channels over one at a time therefore cost one full listing per
// channel; the batch hook receives the set and resolves it once.
func TestRenameDeviceWithChannelsUsesTheBatchHookOnce(t *testing.T) {
	c := newTestCentral(t)
	const addr = "RENBATCH1"
	renameBatchDevice(t, c, addr)

	var (
		mu      sync.Mutex
		batched []DeviceRename
		singles []string
		batchFn = func(_ context.Context, r DeviceRename) error {
			mu.Lock()
			defer mu.Unlock()
			batched = append(batched, r)
			return nil
		}
		singleFn = func(_ context.Context, address, _ string) error {
			mu.Lock()
			defer mu.Unlock()
			singles = append(singles, address)
			return nil
		}
	)
	c.SetRenameDeviceFn(singleFn)
	c.SetRenameDeviceBatchFn(batchFn)

	if err := c.RenameDeviceWithChannels(context.Background(), addr, "Kueche", true); err != nil {
		t.Fatalf("RenameDeviceWithChannels: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(batched) != 1 {
		t.Fatalf("batch hook was called %d time(s), want exactly 1", len(batched))
	}
	if len(singles) != 0 {
		t.Errorf("the per-address hook also ran for %v — that is one CCU inventory fetch each", singles)
	}
	got := batched[0]
	if got.Device.Address != addr || got.Device.Name != "Kueche" {
		t.Errorf("device rename = %+v, want {%s Kueche}", got.Device, addr)
	}
	want := []Rename{
		{Address: addr + ":1", Name: "Kueche:1"},
		{Address: addr + ":2", Name: "Kueche:2"},
		{Address: addr + ":3", Name: "Kueche:3"},
	}
	if len(got.Channels) != len(want) {
		t.Fatalf("channel renames = %+v, want %+v", got.Channels, want)
	}
	for i, w := range want {
		if got.Channels[i] != w {
			t.Errorf("channel rename %d = %+v, want %+v", i, got.Channels[i], w)
		}
	}
}

// TestRenameDeviceWithChannelsFallsBackToThePerAddressHook pins that a
// central without a batch hook still renames every channel — the batch
// hook is a cost optimisation, never a precondition. Only the CCU backend
// wires one; a Homegear or CUxD central does not.
func TestRenameDeviceWithChannelsFallsBackToThePerAddressHook(t *testing.T) {
	c := newTestCentral(t)
	const addr = "RENBATCH2"
	dev := renameBatchDevice(t, c, addr)

	var (
		mu   sync.Mutex
		seen []string
	)
	c.SetRenameDeviceFn(func(_ context.Context, address, name string) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, address+"="+name)
		return nil
	})

	if err := c.RenameDeviceWithChannels(context.Background(), addr, "Bad", true); err != nil {
		t.Fatalf("RenameDeviceWithChannels: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{addr + "=Bad", addr + ":1=Bad:1", addr + ":2=Bad:2", addr + ":3=Bad:3"}
	if len(seen) != len(want) {
		t.Fatalf("rename hook saw %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Errorf("rename %d: got %q, want %q", i, seen[i], want[i])
		}
	}
	if got := dev.Channel(addr + ":2").Name(); got != "Bad:2" {
		t.Errorf("in-memory channel name = %q, want Bad:2", got)
	}
}

// TestRenameDeviceWithChannelsSkipsChannelsWhenTheDeviceRenameFails pins
// the ordering contract DeviceRename documents: a device the CCU refused
// to rename leaves its channels alone, so the two never end up describing
// different names.
func TestRenameDeviceWithChannelsSkipsChannelsWhenTheDeviceRenameFails(t *testing.T) {
	c := newTestCentral(t)
	const addr = "RENBATCH3"
	renameBatchDevice(t, c, addr)

	boom := errors.New("ccu refused")
	var (
		mu   sync.Mutex
		seen []string
	)
	c.SetRenameDeviceFn(func(_ context.Context, address, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, address)
		if address == addr {
			return boom
		}
		return nil
	})

	if err := c.RenameDeviceWithChannels(context.Background(), addr, "Keller", true); !errors.Is(err, boom) {
		t.Fatalf("RenameDeviceWithChannels error = %v, want the CCU's refusal", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0] != addr {
		t.Fatalf("rename hook saw %v, want only the device — the channels must not be renamed around a name the CCU rejected", seen)
	}
}
