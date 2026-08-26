// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func freshDeviceStore(t *testing.T) *DeviceStore {
	t.Helper()
	return NewDeviceStore(openTestDB(t, "dev.db"))
}

func baseDeviceRecord(centralName, iface, addr string) DeviceRecord {
	return DeviceRecord{
		CentralName:  centralName,
		InterfaceID:  iface,
		Address:      addr,
		Type:         "HM-CC-RT-DN",
		Parent:       "",
		Firmware:     "1.4",
		Model:        "HM-CC-RT-DN",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHM,
		Hash:         "aabbcc",
		Description: hmproto.DeviceDescription{
			Address:   addr,
			Type:      "HM-CC-RT-DN",
			Firmware:  "1.4",
			Paramsets: []string{"MASTER", "VALUES"},
		},
	}
}

// TestDeviceStoreInsertAndGet verifies a full round-trip including all
// enum fields and the description_json blob.
func TestDeviceStoreInsertAndGet(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	rec := baseDeviceRecord("ccu1", "HmIP-RF", "ABC123")
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := s.Get(ctx, "ccu1", "HmIP-RF", "ABC123")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.CentralName != "ccu1" {
		t.Errorf("CentralName=%q want ccu1", got.CentralName)
	}
	if got.InterfaceID != "HmIP-RF" {
		t.Errorf("InterfaceID=%q want HmIP-RF", got.InterfaceID)
	}
	if got.Address != "ABC123" {
		t.Errorf("Address=%q want ABC123", got.Address)
	}
	if got.Manufacturer != hmenum.ManufacturerEQ3 {
		t.Errorf("Manufacturer=%q want %q", got.Manufacturer, hmenum.ManufacturerEQ3)
	}
	if got.ProductGroup != hmenum.ProductGroupHM {
		t.Errorf("ProductGroup=%q want %q", got.ProductGroup, hmenum.ProductGroupHM)
	}
	if got.Hash != "aabbcc" {
		t.Errorf("Hash=%q want aabbcc", got.Hash)
	}
	// description_json round-trip.
	if got.Description.Address != "ABC123" {
		t.Errorf("Description.Address=%q want ABC123", got.Description.Address)
	}
	if got.Description.Firmware != "1.4" {
		t.Errorf("Description.Firmware=%q want 1.4", got.Description.Firmware)
	}
	if len(got.Description.Paramsets) != 2 {
		t.Errorf("Paramsets len=%d want 2", len(got.Description.Paramsets))
	}
}

// TestDeviceStoreUpsertUpdatesExistingRow ensures the ON CONFLICT path
// updates all mutable columns without creating a duplicate.
func TestDeviceStoreUpsertUpdatesExistingRow(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	rec := baseDeviceRecord("ccu1", "HmIP-RF", "UPSERT1")
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("first upsert: %v", err)
	}

	// Mutate several fields and upsert again with the same PK.
	rec.Hash = "newHash"
	rec.Firmware = "2.0"
	rec.Description.Firmware = "2.0"
	rec.Manufacturer = hmenum.ManufacturerHB
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("second upsert: %v", err)
	}

	// Verify updated values.
	got, err := s.Get(ctx, "ccu1", "HmIP-RF", "UPSERT1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Hash != "newHash" {
		t.Errorf("Hash=%q want newHash", got.Hash)
	}
	if got.Firmware != "2.0" {
		t.Errorf("Firmware=%q want 2.0", got.Firmware)
	}
	if got.Manufacturer != hmenum.ManufacturerHB {
		t.Errorf("Manufacturer=%q want %q", got.Manufacturer, hmenum.ManufacturerHB)
	}
	if got.Description.Firmware != "2.0" {
		t.Errorf("Description.Firmware=%q want 2.0", got.Description.Firmware)
	}

	// Exactly one row must exist.
	list, err := s.ListByInterface(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("row count=%d want 1 (upsert created duplicate)", len(list))
	}
}

// TestDeviceStoreGetMissingReturnsErrDeviceNotFound checks nil-safe
// behaviour on a missing primary key.
func TestDeviceStoreGetMissingReturnsErrDeviceNotFound(t *testing.T) {
	s := freshDeviceStore(t)
	_, err := s.Get(context.Background(), "ccu1", "HmIP-RF", "GHOST")
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("got %v, want ErrDeviceNotFound", err)
	}
}

