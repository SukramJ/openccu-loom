// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/pkg/matterport"
)

// encodeExtensionTLV returns a minimal well-formed TLV List — the shape
// AccessControl.Extension's Data field must decode as per matter.js
// AccessControlServer.ts:424-441 (extensionEntryValidator). Test helper
// for the entries validateAccessControlExtensionData is meant to accept.
func encodeExtensionTLV(t *testing.T) []byte {
	t.Helper()
	enc := tlv.NewEncoder()
	enc.StartList(tlv.AnonymousTag())
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer: %v", err)
	}
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encoder.Bytes: %v", err)
	}
	return b
}

// TestAccessControl_ExtensionAttributeNeverNil pins that AccessControl
// attribute 0x0001 (Extension) always returns a non-nil empty list, never
// nil/null.  Apple Home and chip-tool both strict-decode the Extension list
// as a TLV Array; a null value triggers an IM StatusResponse INVALID_ACTION
// and Apple tears the fabric down via RemoveFabric after the initial
// Subscribe-Initial.
//
// Mirrors the chip AccessControl cluster server implementation and
// matter.js AccessControlServer.ts which always encodes an empty TLV
// Array for the Extension attribute when no vendor extension is configured.
func TestAccessControl_ExtensionAttributeNeverNil(t *testing.T) {
	t.Parallel()

	ac := newAccessControl(t)

	// Direct MatterRead (no fabric context).
	v, ok := ac.MatterRead(0x0001)
	if !ok {
		t.Fatal("Extension (0x0001): ok=false — attribute must be present")
	}
	if v == nil {
		t.Fatal("Extension (0x0001): MatterRead returned nil — must be an empty list, not null")
	}

	// Verify it is a slice type (non-nil empty slice), not any scalar.
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice {
		t.Fatalf("Extension (0x0001): type=%T, want slice", v)
	}
	// Must not be nil slice — nil encodes as TLV Null on the wire, empty
	// slice encodes as TLV Array{} which is what Apple expects.
	if rv.IsNil() {
		t.Fatal("Extension (0x0001): slice is nil — must be a non-nil (possibly empty) slice")
	}
}

// TestAccessControl_ExtensionAttributeNeverNil_FabricFiltered runs the
// same check through the MatterReadFiltered path (CASE session context)
// to confirm that the fabric-filter dispatch does not accidentally convert
// the empty list to nil on the way through.
func TestAccessControl_ExtensionAttributeNeverNil_FabricFiltered(t *testing.T) {
	t.Parallel()

	ac := newAccessControl(t)
	ctx := im.WithFabricFilter(context.Background(), true, 1)

	v, ok := ac.MatterReadFiltered(ctx, 0x0001)
	if !ok {
		t.Fatal("Extension via MatterReadFiltered: ok=false")
	}
	if v == nil {
		t.Fatal("Extension via MatterReadFiltered: returned nil — must be non-nil empty list")
	}

	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice {
		t.Fatalf("Extension via MatterReadFiltered: type=%T, want slice", v)
	}
	if rv.IsNil() {
		t.Fatal("Extension via MatterReadFiltered: slice is nil — must be non-nil empty list")
	}
	if rv.Len() != 0 {
		t.Fatalf("Extension via MatterReadFiltered: len=%d, want 0 (no vendor extensions configured)", rv.Len())
	}
}

// TestAccessControl_Extension_WriteAndReadBack verifies that writing to
// Extension (0x0001) persists the entries and MatterRead returns them back.
func TestAccessControl_Extension_WriteAndReadBack(t *testing.T) {
	t.Parallel()

	ac := newAccessControl(t)
	ctx := im.WithFabricFilter(context.Background(), true, 1)

	data := encodeExtensionTLV(t)
	entries := []core.AccessControlExtensionEntry{
		{Data: data, FabricIndex: 1},
	}
	if err := ac.MatterWrite(ctx, 0x0001, entries, 0); err != nil {
		t.Fatalf("MatterWrite Extension: %v", err)
	}

	v, ok := ac.MatterReadFiltered(ctx, 0x0001)
	if !ok {
		t.Fatal("MatterReadFiltered Extension after write: ok=false")
	}
	got, ok := v.([]core.AccessControlExtensionEntry)
	if !ok {
		t.Fatalf("Extension read-back type = %T, want []core.AccessControlExtensionEntry", v)
	}
	if len(got) != 1 {
		t.Fatalf("Extension read-back len = %d, want 1", len(got))
	}
	if !bytes.Equal(got[0].Data, data) {
		t.Errorf("Extension Data = %v, want %v", got[0].Data, data)
	}
}

