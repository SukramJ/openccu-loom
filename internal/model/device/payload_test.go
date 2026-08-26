// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestInfoPayloadUsesAltNames(t *testing.T) {
	d := New(Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0001ABCD",
		Model:        "HmIP-STH",
		SubModel:     "revA",
		Name:         "Flur",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	info, ok := d.Info().(*payload.DeviceInfo)
	if !ok || info == nil {
		t.Fatalf("InfoPayload must return *payload.DeviceInfo, got %T", d.Info())
	}
	if info.Address != "0001ABCD" {
		t.Fatalf("serial_number (Address) missing: %+v", info)
	}
	if info.SubModel != "revA" {
		t.Fatalf("sub_model missing: %+v", info)
	}
	if info.ProductGroup != string(hmenum.ProductGroupHmIP) {
		t.Fatalf("product_group: %+v", info)
	}
	if info.Model != "HmIP-STH" {
		t.Fatalf("model: %+v", info)
	}
}

func TestConfigPayloadContainsUpdatable(t *testing.T) {
	d := New(Config{Address: "0001", Updatable: true})
	cfg, ok := d.Config().(*payload.DeviceConfig)
	if !ok || cfg == nil {
		t.Fatalf("ConfigPayload must return *payload.DeviceConfig, got %T", d.Config())
	}
	if !cfg.Updatable {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestStatePayloadReflectsAvailability(t *testing.T) {
	d := New(Config{Address: "0001", Firmware: FirmwareInfo{
		Current: "2.0.4", UpdateState: hmenum.DeviceFirmwareStateUpToDate,
	}})
	state, ok := d.State().(*payload.DeviceState)
	if !ok || state == nil {
		t.Fatalf("StatePayload must return *payload.DeviceState, got %T", d.State())
	}
	if !state.Available {
		t.Fatalf("available: %+v", state)
	}
	if state.Firmware != "2.0.4" {
		t.Fatalf("firmware: %+v", state)
	}
	if state.FirmwareUpdateState != string(hmenum.DeviceFirmwareStateUpToDate) {
		t.Fatalf("update state: %+v", state)
	}
}

func TestStatePayloadFlipsAfterForceUnreachable(t *testing.T) {
	d := New(Config{Address: "0001"})
	d.SetForcedAvailability(hmenum.ForcedDeviceAvailabilityForceFalse)
	state, ok := d.State().(*payload.DeviceState)
	if !ok || state == nil {
		t.Fatalf("StatePayload must return *payload.DeviceState")
	}
	if state.Available {
		t.Fatalf("force-false must flip availability")
	}
}