// TestDeviceStoreDelete verifies that Delete returns the correct
// row-count and that a subsequent Get returns ErrDeviceNotFound.
func TestDeviceStoreDelete(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	rec := baseDeviceRecord("ccu1", "HmIP-RF", "DEL1")
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	n, err := s.Delete(ctx, "ccu1", "HmIP-RF", "DEL1")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 1 {
		t.Errorf("RowsAffected=%d want 1", n)
	}

	// Deleting a non-existent row returns 0, not an error.
	n2, err := s.Delete(ctx, "ccu1", "HmIP-RF", "DEL1")
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if n2 != 0 {
		t.Errorf("second delete RowsAffected=%d want 0", n2)
	}

	// Get must now return ErrDeviceNotFound.
	if _, err := s.Get(ctx, "ccu1", "HmIP-RF", "DEL1"); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("after delete got %v, want ErrDeviceNotFound", err)
	}
}

// TestDeviceStoreListByInterfaceSortedByAddress verifies address-ascending
// ordering and interface isolation.
func TestDeviceStoreListByInterfaceSortedByAddress(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	// Insert three devices under "HmIP-RF" in reverse alphabetical order.
	for _, addr := range []string{"CCC", "AAA", "BBB"} {
		rec := baseDeviceRecord("ccu1", "HmIP-RF", addr)
		if err := s.Upsert(ctx, rec); err != nil {
			t.Fatalf("upsert %s: %v", addr, err)
		}
	}
	// One device on a different interface — must not appear in the result.
	other := baseDeviceRecord("ccu1", "BidCos-RF", "ZZZ")
	if err := s.Upsert(ctx, other); err != nil {
		t.Fatalf("upsert other: %v", err)
	}

	list, err := s.ListByInterface(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len=%d want 3", len(list))
	}
	want := []string{"AAA", "BBB", "CCC"}
	for i, rec := range list {
		if rec.Address != want[i] {
			t.Errorf("list[%d].Address=%q want %q", i, rec.Address, want[i])
		}
	}
}

// TestDeviceStoreListByInterfaceDescriptionJSON ensures that the
// description_json blob survives the list scan path (not just Get).
func TestDeviceStoreListByInterfaceDescriptionJSON(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	rec := baseDeviceRecord("ccu1", "HmIP-RF", "JSON1")
	rec.Description.Children = []string{"JSON1:0", "JSON1:1"}
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	list, err := s.ListByInterface(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d want 1", len(list))
	}
	got := list[0].Description
	if len(got.Children) != 2 {
		t.Errorf("Children=%v want [JSON1:0 JSON1:1]", got.Children)
	}
	if got.Children[0] != "JSON1:0" || got.Children[1] != "JSON1:1" {
		t.Errorf("Children=%v", got.Children)
	}
}

