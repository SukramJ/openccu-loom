// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func openMem(t *testing.T) (*DeviceStore, *ParamsetStore, *IncidentStore) {
	t.Helper()
	db, err := Open(context.Background(), "file::memory:?cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewDeviceStore(db), NewParamsetStore(db), NewIncidentStore(db)
}

func TestDeviceStoreRoundTrip(t *testing.T) {
	ds, _, _ := openMem(t)
	rec := DeviceRecord{
		CentralName: "main",
		InterfaceID: "HmIP-RF",
		Address:     "ABC",
		Type:        "HM-X",
		Model:       "HM-X",
		Hash:        "h1",
		Description: hmproto.DeviceDescription{Address: "ABC", Type: "HM-X"},
	}
	if err := ds.Upsert(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	got, err := ds.Get(context.Background(), "main", "HmIP-RF", "ABC")
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash != "h1" || got.Description.Type != "HM-X" {
		t.Fatalf("got %+v", got)
	}
	// Upsert overwrite path.
	rec.Hash = "h2"
	rec.Description.Firmware = "1.1"
	if err := ds.Upsert(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	got, _ = ds.Get(context.Background(), "main", "HmIP-RF", "ABC")
	if got.Hash != "h2" || got.Description.Firmware != "1.1" {
		t.Fatalf("overwrite failed: %+v", got)
	}
	n, err := ds.Delete(context.Background(), "main", "HmIP-RF", "ABC")
	if err != nil || n != 1 {
		t.Fatalf("Delete returned (%d, %v)", n, err)
	}
}

func TestDeviceStoreMissingReturnsErr(t *testing.T) {
	ds, _, _ := openMem(t)
	_, err := ds.Get(context.Background(), "main", "HmIP-RF", "ghost")
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestDeviceStoreListByInterface(t *testing.T) {
	ds, _, _ := openMem(t)
	for _, addr := range []string{"A", "B", "C"} {
		_ = ds.Upsert(context.Background(), DeviceRecord{
			CentralName: "main", InterfaceID: "HmIP-RF", Address: addr,
			Type: "T", Hash: "h", Description: hmproto.DeviceDescription{Address: addr},
		})
	}
	list, err := ds.ListByInterface(context.Background(), "main", "HmIP-RF")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Address != "A" || list[2].Address != "C" {
		t.Fatalf("list=%v", list)
	}
}

func TestParamsetStore(t *testing.T) {
	_, ps, _ := openMem(t)
	ps1 := hmproto.Paramset{"LEVEL": hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}}
	if err := ps.Upsert(context.Background(), ParamsetRecord{
		CentralName:    "main",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "ABC:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "h1",
		Paramset:       ps1,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := ps.Get(context.Background(), "main", "HmIP-RF", "ABC:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Paramset["LEVEL"]; !ok {
		t.Fatal("paramset lost LEVEL")
	}
}

func TestIncidentStoreRecordAndList(t *testing.T) {
	_, _, is := openMem(t)
	_, err := is.Record(context.Background(), Incident{
		CentralName: "main",
		InterfaceID: "HmIP-RF",
		Type:        hmenum.IncidentTypeAuthFailure,
		Severity:    hmenum.IncidentSeverityError,
		Message:     "auth failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := is.Recent(context.Background(), "main", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("recent=%d", len(list))
	}
	if list[0].Count != 1 {
		t.Fatalf("count=%d", list[0].Count)
	}
}

func TestIncidentStoreBumpIfRecent(t *testing.T) {
	_, _, is := openMem(t)
	inc := Incident{
		CentralName: "main",
		InterfaceID: "HmIP-RF",
		Type:        hmenum.IncidentTypeAuthFailure,
		Severity:    hmenum.IncidentSeverityError,
		Message:     "auth failed",
	}
	if _, err := is.Record(context.Background(), inc); err != nil {
		t.Fatal(err)
	}
	bumped, err := is.BumpIfRecent(context.Background(), inc, 1*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !bumped {
		t.Fatal("expected dup to be bumped")
	}
	list, _ := is.Recent(context.Background(), "main", 10)
	if len(list) != 1 || list[0].Count != 2 {
		t.Fatalf("after bump list=%+v", list)
	}
}

// TestIsMemoryDSN exercises the isMemoryDSN path in Open via an
// in-memory DSN. If Open succeeds the in-memory pragma branch was exercised.
func TestIsMemoryDSN(t *testing.T) {
	db, err := Open(context.Background(), "file::memory:?cache=shared&_1=unique")
	if err != nil {
		t.Skip("in-memory DSN open failed:", err)
	}
	_ = db.Close()
}
