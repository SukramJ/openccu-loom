// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package registry

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func TestDeviceDescriptionRegistryPutGet(t *testing.T) {
	r := NewDeviceDescriptionRegistry()
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "ABC"})
	d, ok := r.Get(hmenum.InterfaceHmIPRF, "ABC")
	if !ok || d.Address != "ABC" {
		t.Fatalf("Get returned %+v ok=%v", d, ok)
	}
	if !r.Delete(hmenum.InterfaceHmIPRF, "ABC") {
		t.Fatal("Delete should report true for existing entry")
	}
	if r.Delete(hmenum.InterfaceHmIPRF, "ABC") {
		t.Fatal("Delete should report false for missing entry")
	}
}

func TestDeviceDescriptionRegistryAllFiltersByInterface(t *testing.T) {
	r := NewDeviceDescriptionRegistry()
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "X"})
	r.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{Address: "Y"})
	all := r.All(hmenum.InterfaceHmIPRF)
	if len(all) != 1 || all[0].Address != "X" {
		t.Fatalf("All=%+v", all)
	}
}

func TestDeviceDescriptionRegistryLen(t *testing.T) {
	r := NewDeviceDescriptionRegistry()
	if r.Len() != 0 {
		t.Fatalf("expected Len=0, got %d", r.Len())
	}
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "D1"})
	r.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{Address: "D2"})
	if r.Len() != 2 {
		t.Fatalf("expected Len=2, got %d", r.Len())
	}
}

func TestDeviceDescriptionRegistryGetMiss(t *testing.T) {
	r := NewDeviceDescriptionRegistry()
	_, ok := r.Get(hmenum.InterfaceHmIPRF, "NOPE")
	if ok {
		t.Fatal("expected Get to return ok=false for unknown address")
	}
}

func TestDeviceDescriptionRegistryAllEmpty(t *testing.T) {
	r := NewDeviceDescriptionRegistry()
	r.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{Address: "Z"})
	all := r.All(hmenum.InterfaceHmIPRF) // different interface
	if len(all) != 0 {
		t.Fatalf("expected empty slice for non-matching interface, got %v", all)
	}
}

func TestDeviceDescriptionRegistryConcurrent(t *testing.T) {
	r := NewDeviceDescriptionRegistry()
	var wg sync.WaitGroup
	const n = 20
	for range n {
		wg.Go(func() {
			r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "shared"})
			_, _ = r.Get(hmenum.InterfaceHmIPRF, "shared")
			_ = r.All(hmenum.InterfaceHmIPRF)
			_ = r.Len()
		})
	}
	wg.Wait()
}

func TestDeviceDescriptionRegistryGetAddresses(t *testing.T) {
	t.Parallel()
	r := NewDeviceDescriptionRegistry()
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "A"})
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "B"})
	r.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{Address: "C"})

	addrs := r.GetAddresses(hmenum.InterfaceHmIPRF)
	if len(addrs) != 2 {
		t.Fatalf("GetAddresses(HmIP-RF) len=%d want 2", len(addrs))
	}
	// all interfaces
	all := r.GetAddresses("")
	if len(all) != 3 {
		t.Fatalf("GetAddresses(\"\") len=%d want 3", len(all))
	}
}

func TestDeviceDescriptionRegistryGetDeviceWithChannels(t *testing.T) {
	t.Parallel()
	r := NewDeviceDescriptionRegistry()
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "DEV", Children: []string{"DEV:1", "DEV:2"}})
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "DEV:1", Parent: "DEV"})
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "DEV:2", Parent: "DEV"})

	m := r.GetDeviceWithChannels(hmenum.InterfaceHmIPRF, "DEV")
	if len(m) != 3 {
		t.Fatalf("GetDeviceWithChannels len=%d want 3 (device + 2 channels)", len(m))
	}
	if _, ok := m["DEV"]; !ok {
		t.Error("device entry must be in result")
	}
	if _, ok := m["DEV:1"]; !ok {
		t.Error("channel DEV:1 must be in result")
	}

	// unknown device
	m2 := r.GetDeviceWithChannels(hmenum.InterfaceHmIPRF, "UNKNOWN")
	if len(m2) != 0 {
		t.Fatal("unknown device must return empty map")
	}
}

func TestDeviceDescriptionRegistryGetInterfaceIDs(t *testing.T) {
	t.Parallel()
	r := NewDeviceDescriptionRegistry()
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "X"})
	r.Put(hmenum.InterfaceBidCosRF, hmproto.DeviceDescription{Address: "Y"})

	ids := r.GetInterfaceIDs()
	if len(ids) != 2 {
		t.Fatalf("GetInterfaceIDs len=%d want 2", len(ids))
	}
	seen := make(map[hmenum.Interface]bool)
	for _, id := range ids {
		seen[id] = true
	}
	if !seen[hmenum.InterfaceHmIPRF] || !seen[hmenum.InterfaceBidCosRF] {
		t.Error("expected both interfaces to be returned")
	}
}

func TestDeviceDescriptionRegistryGetModel(t *testing.T) {
	t.Parallel()
	r := NewDeviceDescriptionRegistry()
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "VCU001", Type: "HM-CC-RT-DN"})
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "VCU001:1", Parent: "VCU001", Type: "HM-CC-RT-DN"})

	model := r.GetModel("VCU001")
	if model != "HM-CC-RT-DN" {
		t.Fatalf("GetModel=%q want HM-CC-RT-DN", model)
	}
	// channel address must not be returned as model
	chModel := r.GetModel("VCU001:1")
	if chModel != "" {
		t.Fatalf("GetModel for channel=%q want empty string", chModel)
	}
	// unknown
	if r.GetModel("NONE") != "" {
		t.Fatal("GetModel for unknown address must return empty string")
	}
}

func TestDeviceDescriptionRegistryHasDeviceDescriptions(t *testing.T) {
	t.Parallel()
	r := NewDeviceDescriptionRegistry()
	if r.HasDeviceDescriptions(hmenum.InterfaceHmIPRF) {
		t.Fatal("HasDeviceDescriptions must return false for empty registry")
	}
	r.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{Address: "Z"})
	if !r.HasDeviceDescriptions(hmenum.InterfaceHmIPRF) {
		t.Fatal("HasDeviceDescriptions must return true after Put")
	}
	if r.HasDeviceDescriptions(hmenum.InterfaceBidCosRF) {
		t.Fatal("HasDeviceDescriptions must return false for interface with no entries")
	}
}
