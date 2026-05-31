// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package registry

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func newTestDevice(address string) *device.Device {
	return device.New(device.Config{
		Address:   address,
		Interface: hmenum.InterfaceHmIPRF,
	})
}

func TestModelRegistryPutGet(t *testing.T) {
	r := NewModelRegistry()
	d := newTestDevice("MDEV001")
	r.Put(d)
	got, ok := r.Get("MDEV001")
	if !ok || got.Address != "MDEV001" {
		t.Fatalf("Get returned ok=%v addr=%s", ok, got.Address)
	}
}

func TestModelRegistryPutNilNoOp(t *testing.T) {
	r := NewModelRegistry()
	r.Put(nil) // must not panic
	if r.Len() != 0 {
		t.Fatal("Put(nil) must not increment Len")
	}
}

func TestModelRegistryPutEmptyAddressNoOp(t *testing.T) {
	r := NewModelRegistry()
	d := newTestDevice("")
	r.Put(d) // address is empty — must be silently dropped
	if r.Len() != 0 {
		t.Fatal("Put with empty address must not increment Len")
	}
}

func TestModelRegistryGetMiss(t *testing.T) {
	r := NewModelRegistry()
	_, ok := r.Get("NOPE")
	if ok {
		t.Fatal("expected Get to return ok=false for unknown address")
	}
}

func TestModelRegistryList(t *testing.T) {
	r := NewModelRegistry()
	r.Put(newTestDevice("C"))
	r.Put(newTestDevice("A"))
	r.Put(newTestDevice("B"))
	list := r.List()
	if len(list) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(list))
	}
	if list[0].Address != "A" || list[1].Address != "B" || list[2].Address != "C" {
		t.Fatalf("List not sorted by address: %v", list)
	}
}

func TestModelRegistryListEmpty(t *testing.T) {
	r := NewModelRegistry()
	if list := r.List(); len(list) != 0 {
		t.Fatalf("expected empty list, got %v", list)
	}
}

func TestModelRegistryRemoveHit(t *testing.T) {
	r := NewModelRegistry()
	removed := false
	d := newTestDevice("REM001")
	d.OnRemoved(func() { removed = true })
	r.Put(d)
	if !r.Remove("REM001") {
		t.Fatal("Remove should return true for existing device")
	}
	if !removed {
		t.Fatal("NotifyRemoved hook should have fired")
	}
	if r.Len() != 0 {
		t.Fatalf("expected Len=0 after Remove, got %d", r.Len())
	}
}

func TestModelRegistryRemoveMiss(t *testing.T) {
	r := NewModelRegistry()
	if r.Remove("GHOST") {
		t.Fatal("Remove should return false for non-existent device")
	}
}

func TestModelRegistryLen(t *testing.T) {
	r := NewModelRegistry()
	if r.Len() != 0 {
		t.Fatalf("expected Len=0, got %d", r.Len())
	}
	r.Put(newTestDevice("D1"))
	r.Put(newTestDevice("D2"))
	if r.Len() != 2 {
		t.Fatalf("expected Len=2, got %d", r.Len())
	}
}

func TestModelRegistryConcurrent(t *testing.T) {
	r := NewModelRegistry()
	var wg sync.WaitGroup
	const n = 30
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.Put(newTestDevice("shared"))
			_, _ = r.Get("shared")
			_ = r.List()
			_ = r.Len()
		}()
	}
	wg.Wait()
}
