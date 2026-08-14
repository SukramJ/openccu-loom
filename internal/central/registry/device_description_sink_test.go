// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package registry

import (
	"reflect"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// fakeDescriptionSink is a [DescriptionSink] recorder used to assert that
// [DeviceDescriptionRegistry] mutations mirror into the persistence sink
// exactly once, with the normalised description.
type fakeDescriptionSink struct {
	mu      sync.Mutex
	puts    []fakeDescriptionPut
	deletes []fakeDescriptionDelete
}

type fakeDescriptionPut struct {
	iface hmtypes.WireInterfaceID
	desc  hmproto.DeviceDescription
}

type fakeDescriptionDelete struct {
	iface   hmtypes.WireInterfaceID
	address string
}

func (f *fakeDescriptionSink) PutDescription(iface hmtypes.WireInterfaceID, desc hmproto.DeviceDescription) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.puts = append(f.puts, fakeDescriptionPut{iface: iface, desc: desc})
}

func (f *fakeDescriptionSink) DeleteDescription(iface hmtypes.WireInterfaceID, address string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deletes = append(f.deletes, fakeDescriptionDelete{iface: iface, address: address})
}

func (f *fakeDescriptionSink) putCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.puts)
}

func (f *fakeDescriptionSink) deleteCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.deletes)
}

func TestDeviceDescriptionRegistryPutFiresSinkWithNormalisedDescription(t *testing.T) {
	r := NewDeviceDescriptionRegistry()
	sink := &fakeDescriptionSink{}
	r.SetSink(sink)

	raw := hmproto.DeviceDescription{Address: "  VCU1  ", Type: " HmIP-PS "}
	r.Put(wireHmIPRF, raw)

	if got := sink.putCount(); got != 1 {
		t.Fatalf("PutDescription called %d times, want 1", got)
	}
	want := hmproto.NormalizeDevice(raw)
	sink.mu.Lock()
	got := sink.puts[0]
	sink.mu.Unlock()
	if got.iface != wireHmIPRF {
		t.Errorf("sink iface=%v want %v", got.iface, wireHmIPRF)
	}
	if !reflect.DeepEqual(got.desc, want) {
		t.Errorf("sink desc=%+v want normalised %+v", got.desc, want)
	}
}

func TestDeviceDescriptionRegistryDeleteFiresSinkOnHit(t *testing.T) {
	r := NewDeviceDescriptionRegistry()
	r.Put(wireHmIPRF, hmproto.DeviceDescription{Address: "VCU2"})
	sink := &fakeDescriptionSink{}
	r.SetSink(sink)

	if !r.Delete(wireHmIPRF, "VCU2") {
		t.Fatal("Delete must report true for an existing entry")
	}
	if got := sink.deleteCount(); got != 1 {
		t.Fatalf("DeleteDescription called %d times, want 1", got)
	}
	sink.mu.Lock()
	got := sink.deletes[0]
	sink.mu.Unlock()
	if got.iface != wireHmIPRF || got.address != "VCU2" {
		t.Errorf("delete call=%+v want {HmIP-RF VCU2}", got)
	}
}

func TestDeviceDescriptionRegistryDeleteMissingFiresNoSinkCall(t *testing.T) {
	r := NewDeviceDescriptionRegistry()
	sink := &fakeDescriptionSink{}
	r.SetSink(sink)

	if r.Delete(wireHmIPRF, "GHOST") {
		t.Fatal("Delete must report false for a missing entry")
	}
	if got := sink.deleteCount(); got != 0 {
		t.Fatalf("DeleteDescription called %d times for a miss, want 0", got)
	}
	if got := sink.putCount(); got != 0 {
		t.Fatalf("PutDescription called %d times, want 0", got)
	}
}

func TestDeviceDescriptionRegistrySetSinkNilDetaches(t *testing.T) {
	r := NewDeviceDescriptionRegistry()
	sink := &fakeDescriptionSink{}
	r.SetSink(sink)

	r.Put(wireHmIPRF, hmproto.DeviceDescription{Address: "VCU3"})
	if got := sink.putCount(); got != 1 {
		t.Fatalf("PutDescription called %d times before detach, want 1", got)
	}

	r.SetSink(nil)
	r.Put(wireHmIPRF, hmproto.DeviceDescription{Address: "VCU4"})
	r.Delete(wireHmIPRF, "VCU3")

	if got := sink.putCount(); got != 1 {
		t.Fatalf("PutDescription called %d times after SetSink(nil), want unchanged 1", got)
	}
	if got := sink.deleteCount(); got != 0 {
		t.Fatalf("DeleteDescription called %d times after SetSink(nil), want 0", got)
	}
}