// TestDeviceStoreParentIndex verifies that the parent column is stored
// and round-trips; callers can use ListByInterface + filter by Parent to
// enumerate channels of a device.
func TestDeviceStoreParentIndex(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	device := baseDeviceRecord("ccu1", "HmIP-RF", "DEV0")
	if err := s.Upsert(ctx, device); err != nil {
		t.Fatalf("upsert device: %v", err)
	}

	// Two channels with Parent = "DEV0".
	for _, ch := range []string{"DEV0:0", "DEV0:1"} {
		rec := baseDeviceRecord("ccu1", "HmIP-RF", ch)
		rec.Parent = "DEV0"
		rec.Description.Parent = "DEV0"
		if err := s.Upsert(ctx, rec); err != nil {
			t.Fatalf("upsert channel %s: %v", ch, err)
		}
	}

	list, err := s.ListByInterface(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var channels []DeviceRecord
	for _, r := range list {
		if r.Parent == "DEV0" {
			channels = append(channels, r)
		}
	}
	if len(channels) != 2 {
		t.Fatalf("channel count=%d want 2", len(channels))
	}
	// Verify the parent round-trips through description_json as well.
	for _, ch := range channels {
		if ch.Description.Parent != "DEV0" {
			t.Errorf("Description.Parent=%q want DEV0", ch.Description.Parent)
		}
	}
}

// TestDeviceStoreMultiCCUIsolation verifies that (central_name) scopes
// correctly — a device inserted for "ccu1" must not appear under "ccu2".
func TestDeviceStoreMultiCCUIsolation(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	rec1 := baseDeviceRecord("ccu1", "HmIP-RF", "SHARED")
	rec2 := baseDeviceRecord("ccu2", "HmIP-RF", "SHARED")
	rec2.Firmware = "9.9"
	rec2.Hash = "different"

	for _, r := range []DeviceRecord{rec1, rec2} {
		if err := s.Upsert(ctx, r); err != nil {
			t.Fatalf("upsert: %v", err)
		}
	}

	got1, err := s.Get(ctx, "ccu1", "HmIP-RF", "SHARED")
	if err != nil {
		t.Fatalf("get ccu1: %v", err)
	}
	got2, err := s.Get(ctx, "ccu2", "HmIP-RF", "SHARED")
	if err != nil {
		t.Fatalf("get ccu2: %v", err)
	}

	if got1.Firmware == got2.Firmware {
		t.Errorf("ccu isolation broken: both have Firmware=%q", got1.Firmware)
	}
	if got2.Firmware != "9.9" {
		t.Errorf("ccu2 Firmware=%q want 9.9", got2.Firmware)
	}
}

// ---------------------------------------------------------------------------
// DeviceStore.Size
// ---------------------------------------------------------------------------

func TestDeviceStoreSize(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	n, err := s.Size(ctx, "ccu1")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if n != 0 {
		t.Errorf("Size on empty store=%d want 0", n)
	}

	for _, addr := range []string{"A", "B", "C"} {
		if err := s.Upsert(ctx, baseDeviceRecord("ccu1", "HmIP-RF", addr)); err != nil {
			t.Fatalf("Upsert %s: %v", addr, err)
		}
	}
	// Device for a different central must not be counted.
	if err := s.Upsert(ctx, baseDeviceRecord("ccu2", "HmIP-RF", "D")); err != nil {
		t.Fatalf("Upsert D: %v", err)
	}

	n, err = s.Size(ctx, "ccu1")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if n != 3 {
		t.Errorf("Size=%d want 3", n)
	}
}

// ---------------------------------------------------------------------------
// DeviceStore.FindDeviceDescription
// ---------------------------------------------------------------------------

func TestDeviceStoreFindDeviceDescriptionHit(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, baseDeviceRecord("ccu1", "HmIP-RF", "HIT1")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	rec, err := s.FindDeviceDescription(ctx, "ccu1", "HmIP-RF", "HIT1")
	if err != nil {
		t.Fatalf("FindDeviceDescription: %v", err)
	}
	if rec == nil {
		t.Fatal("FindDeviceDescription returned nil on existing record")
	}
	if rec.Address != "HIT1" {
		t.Errorf("Address=%q want HIT1", rec.Address)
	}
}

func TestDeviceStoreFindDeviceDescriptionMiss(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	rec, err := s.FindDeviceDescription(ctx, "ccu1", "HmIP-RF", "GHOST")
	if err != nil {
		t.Fatalf("FindDeviceDescription on miss: %v", err)
	}
	if rec != nil {
		t.Errorf("FindDeviceDescription on miss must return nil, got %+v", rec)
	}
}

// ---------------------------------------------------------------------------
// DeviceStore.GetAddresses
// ---------------------------------------------------------------------------

func TestDeviceStoreGetAddresses(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	for _, addr := range []string{"CCC", "AAA", "BBB"} {
		if err := s.Upsert(ctx, baseDeviceRecord("ccu1", "HmIP-RF", addr)); err != nil {
			t.Fatalf("Upsert %s: %v", addr, err)
		}
	}
	// Different interface must not appear.
	if err := s.Upsert(ctx, baseDeviceRecord("ccu1", "BidCos-RF", "ZZZ")); err != nil {
		t.Fatalf("Upsert ZZZ: %v", err)
	}

	addrs, err := s.GetAddresses(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("GetAddresses: %v", err)
	}
	if len(addrs) != 3 {
		t.Fatalf("len=%d want 3", len(addrs))
	}
	if addrs[0] != "AAA" || addrs[1] != "BBB" || addrs[2] != "CCC" {
		t.Errorf("addresses=%v want [AAA BBB CCC]", addrs)
	}
}

// ---------------------------------------------------------------------------
// DeviceStore.GetDeviceWithChannels
// ---------------------------------------------------------------------------

func TestDeviceStoreGetDeviceWithChannels(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	dev := baseDeviceRecord("ccu1", "HmIP-RF", "DEV1")
	if err := s.Upsert(ctx, dev); err != nil {
		t.Fatalf("Upsert device: %v", err)
	}
	for _, ch := range []string{"DEV1:0", "DEV1:1"} {
		rec := baseDeviceRecord("ccu1", "HmIP-RF", ch)
		rec.Parent = "DEV1"
		rec.Description.Parent = "DEV1"
		if err := s.Upsert(ctx, rec); err != nil {
			t.Fatalf("Upsert channel %s: %v", ch, err)
		}
	}
	// Unrelated device — must not appear.
	if err := s.Upsert(ctx, baseDeviceRecord("ccu1", "HmIP-RF", "OTHER")); err != nil {
		t.Fatalf("Upsert OTHER: %v", err)
	}

	result, err := s.GetDeviceWithChannels(ctx, "ccu1", "HmIP-RF", "DEV1")
	if err != nil {
		t.Fatalf("GetDeviceWithChannels: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("len=%d want 3 (device + 2 channels)", len(result))
	}
	if _, ok := result["DEV1"]; !ok {
		t.Error("device address DEV1 not in result")
	}
	if _, ok := result["DEV1:0"]; !ok {
		t.Error("channel DEV1:0 not in result")
	}
	if _, ok := result["DEV1:1"]; !ok {
		t.Error("channel DEV1:1 not in result")
	}
}

// ---------------------------------------------------------------------------
// DeviceStore.GetInterfaceIDs
// ---------------------------------------------------------------------------

func TestDeviceStoreGetInterfaceIDs(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	for _, iface := range []string{"HmIP-RF", "BidCos-RF", "HmIP-RF"} {
		if err := s.Upsert(ctx, baseDeviceRecord("ccu1", iface, "ADDR-"+iface)); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	// Different central — must not appear.
	if err := s.Upsert(ctx, baseDeviceRecord("ccu2", "VirtualDevices", "V1")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	ids, err := s.GetInterfaceIDs(ctx, "ccu1")
	if err != nil {
		t.Fatalf("GetInterfaceIDs: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("len=%d want 2 (distinct interfaces)", len(ids))
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen["HmIP-RF"] || !seen["BidCos-RF"] {
		t.Errorf("interfaces=%v want [HmIP-RF BidCos-RF]", ids)
	}
}

// ---------------------------------------------------------------------------
// DeviceStore.GetModel
// ---------------------------------------------------------------------------

func TestDeviceStoreGetModel(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	rec := baseDeviceRecord("ccu1", "HmIP-RF", "MDLADDR")
	rec.Model = "HmIP-SWDO"
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	model, err := s.GetModel(ctx, "ccu1", "MDLADDR")
	if err != nil {
		t.Fatalf("GetModel: %v", err)
	}
	if model != "HmIP-SWDO" {
		t.Errorf("GetModel=%q want HmIP-SWDO", model)
	}

	// Missing address returns empty string, no error.
	model2, err := s.GetModel(ctx, "ccu1", "GHOST")
	if err != nil {
		t.Fatalf("GetModel for ghost: %v", err)
	}
	if model2 != "" {
		t.Errorf("GetModel for ghost=%q want empty", model2)
	}
}

// ---------------------------------------------------------------------------
// DeviceStore.HasDeviceDescriptions
// ---------------------------------------------------------------------------

func TestDeviceStoreHasDeviceDescriptions(t *testing.T) {
	s := freshDeviceStore(t)
	ctx := context.Background()

	has, err := s.HasDeviceDescriptions(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("HasDeviceDescriptions: %v", err)
	}
	if has {
		t.Error("HasDeviceDescriptions on empty store must be false")
	}

	if err := s.Upsert(ctx, baseDeviceRecord("ccu1", "HmIP-RF", "X1")); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	has, err = s.HasDeviceDescriptions(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("HasDeviceDescriptions after insert: %v", err)
	}
	if !has {
		t.Error("HasDeviceDescriptions after insert must be true")
	}

	// Different interface must still return false.
	has2, err := s.HasDeviceDescriptions(ctx, "ccu1", "BidCos-RF")
	if err != nil {
		t.Fatalf("HasDeviceDescriptions other iface: %v", err)
	}
	if has2 {
		t.Error("HasDeviceDescriptions for interface with no records must be false")
	}
}

// ---------------------------------------------------------------------------
// DeviceStore.Clear
// ---------------------------------------------------------------------------

func TestDeviceStoreClearEmptyIsNoOp(t *testing.T) {
	t.Parallel()
	s := NewDeviceStore(openTestDB(t, "dev_clear_empty.db"))
	ctx := context.Background()

	n, err := s.Clear(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if n != 0 {
		t.Errorf("Clear on empty store returned %d, want 0", n)
	}
}

func TestDeviceStoreClearRemovesOnlyMatchingRows(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "dev_clear.db")
	s := NewDeviceStore(db)
	ctx := context.Background()

	upsert := func(centralName, iface, addr string) {
		t.Helper()
		err := s.Upsert(ctx, DeviceRecord{
			CentralName: centralName,
			InterfaceID: iface,
			Address:     addr,
			Type:        "DEVICE",
			Hash:        "h",
			Description: hmproto.DeviceDescription{},
		})
		if err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	upsert("ccu1", "HmIP-RF", "VCU0000001")
	upsert("ccu1", "HmIP-RF", "VCU0000002")
	upsert("ccu1", "BidCos-RF", "ABC0000001")
	upsert("ccu2", "HmIP-RF", "XYZ0000001")

	n, err := s.Clear(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if n != 2 {
		t.Errorf("Clear returned %d, want 2", n)
	}

	// BidCos-RF rows for ccu1 must still be present.
	ok, err := s.HasDeviceDescriptions(ctx, "ccu1", "BidCos-RF")
	if err != nil {
		t.Fatalf("HasDeviceDescriptions: %v", err)
	}
	if !ok {
		t.Error("HasDeviceDescriptions must return true for BidCos-RF after clearing HmIP-RF")
	}

	// ccu2 HmIP-RF rows must still be present.
	ok, err = s.HasDeviceDescriptions(ctx, "ccu2", "HmIP-RF")
	if err != nil {
		t.Fatalf("HasDeviceDescriptions ccu2: %v", err)
	}
	if !ok {
		t.Error("HasDeviceDescriptions must return true for ccu2 HmIP-RF after clearing ccu1 HmIP-RF")
	}

	// ccu1 HmIP-RF must now be empty.
	ok, err = s.HasDeviceDescriptions(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("HasDeviceDescriptions after clear: %v", err)
	}
	if ok {
		t.Error("HasDeviceDescriptions must return false for cleared interface")
	}
}

// TestDeviceStoreSizeHasDescriptionsClear verifies Size, HasDeviceDescriptions,
// and Clear together in a single store instance.
func TestDeviceStoreSizeHasDescriptionsClear(t *testing.T) {
	ds, _, _ := openMem(t)
	ctx := context.Background()

	// Insert a record.
	rec := DeviceRecord{
		CentralName: "ccu1", InterfaceID: "HmIP-RF", Address: "ABC",
		Type: "T", Hash: "h",
		Description: hmproto.DeviceDescription{Address: "ABC"},
	}
	if err := ds.Upsert(ctx, rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Size.
	n, err := ds.Size(ctx, "ccu1")
	if err != nil {
		t.Fatalf("Size: %v", err)
	}
	if n != 1 {
		t.Errorf("Size=%d, want 1", n)
	}

	// HasDeviceDescriptions.
	has, err := ds.HasDeviceDescriptions(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("HasDeviceDescriptions: %v", err)
	}
	if !has {
		t.Error("HasDeviceDescriptions must be true after insert")
	}
	hasNot, _ := ds.HasDeviceDescriptions(ctx, "ccu1", "no-such-iface")
	if hasNot {
		t.Error("HasDeviceDescriptions must be false for unknown interface")
	}

	// Clear.
	n2, err := ds.Clear(ctx, "ccu1", "HmIP-RF")
	if err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if n2 != 1 {
		t.Errorf("Clear removed %d rows, want 1", n2)
	}
	n3, _ := ds.Size(ctx, "ccu1")
	if n3 != 0 {
		t.Errorf("Size after Clear=%d, want 0", n3)
	}
}
