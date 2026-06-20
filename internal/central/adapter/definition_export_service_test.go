// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/device/definitionexport"
)

// TestDefinitionExportNilReceiver verifies that a nil *DefinitionExportDomain
// returns an error rather than panicking.
func TestDefinitionExportNilReceiver(t *testing.T) {
	t.Parallel()
	var d *DefinitionExportDomain
	_, _, err := d.ExportDefinition(context.Background(), "DEV001")
	if err == nil {
		t.Fatal("nil receiver must return an error")
	}
}

// TestDefinitionExportNilRegistry verifies that a domain built with a nil
// registry returns an error rather than panicking.
func TestDefinitionExportNilRegistry(t *testing.T) {
	t.Parallel()
	d := NewDefinitionExportDomain(nil)
	_, _, err := d.ExportDefinition(context.Background(), "DEV001")
	if err == nil {
		t.Fatal("nil registry must return an error")
	}
}

// TestDefinitionExportEmptyRegistry verifies that an empty registry (no centrals)
// returns ErrDeviceNotFound.
func TestDefinitionExportEmptyRegistry(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	d := NewDefinitionExportDomain(reg)
	_, _, err := d.ExportDefinition(context.Background(), "DEV001")
	if !errors.Is(err, definitionexport.ErrDeviceNotFound) {
		t.Fatalf("empty registry: err = %v, want ErrDeviceNotFound", err)
	}
}

// TestDefinitionExportDeviceNotInRegistry verifies that a central with no
// matching device returns ErrDeviceNotFound.
func TestDefinitionExportDeviceNotInRegistry(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	d := NewDefinitionExportDomain(reg)
	_, _, exportErr := d.ExportDefinition(context.Background(), "NOSUCHDEV")
	if !errors.Is(exportErr, definitionexport.ErrDeviceNotFound) {
		t.Fatalf("unknown device: err = %v, want ErrDeviceNotFound", exportErr)
	}
}

// TestDefinitionExportDeviceFoundNoClient verifies that when a device exists in
// the ModelRegistry but there is no matching InterfaceClient, the domain returns
// an error (not ErrDeviceNotFound — there is a different error for the missing
// client).
func TestDefinitionExportDeviceFoundNoClient(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-02"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}

	// Register a device so ModelRegistry.Get succeeds, but leave Clients empty
	// so clientForInterface returns nil.
	dev := device.New(device.Config{
		Address:     "DEV9999",
		InterfaceID: "HmIP-RF",
		Model:       "HmIP-PS",
	})
	c.ModelRegistry.Put(dev)

	d := NewDefinitionExportDomain(reg)
	_, _, exportErr := d.ExportDefinition(context.Background(), "DEV9999")
	// The device is found, but no client → must error (not ErrDeviceNotFound).
	if exportErr == nil {
		t.Fatal("device found without client: expected non-nil error")
	}
	if errors.Is(exportErr, definitionexport.ErrDeviceNotFound) {
		t.Fatalf("expected a 'no client' error, got ErrDeviceNotFound")
	}
}

// TestDefinitionExportMultiCentralWalksAll verifies that when the first central
// does not hold the device but the second does, the domain still returns
// ErrDeviceNotFound (because no client is wired — the walk itself is exercised).
func TestDefinitionExportMultiCentralWalksAll(t *testing.T) {
	t.Parallel()
	c1, err := central.New(central.Config{Name: "ccu-a"})
	if err != nil {
		t.Fatalf("central.New ccu-a: %v", err)
	}
	c2, err := central.New(central.Config{Name: "ccu-b"})
	if err != nil {
		t.Fatalf("central.New ccu-b: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c1); err != nil {
		t.Fatalf("reg.Register ccu-a: %v", err)
	}
	if err := reg.Register(c2); err != nil {
		t.Fatalf("reg.Register ccu-b: %v", err)
	}

	// Put the device only in c2; c1 stays empty.
	dev := device.New(device.Config{
		Address:     "MULTI001",
		InterfaceID: "HmIP-RF",
		Model:       "HmIP-WTH-2",
	})
	c2.ModelRegistry.Put(dev)

	d := NewDefinitionExportDomain(reg)
	_, _, exportErr := d.ExportDefinition(context.Background(), "MULTI001")
	// Device is found in c2 but no client is wired → "no client" error, not
	// ErrDeviceNotFound, proving the domain walked past c1 to c2.
	if exportErr == nil {
		t.Fatal("expected non-nil error (no client wired)")
	}
	if errors.Is(exportErr, definitionexport.ErrDeviceNotFound) {
		t.Fatalf("multi-central walk must reach c2 and return 'no client' error, got ErrDeviceNotFound")
	}
}
