// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func defaultDescriptor(t *testing.T) *core.Descriptor {
	t.Helper()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 0x000E, Revision: 1}},
		[]uint32{0x001D, 0x001E},
		[]uint32{},
		[]uint16{1, 2},
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}
	return d
}

func TestDescriptor_NewEmptyDeviceTypes(t *testing.T) {
	t.Parallel()
	_, err := core.NewDescriptor(nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty deviceTypes, got nil")
	}
}

func TestDescriptor_NewEmptySliceDeviceTypes(t *testing.T) {
	t.Parallel()
	_, err := core.NewDescriptor([]core.DeviceTypeStruct{}, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for empty deviceTypes slice, got nil")
	}
}

func TestDescriptor_ClusterID(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	if got := d.MatterClusterID(); got != 0x001D {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x001D", got)
	}
}

func TestDescriptor_ReadClusterRevision(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	v, ok := d.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 3 {
		t.Fatalf("ClusterRevision = %v, want 3", v)
	}
}

func TestDescriptor_ReadFeatureMap(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	v, ok := d.MatterRead(cluster.AttrGlobalFeatureMap)
	if !ok {
		t.Fatal("FeatureMap: ok=false")
	}
	if v.(uint32) != 0 {
		t.Fatalf("FeatureMap = %v, want 0", v)
	}
}

func TestDescriptor_ReadDeviceTypeList(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	v, ok := d.MatterRead(0x0000)
	if !ok {
		t.Fatal("DeviceTypeList: ok=false")
	}
	list := v.([]core.DeviceTypeStruct)
	if len(list) != 1 {
		t.Fatalf("DeviceTypeList len=%d, want 1", len(list))
	}
	if list[0].DeviceType != 0x000E {
		t.Fatalf("DeviceType = 0x%X, want 0x000E", list[0].DeviceType)
	}
}

func TestDescriptor_ReadServerList(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	v, ok := d.MatterRead(0x0001)
	if !ok {
		t.Fatal("ServerList: ok=false")
	}
	list := v.([]uint32)
	if len(list) != 2 {
		t.Fatalf("ServerList len=%d, want 2", len(list))
	}
}

func TestDescriptor_ReadClientList(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	v, ok := d.MatterRead(0x0002)
	if !ok {
		t.Fatal("ClientList: ok=false")
	}
	_ = v.([]uint32)
}

func TestDescriptor_ReadPartsList(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	v, ok := d.MatterRead(0x0003)
	if !ok {
		t.Fatal("PartsList: ok=false")
	}
	list := v.([]uint16)
	if len(list) != 2 {
		t.Fatalf("PartsList len=%d, want 2", len(list))
	}
}

// TestDescriptor_TagListReportedUnsupported asserts that TagList
// (0x0004) is reported as unsupported when the TAGLIST feature bit
// is not advertised. Apple Home's iOS Matter SDK does not yet ship
// the `semtag` schema and rejects the whole Descriptor cluster when
// TagList appears in a Subscribe-Initial — HAP rebuild then aborts
// with HAPErrorDomain Code=14 ("No Endpoints In Use"). matter.js
// `descriptor.element.ts` defines the attribute as conformance
// "TAGLIST" so omitting it under FeatureMap=0 is spec-compliant.
func TestDescriptor_TagListReportedUnsupported(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	v, ok := d.MatterRead(0x0004)
	if ok {
		t.Fatalf("TagList: ok=true (returned %v) — expected unsupported when TAGLIST feature is off", v)
	}
}

func TestDescriptor_ReadUnknownAttribute(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	v, ok := d.MatterRead(0xBEEF)
	if ok || v != nil {
		t.Fatalf("unknown attr: got (%v, %v), want (nil, false)", v, ok)
	}
}

func TestDescriptor_WriteReturnsError(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	ctx := context.Background()
	for _, attrID := range []uint32{0x0000, 0x0001, 0x0002, 0x0003, 0xFFFD} {
		err := d.MatterWrite(ctx, attrID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterWrite(0x%04X) expected error, got nil", attrID)
		}
	}
}

func TestDescriptor_InvokeReturnsError(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	ctx := context.Background()
	for _, cmdID := range []uint32{0x00, 0x01, 0xFF} {
		_, err := d.MatterInvoke(ctx, cmdID, nil, hmenum.CommandPriorityHigh)
		if err == nil {
			t.Errorf("MatterInvoke(0x%02X) expected error, got nil", cmdID)
		}
	}
}