// TestAccessControl_Extension_Write_InvalidTLV rejects a Data blob that
// does not decode as a well-formed TLV List — matter.js
// AccessControlServer.ts:424-441 (extensionEntryValidator) rejects the
// same shape with ConstraintError so a garbage write cannot wedge a
// later fabric-scoped read.
func TestAccessControl_Extension_Write_InvalidTLV(t *testing.T) {
	t.Parallel()

	ac := newAccessControl(t)
	entries := []core.AccessControlExtensionEntry{
		{Data: []byte{0x01, 0x02, 0x03}},
	}
	if err := ac.MatterWrite(context.Background(), 0x0001, entries, 0); err == nil {
		t.Fatal("MatterWrite Extension with non-TLV Data: want error, got nil")
	}
}

// TestAccessControl_Extension_Write_MoreThanOneEntryPerFabric rejects a
// write carrying more than one entry — every entry in a single write is
// stamped to the writer's own fabric, so more than one entry always
// means more than one entry for that fabric. Mirrors matter.js
// AccessControlServer.ts:352-370
// (#validateAccessControlExtensionChanges): "Extension list must
// contain a single entry" (ConstraintError).
func TestAccessControl_Extension_Write_MoreThanOneEntryPerFabric(t *testing.T) {
	t.Parallel()

	ac := newAccessControl(t)
	data := encodeExtensionTLV(t)
	entries := []core.AccessControlExtensionEntry{
		{Data: data},
		{Data: data},
	}
	if err := ac.MatterWrite(context.Background(), 0x0001, entries, 0); err == nil {
		t.Fatal("MatterWrite Extension with 2 entries: want error, got nil")
	}
}

// TestAccessControl_ExtensionWriteEmitsExtensionChanged verifies that a
// successful MatterWrite to the Extension attribute (0x0001) emits
// exactly one AccessControlExtensionChanged event (cluster 0x001F,
// event 0x0001) at priority Info, with ChangeType derived from the
// per-fabric list-length delta — mirroring
// TestAccessControl_ACLWriteEmitsEntryChanged for the sibling
// AccessControlEntryChanged event. Before this fix the event was
// declared in MatterEvents() but never emitted (the write path itself
// was unreachable — see TestAccessControl_Extension_Write_TooLarge's
// history).
func TestAccessControl_ExtensionWriteEmitsExtensionChanged(t *testing.T) {
	t.Parallel()

	ac := newAccessControl(t)
	ac.SetCurrentFabric(1)
	ac.SetEndpoint(0)

	emitter := &fakeEmitter{}
	ac.SetMatterEventEmitter(emitter)

	data := encodeExtensionTLV(t)

	// First write: 0 -> 1 entry => Added.
	if err := ac.MatterWrite(context.Background(), 0x0001, []core.AccessControlExtensionEntry{{Data: data}}, 0); err != nil {
		t.Fatalf("first MatterWrite: unexpected error: %v", err)
	}
	// Second write: 1 -> 1 entry => Changed.
	if err := ac.MatterWrite(context.Background(), 0x0001, []core.AccessControlExtensionEntry{{Data: data}}, 0); err != nil {
		t.Fatalf("second MatterWrite: unexpected error: %v", err)
	}
	// Third write: 1 -> 0 entries => Removed.
	if err := ac.MatterWrite(context.Background(), 0x0001, []core.AccessControlExtensionEntry{}, 0); err != nil {
		t.Fatalf("third MatterWrite: unexpected error: %v", err)
	}

	emitter.mu.Lock()
	got := append([]recordedEvent(nil), emitter.events...)
	emitter.mu.Unlock()

	wantChangeTypes := []uint8{
		core.AccessControlChangeTypeAdded,
		core.AccessControlChangeTypeChanged,
		core.AccessControlChangeTypeRemoved,
	}
	if len(got) != len(wantChangeTypes) {
		t.Fatalf("expected %d emitted events, got %d", len(wantChangeTypes), len(got))
	}
	for i, ev := range got {
		if ev.cluster != 0x001F {
			t.Errorf("event %d: cluster = 0x%04X, want 0x001F (AccessControl)", i, ev.cluster)
		}
		if ev.event != 0x0001 {
			t.Errorf("event %d: event = 0x%04X, want 0x0001 (AccessControlExtensionChanged)", i, ev.event)
		}
		if ev.priority != matterport.EventPriorityInfo {
			t.Errorf("event %d: priority = %v, want Info", i, ev.priority)
		}
		payload, ok := ev.data.(core.AccessControlExtensionChangedEvent)
		if !ok {
			t.Fatalf("event %d: data = %T, want AccessControlExtensionChangedEvent", i, ev.data)
		}
		if payload.ChangeType != wantChangeTypes[i] {
			t.Errorf("event %d: ChangeType = %d, want %d", i, payload.ChangeType, wantChangeTypes[i])
		}
		if payload.FabricIndex != 1 {
			t.Errorf("event %d: FabricIndex = %d, want 1", i, payload.FabricIndex)
		}
	}
}

