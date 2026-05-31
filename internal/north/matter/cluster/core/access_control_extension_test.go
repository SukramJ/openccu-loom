// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
)

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

	entries := []core.AccessControlExtensionEntry{
		{Data: []byte{0x01, 0x02, 0x03}, FabricIndex: 1},
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
	if !bytes.Equal(got[0].Data, []byte{0x01, 0x02, 0x03}) {
		t.Errorf("Extension Data = %v, want %v", got[0].Data, []byte{0x01, 0x02, 0x03})
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