func TestDescriptor_SetPartsList(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	d.SetPartsList([]uint16{10, 20, 30})
	v, ok := d.MatterRead(0x0003)
	if !ok {
		t.Fatal("PartsList after SetPartsList: ok=false")
	}
	list := v.([]uint16)
	if len(list) != 3 || list[0] != 10 || list[1] != 20 || list[2] != 30 {
		t.Fatalf("PartsList = %v, want [10 20 30]", list)
	}
}

func TestDescriptor_MatterReportableContainsPartsList(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	reportable := d.MatterReportable()
	found := slices.Contains(reportable, 0x0003)
	if !found {
		t.Fatalf("MatterReportable = %v, expected PartsList (0x0003)", reportable)
	}
}

func TestDescriptor_ReadReturnsDefensiveCopy(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)

	// Mutate DeviceTypeList returned slice.
	v1, _ := d.MatterRead(0x0000)
	list1 := v1.([]core.DeviceTypeStruct)
	list1[0].DeviceType = 0xDEAD
	v2, _ := d.MatterRead(0x0000)
	list2 := v2.([]core.DeviceTypeStruct)
	if list2[0].DeviceType == 0xDEAD {
		t.Fatal("DeviceTypeList: mutation of returned slice affected internal state")
	}

	// Mutate PartsList returned slice.
	vp1, _ := d.MatterRead(0x0003)
	plist1 := vp1.([]uint16)
	if len(plist1) > 0 {
		plist1[0] = 0xBEEF
		vp2, _ := d.MatterRead(0x0003)
		plist2 := vp2.([]uint16)
		if len(plist2) > 0 && plist2[0] == 0xBEEF {
			t.Fatal("PartsList: mutation of returned slice affected internal state")
		}
	}

	// Mutate ServerList returned slice.
	vs1, _ := d.MatterRead(0x0001)
	slist1 := vs1.([]uint32)
	if len(slist1) > 0 {
		slist1[0] = 0xDEADBEEF
		vs2, _ := d.MatterRead(0x0001)
		slist2 := vs2.([]uint32)
		if len(slist2) > 0 && slist2[0] == 0xDEADBEEF {
			t.Fatal("ServerList: mutation of returned slice affected internal state")
		}
	}
}

func TestDescriptor_WriteErrorWrapsReadOnly(t *testing.T) {
	t.Parallel()
	d := defaultDescriptor(t)
	err := d.MatterWrite(context.Background(), 0x0000, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Descriptor does not export errDescriptorReadOnly but we verify a non-nil error.
	_ = errors.Unwrap(err) // exercising wrapping chain; error existence already checked above.
}

// TestDescriptor_ServerListProvider_Overrides pins the Bug K fix:
// SetServerListProvider replaces the static list and every MatterRead
// reflects the provider's current return value. Mirrors matter.js
// DescriptorServer.#serverList (DescriptorServer.ts:236-244) — the
// advertised ServerList is always the set of mounted behaviors, never
// a hardcoded list that can drift when a cluster is added without
// updating the list.
func TestDescriptor_ServerListProvider_Overrides(t *testing.T) {
	t.Parallel()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 0x0016, Revision: 3}},
		[]uint32{0xDEAD}, // static list — must be ignored when provider is set
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}
	calls := 0
	d.SetServerListProvider(func() []uint32 {
		calls++
		// Simulate the daemon-side "mounted clusters" iteration: every
		// call returns the live snapshot.
		return []uint32{0x001D, 0x001F, 0x0028, 0x002A, 0x0038}
	})

	v, ok := d.MatterRead(0x0001)
	if !ok {
		t.Fatal("ServerList: ok=false")
	}
	got, ok := v.([]uint32)
	if !ok {
		t.Fatalf("ServerList type = %T, want []uint32", v)
	}
	want := []uint32{0x001D, 0x001F, 0x0028, 0x002A, 0x0038}
	if len(got) != len(want) {
		t.Fatalf("ServerList len=%d, want %d", len(got), len(want))
	}
	for i, id := range got {
		if id != want[i] {
			t.Errorf("ServerList[%d]=0x%04X, want 0x%04X", i, id, want[i])
		}
	}
	// Bug-K guard: the static 0xDEAD entry MUST NOT leak through when
	// a provider is set. Re-derivation per read is the matter.js
	// contract — drift between mounted set and advertised list is
	// rejected by Apple's HAP mapper as schematic inconsistency.
	for _, id := range got {
		if id == 0xDEAD {
			t.Error("static ServerList leaked through provider — Bug K regression")
		}
	}
	if calls == 0 {
		t.Error("provider was not consulted on MatterRead")
	}
}

