// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

func TestWireDescriptorPersistenceRoundTripsPutAndDelete(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, sqlite.FileDSN(filepath.Join(t.TempDir(), "descriptor.db")))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	unit, err := central.New(central.Config{Name: "ccu-desc"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}

	stores := DescriptorStores{
		Devices:   sqlite.NewDeviceStore(db),
		Paramsets: sqlite.NewParamsetStore(db),
	}

	// Empty database: hydration must report zero counts and still install
	// the sinks (asserted implicitly below via the subsequent Put/Add).
	if devices, paramsets := WireDescriptorPersistence(ctx, unit, stores, nil); devices != 0 || paramsets != 0 {
		t.Fatalf("hydration on empty db=(%d,%d), want (0,0)", devices, paramsets)
	}

	unit.DescRegistry.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address:  "VCU1",
		Type:     "HmIP-PS",
		Children: []string{"VCU1:1"},
	})
	unit.ParamsetReg.Add(hmenum.InterfaceHmIPRF, "VCU1:1", hmenum.ParamsetKeyValues, hmproto.Paramset{
		"STATE": {
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}, "HmIP-PS")

	devRec, err := stores.Devices.Get(ctx, "ccu-desc", "HmIP-RF", "VCU1")
	if err != nil {
		t.Fatalf("Devices.Get after Put: %v", err)
	}
	if devRec.Hash == "" {
		t.Error("persisted device record must carry a non-empty Hash")
	}
	if devRec.Description.Type != "HmIP-PS" {
		t.Errorf("persisted device Type=%q want HmIP-PS", devRec.Description.Type)
	}

	psRec, err := stores.Paramsets.Get(ctx, "ccu-desc", "HmIP-RF", "VCU1:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("Paramsets.Get after Add: %v", err)
	}
	if psRec.Hash == "" {
		t.Error("persisted paramset record must carry a non-empty Hash")
	}
	if _, ok := psRec.Paramset["STATE"]; !ok {
		t.Errorf("persisted paramset missing STATE: %+v", psRec.Paramset)
	}

	// Registry-side deletes must mirror through the sink into SQLite.
	if !unit.DescRegistry.Delete(hmenum.InterfaceHmIPRF, "VCU1") {
		t.Fatal("DescRegistry.Delete must report true for the existing entry")
	}
	if _, err := stores.Devices.Get(ctx, "ccu-desc", "HmIP-RF", "VCU1"); !errors.Is(err, sqlite.ErrDeviceNotFound) {
		t.Fatalf("Devices.Get after registry Delete: got %v, want ErrDeviceNotFound", err)
	}

	unit.ParamsetReg.DeleteChannel(hmenum.InterfaceHmIPRF, "VCU1:1")
	if _, err := stores.Paramsets.Get(ctx, "ccu-desc", "HmIP-RF", "VCU1:1", hmenum.ParamsetKeyValues); !errors.Is(err, sqlite.ErrParamsetNotFound) {
		t.Fatalf("Paramsets.Get after registry DeleteChannel: got %v, want ErrParamsetNotFound", err)
	}
}

// TestWireDescriptorPersistenceWarmBootHydratesAndCreatesDevice simulates a
// daemon restart: a first Unit persists a description + paramset through the
// sink, then a second, freshly-constructed Unit against the SAME database
// hydrates from the store and materialises the device via
// [coordinators.DeviceCoordinator.CheckAndCreateDevicesFromCache] — the
// warm-boot path WireDescriptorPersistence exists to serve.
func TestWireDescriptorPersistenceWarmBootHydratesAndCreatesDevice(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, sqlite.FileDSN(filepath.Join(t.TempDir(), "warmboot.db")))
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stores := DescriptorStores{
		Devices:   sqlite.NewDeviceStore(db),
		Paramsets: sqlite.NewParamsetStore(db),
	}

	unit1, err := central.New(central.Config{Name: "ccu-warm"})
	if err != nil {
		t.Fatalf("central.New (unit1): %v", err)
	}
	if devices, paramsets := WireDescriptorPersistence(ctx, unit1, stores, nil); devices != 0 || paramsets != 0 {
		t.Fatalf("first-boot hydration=(%d,%d), want (0,0) for an empty db", devices, paramsets)
	}

	unit1.DescRegistry.Put(hmenum.InterfaceHmIPRF, hmproto.DeviceDescription{
		Address:  "VCU2",
		Type:     "HmIP-PS",
		Children: []string{"VCU2:1"},
	})
	unit1.ParamsetReg.Add(hmenum.InterfaceHmIPRF, "VCU2:1", hmenum.ParamsetKeyValues, hmproto.Paramset{
		"STATE": {
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}, "HmIP-PS")

	// Second boot: a fresh Unit (empty in-memory registries) against the
	// same on-disk store — the scenario after a daemon restart.
	unit2, err := central.New(central.Config{Name: "ccu-warm"})
	if err != nil {
		t.Fatalf("central.New (unit2): %v", err)
	}
	devices, paramsets := WireDescriptorPersistence(ctx, unit2, stores, nil)
	if devices != 1 {
		t.Fatalf("warm-boot device hydration count=%d, want 1", devices)
	}
	if paramsets != 1 {
		t.Fatalf("warm-boot paramset hydration count=%d, want 1", paramsets)
	}

	if desc, ok := unit2.DescRegistry.Get(hmenum.InterfaceHmIPRF, "VCU2"); !ok || desc.Type != "HmIP-PS" {
		t.Fatalf("DescRegistry.Get after hydration=%+v ok=%v, want HmIP-PS", desc, ok)
	}
	if ps, ok := unit2.ParamsetReg.Get(hmenum.InterfaceHmIPRF, "VCU2:1", hmenum.ParamsetKeyValues); !ok || len(ps) == 0 {
		t.Fatalf("ParamsetReg.Get after hydration=%+v ok=%v, want a non-empty paramset", ps, ok)
	}

	if err := unit2.Devices.CheckAndCreateDevicesFromCache(ctx); err != nil {
		t.Fatalf("CheckAndCreateDevicesFromCache: %v", err)
	}
	if !unit2.DeviceRegistry.Has(hmenum.InterfaceHmIPRF, "VCU2") {
		t.Fatal("VCU2 must be materialised in DeviceRegistry after warm-boot cache restore")
	}
}
