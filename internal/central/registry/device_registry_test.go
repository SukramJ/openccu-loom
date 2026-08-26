// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package registry

import (
	"sync"
	"testing"
)

func TestDeviceRegistrySorted(t *testing.T) {
	r := NewDeviceRegistry()
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "B"})
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "A"})
	r.Put(DeviceEntry{Interface: wireBidCosRF, Address: "C"})
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("len=%d", len(list))
	}
	// BidCos-RF sorts before HmIP-RF lexicographically.
	if list[0].Interface != wireBidCosRF {
		t.Fatalf("first=%s", list[0].Interface)
	}
}

func TestDeviceRegistryGetHit(t *testing.T) {
	r := NewDeviceRegistry()
	entry := DeviceEntry{Interface: wireHmIPRF, Address: "DEV001"}
	r.Put(entry)
	got, ok := r.Get(wireHmIPRF, "DEV001")
	if !ok {
		t.Fatal("expected Get to return ok=true")
	}
	if got.Address != "DEV001" {
		t.Fatalf("expected address DEV001, got %s", got.Address)
	}
}

func TestDeviceRegistryGetMiss(t *testing.T) {
	r := NewDeviceRegistry()
	_, ok := r.Get(wireHmIPRF, "UNKNOWN")
	if ok {
		t.Fatal("expected Get to return ok=false for unknown key")
	}
}

func TestDeviceRegistryRemove(t *testing.T) {
	r := NewDeviceRegistry()
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "X"})
	if !r.Remove(wireHmIPRF, "X") {
		t.Fatal("Remove should return true for existing entry")
	}
	if r.Remove(wireHmIPRF, "X") {
		t.Fatal("Remove should return false for already-removed entry")
	}
}

func TestDeviceRegistryLen(t *testing.T) {
	r := NewDeviceRegistry()
	if r.Len() != 0 {
		t.Fatalf("expected Len=0, got %d", r.Len())
	}
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "A"})
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "B"})
	if r.Len() != 2 {
		t.Fatalf("expected Len=2, got %d", r.Len())
	}
}

func TestDeviceRegistryClear(t *testing.T) {
	r := NewDeviceRegistry()
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "A"})
	r.Put(DeviceEntry{Interface: wireBidCosRF, Address: "B"})
	r.Clear()
	if r.Len() != 0 {
		t.Fatalf("expected Len=0 after Clear, got %d", r.Len())
	}
	if list := r.List(); len(list) != 0 {
		t.Fatalf("expected empty List after Clear, got %v", list)
	}
}

func TestDeviceRegistryPutOverwrites(t *testing.T) {
	r := NewDeviceRegistry()
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "A", Model: "OLD"})
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "A", Model: "NEW"})
	got, ok := r.Get(wireHmIPRF, "A")
	if !ok || got.Model != "NEW" {
		t.Fatalf("expected Model=NEW after overwrite, got ok=%v model=%s", ok, got.Model)
	}
	if r.Len() != 1 {
		t.Fatalf("Put of same key should not increase Len, got %d", r.Len())
	}
}

func TestDeviceRegistryConcurrent(t *testing.T) {
	r := NewDeviceRegistry()
	const n = 30
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "concurrent"})
			_, _ = r.Get(wireHmIPRF, "concurrent")
			_ = r.List()
			_ = r.Len()
		}(i)
	}
	wg.Wait()
}

func TestDeviceRegistryHas(t *testing.T) {
	r := NewDeviceRegistry()
	e := DeviceEntry{Interface: wireHmIPRF, Address: "DEV001", Model: "HM-CC-RT-DN"}
	if r.Has(e.Interface, e.Address) {
		t.Fatal("Has must return false for unknown entry")
	}
	r.Put(e)
	if !r.Has(e.Interface, e.Address) {
		t.Fatal("Has must return true after Put")
	}
	r.Remove(e.Interface, e.Address)
	if r.Has(e.Interface, e.Address) {
		t.Fatal("Has must return false after Remove")
	}
}

func TestDeviceRegistryModels(t *testing.T) {
	r := NewDeviceRegistry()
	if m := r.Models(); len(m) != 0 {
		t.Fatalf("Models on empty registry should return empty, got %v", m)
	}
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "A", Model: "MODEL-X"})
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "B", Model: "MODEL-Y"})
	r.Put(DeviceEntry{Interface: wireBidCosRF, Address: "C", Model: "MODEL-X"})

	models := r.Models()
	if len(models) != 2 {
		t.Fatalf("Models()=%v, want 2 unique models", models)
	}
	if models[0] != "MODEL-X" || models[1] != "MODEL-Y" {
		t.Fatalf("Models()=%v, want [MODEL-X, MODEL-Y]", models)
	}
}

func TestDeviceRegistryModelsSkipsEmpty(t *testing.T) {
	r := NewDeviceRegistry()
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "A", Model: ""})
	if m := r.Models(); len(m) != 0 {
		t.Fatalf("Models must skip empty model string, got %v", m)
	}
}

func TestDeviceRegistryAddresses(t *testing.T) {
	r := NewDeviceRegistry()
	if a := r.Addresses(wireHmIPRF); len(a) != 0 {
		t.Fatalf("Addresses on empty registry should return empty, got %v", a)
	}
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "Z001"})
	r.Put(DeviceEntry{Interface: wireHmIPRF, Address: "A001"})
	r.Put(DeviceEntry{Interface: wireBidCosRF, Address: "A001"})

	addrs := r.Addresses(wireHmIPRF)
	if len(addrs) != 2 {
		t.Fatalf("Addresses(HmIPRF)=%v, want 2", addrs)
	}
	// Must be sorted.
	if addrs[0] != "A001" || addrs[1] != "Z001" {
		t.Fatalf("Addresses(HmIPRF)=%v, want [A001, Z001]", addrs)
	}
	// Other interface unaffected.
	if a := r.Addresses(wireBidCosRF); len(a) != 1 {
		t.Fatalf("Addresses(BidCosRF)=%v, want 1", a)
	}
}