// TestAccessControl_Extension_Write_TooLarge rejects entries with Data > 128 bytes.
func TestAccessControl_Extension_Write_TooLarge(t *testing.T) {
	t.Parallel()

	ac := newAccessControl(t)
	bigData := make([]byte, 129)
	entries := []core.AccessControlExtensionEntry{
		{Data: bigData},
	}
	if err := ac.MatterWrite(context.Background(), 0x0001, entries, 0); err == nil {
		t.Fatal("MatterWrite Extension with 129-byte Data: want error, got nil")
	}
}

// TestAccessControl_RemoveFabricExtension_PurgesEntry verifies that
// RemoveFabricExtension deletes the fabric's stored Extension entry and
// bumps DataVersion.
func TestAccessControl_RemoveFabricExtension_PurgesEntry(t *testing.T) {
	t.Parallel()

	ac := newAccessControl(t)
	ctx := im.WithFabricFilter(context.Background(), true, 1)
	data := encodeExtensionTLV(t)
	if err := ac.MatterWrite(ctx, 0x0001, []core.AccessControlExtensionEntry{{Data: data}}, 0); err != nil {
		t.Fatalf("MatterWrite Extension: %v", err)
	}
	before := ac.MatterDataVersion()

	ac.RemoveFabricExtension(1)

	v, ok := ac.MatterReadFiltered(ctx, 0x0001)
	if !ok {
		t.Fatal("MatterReadFiltered Extension after purge: ok=false")
	}
	got, ok := v.([]core.AccessControlExtensionEntry)
	if !ok {
		t.Fatalf("Extension read-back type = %T, want []core.AccessControlExtensionEntry", v)
	}
	if len(got) != 0 {
		t.Errorf("Extension entries after RemoveFabricExtension = %d, want 0", len(got))
	}
	if after := ac.MatterDataVersion(); after == before {
		t.Errorf("DataVersion did not change after RemoveFabricExtension: still %d", after)
	}
}

// TestAccessControl_RemoveFabricExtension_ReusedFabricIndexDoesNotInheritStaleData
// reproduces the scenario the finding named directly: controller A is
// commissioned on fabric index 1 and writes Extension metadata; the
// operator removes it; controller B is later commissioned and assigned
// the SAME fabric index (indices are reused — AddNOC allocates from the
// store's free indices). B must read back an empty Extension list, not
// A's leftover data.
func TestAccessControl_RemoveFabricExtension_ReusedFabricIndexDoesNotInheritStaleData(t *testing.T) {
	t.Parallel()

	ac := newAccessControl(t)
	ctxA := im.WithFabricFilter(context.Background(), true, 1)
	if err := ac.MatterWrite(ctxA, 0x0001, []core.AccessControlExtensionEntry{{Data: encodeExtensionTLV(t)}}, 0); err != nil {
		t.Fatalf("controller A MatterWrite Extension: %v", err)
	}

	// Controller A is unpaired: RemoveFabric's teardown purges fabric 1.
	ac.RemoveFabricExtension(1)

	// Controller B is commissioned and assigned the same fabric index 1.
	ctxB := im.WithFabricFilter(context.Background(), true, 1)
	v, ok := ac.MatterReadFiltered(ctxB, 0x0001)
	if !ok {
		t.Fatal("controller B MatterReadFiltered Extension: ok=false")
	}
	got, ok := v.([]core.AccessControlExtensionEntry)
	if !ok {
		t.Fatalf("controller B Extension read-back type = %T, want []core.AccessControlExtensionEntry", v)
	}
	if len(got) != 0 {
		t.Errorf("controller B inherited %d stale Extension entries from the removed controller A, want 0", len(got))
	}
}

// TestAccessControl_RemoveFabricExtension_NoEntryIsNoop verifies that
// purging a fabric with no stored Extension entry does not bump
// DataVersion — a subscriber must not see a spurious change report for
// a fabric that never wrote anything.
func TestAccessControl_RemoveFabricExtension_NoEntryIsNoop(t *testing.T) {
	t.Parallel()

	ac := newAccessControl(t)
	before := ac.MatterDataVersion()
	ac.RemoveFabricExtension(3)
	if after := ac.MatterDataVersion(); after != before {
		t.Errorf("DataVersion changed (%d -> %d) purging a fabric with no Extension entry", before, after)
	}
}
