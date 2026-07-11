// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package registry

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// newIdentifyChannelDevice builds a device with a single channel whose
// device- and channel-level ise_ids are both set, for exercising
// [ModelRegistry.IdentifyChannel] across multiple registered devices.
func newIdentifyChannelDevice(address string, deviceIseID int, channelAddress string, channelIseID int) *device.Device {
	d := device.New(device.Config{
		Address:   address,
		Interface: hmenum.InterfaceHmIPRF,
		IseID:     deviceIseID,
	})
	ch := d.AddChannel(channelAddress, 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch.IseID = channelIseID
	return d
}

// TestModelRegistryIdentifyChannelMatchesRegisteredDevice verifies that a
// sysvar-name text carrying a registered channel's ise_id resolves to that
// device and channel.
func TestModelRegistryIdentifyChannelMatchesRegisteredDevice(t *testing.T) {
	t.Parallel()

	r := NewModelRegistry()
	devA := newIdentifyChannelDevice("0001ABCD", 100, "0001ABCD:1", 200)
	devB := newIdentifyChannelDevice("0002EFGH", 300, "0002EFGH:1", 400)
	r.Put(devA)
	r.Put(devB)

	// '_' is itself a word character (see device.isWordChar), so it does not
	// act as a token boundary — use a space to bound the ise_id unambiguously.
	gotDev, gotCh, ok := r.IdentifyChannel("sv 200 x")
	if !ok {
		t.Fatal("IdentifyChannel(): expected ok=true")
	}
	if gotDev != devA {
		t.Fatalf("IdentifyChannel(): matched device %q, want %q", gotDev.Address, devA.Address)
	}
	if gotCh == nil || gotCh.Address != "0001ABCD:1" {
		t.Fatalf("IdentifyChannel(): matched channel %v, want address %q", gotCh, "0001ABCD:1")
	}
}

// TestModelRegistryIdentifyChannelNoMatch verifies (nil, nil, false) when no
// registered device's address/ise_id appears in text.
func TestModelRegistryIdentifyChannelNoMatch(t *testing.T) {
	t.Parallel()

	r := NewModelRegistry()
	r.Put(newIdentifyChannelDevice("0001ABCD", 100, "0001ABCD:1", 200))
	r.Put(newIdentifyChannelDevice("0002EFGH", 300, "0002EFGH:1", 400))

	gotDev, gotCh, ok := r.IdentifyChannel("sv_999")
	if ok || gotDev != nil || gotCh != nil {
		t.Fatalf("IdentifyChannel(): got (%v, %v, %v), want (nil, nil, false)", gotDev, gotCh, ok)
	}
}

// TestModelRegistryIdentifyChannelEmptyText verifies empty text short-circuits
// to (nil, nil, false) without scanning any registered device.
func TestModelRegistryIdentifyChannelEmptyText(t *testing.T) {
	t.Parallel()

	r := NewModelRegistry()
	r.Put(newIdentifyChannelDevice("0001ABCD", 100, "0001ABCD:1", 200))

	gotDev, gotCh, ok := r.IdentifyChannel("")
	if ok || gotDev != nil || gotCh != nil {
		t.Fatalf("IdentifyChannel(\"\"): got (%v, %v, %v), want (nil, nil, false)", gotDev, gotCh, ok)
	}
}