// TestDescriptor_ServerListProvider_NilReverts verifies that passing
// nil to SetServerListProvider falls back to the static list. Unit
// tests rely on this to construct a deterministic ServerList without
// wiring a closure.
func TestDescriptor_ServerListProvider_NilReverts(t *testing.T) {
	t.Parallel()
	d, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 0x0016, Revision: 3}},
		[]uint32{0x001D, 0x001F},
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}
	d.SetServerListProvider(func() []uint32 { return []uint32{0xCAFE} })
	d.SetServerListProvider(nil)

	v, _ := d.MatterRead(0x0001)
	got := v.([]uint32)
	if len(got) != 2 || got[0] != 0x001D || got[1] != 0x001F {
		t.Errorf("ServerList = %v, want [0x001D 0x001F] after nil provider revert", got)
	}
}

func TestDescriptor_SetPartsListProvider(t *testing.T) {
	t.Parallel()
	desc, err := core.NewDescriptor(
		[]core.DeviceTypeStruct{{DeviceType: 0x0013, Revision: 3}},
		[]uint32{},
		[]uint32{},
		[]uint16{},
	)
	if err != nil {
		t.Fatalf("NewDescriptor: %v", err)
	}
	called := false
	desc.SetPartsListProvider(func() []uint16 {
		called = true
		return []uint16{1, 2, 3}
	})
	v, ok := desc.MatterRead(0x0003) // PartsList
	if !ok {
		t.Fatal("PartsList: ok=false after provider set")
	}
	parts := v.([]uint16)
	if !called {
		t.Fatal("provider was never called")
	}
	if len(parts) != 3 {
		t.Fatalf("PartsList len = %d, want 3", len(parts))
	}
}

// TestDescriptorProviderInstallRacesRead pins that installing the
// PartsList / ServerList providers is safe while the IM layer is already
// dispatching reads. The daemon attaches both well after the bridge
// started serving, so a previously-paired commissioner re-establishing
// CASE reads 0:0x001D:0x0003 concurrently with the install. Run under
// -race, which is where the unsynchronised field write shows up.
func TestDescriptorProviderInstallRacesRead(t *testing.T) {
	d := defaultDescriptor(t)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 500 {
			_, _ = d.MatterRead(0x0003)
			_, _ = d.MatterRead(0x0001)
		}
	}()
	go func() {
		defer wg.Done()
		for i := range 500 {
			parts := []uint16{uint16(i)}
			d.SetPartsListProvider(func() []uint16 { return parts })
			d.SetServerListProvider(func() []uint32 { return []uint32{0x001D} })
			d.SetPartsList(parts)
		}
	}()
	wg.Wait()

	got, ok := d.MatterRead(0x0003)
	if !ok {
		t.Fatal("PartsList read failed after concurrent installs")
	}
	if _, isSlice := got.([]uint16); !isSlice {
		t.Fatalf("PartsList = %T, want []uint16", got)
	}
}

// TestDescriptor_ProviderReadDoesNotCopyStaticLists pins that a read
// answered by a provider costs the same whether the static fallback list
// is empty or large. Every assembled endpoint installs providers, so
// materialising the static copy before the provider check allocated on
// each of the reads Apple Home drives after CASE and then discarded the
// result.
func TestDescriptor_ProviderReadDoesNotCopyStaticLists(t *testing.T) {
	build := func(t *testing.T, staticLen int) *core.Descriptor {
		t.Helper()
		servers := make([]uint32, staticLen)
		parts := make([]uint16, staticLen)
		for i := range servers {
			servers[i] = uint32(0x1000 + i)
			parts[i] = uint16(1 + i)
		}
		d, err := core.NewDescriptor(
			[]core.DeviceTypeStruct{{DeviceType: 0x000E, Revision: 1}},
			servers, []uint32{}, parts,
		)
		if err != nil {
			t.Fatalf("NewDescriptor: %v", err)
		}
		providedServers := []uint32{0x001D}
		providedParts := []uint16{7}
		d.SetServerListProvider(func() []uint32 { return providedServers })
		d.SetPartsListProvider(func() []uint16 { return providedParts })
		return d
	}

	for name, attrID := range map[string]uint32{"ServerList": 0x0001, "PartsList": 0x0003} {
		t.Run(name, func(t *testing.T) {
			empty := build(t, 0)
			large := build(t, 512)
			emptyAllocs := testing.AllocsPerRun(200, func() { _, _ = empty.MatterRead(attrID) })
			largeAllocs := testing.AllocsPerRun(200, func() { _, _ = large.MatterRead(attrID) })
			if largeAllocs > emptyAllocs {
				t.Errorf("%s read with a provider allocates %.0f with a 512-entry static list vs %.0f with an empty one — the static copy is built and thrown away",
					name, largeAllocs, emptyAllocs)
			}
		})
	}
}
