// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// baseDeviceCfg returns a minimal device.Config for tests.
func baseDeviceCfg() device.Config {
	return device.Config{
		InterfaceID:  "HmIP-RF.interface",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "VCU0000001",
		Model:        "HmIP-eTRV",
		Name:         "Test thermostat",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	}
}

// ─── IseID ───────────────────────────────────────────────────────────────────

// TestDeviceIseIDZeroByDefault verifies that IseID defaults to 0 when not set
// in Config.
func TestDeviceIseIDZeroByDefault(t *testing.T) {
	t.Parallel()

	d := device.New(baseDeviceCfg())
	if d.IseID != 0 {
		t.Errorf("IseID = %d, want 0 (default)", d.IseID)
	}
}

// TestDeviceIseIDSet verifies that IseID is propagated from Config to Device.
func TestDeviceIseIDSet(t *testing.T) {
	t.Parallel()

	cfg := baseDeviceCfg()
	cfg.IseID = 12345
	d := device.New(cfg)
	if d.IseID != 12345 {
		t.Errorf("IseID = %d, want 12345", d.IseID)
	}
}

// ─── IgnoreForCustomDataPoint ─────────────────────────────────────────────────

// TestDeviceIgnoreForCustomDataPointFalseByDefault verifies the zero value.
func TestDeviceIgnoreForCustomDataPointFalseByDefault(t *testing.T) {
	t.Parallel()

	d := device.New(baseDeviceCfg())
	if d.IgnoreForCustomDataPoint {
		t.Error("IgnoreForCustomDataPoint must be false by default")
	}
}

// TestDeviceIgnoreForCustomDataPointSet verifies that the flag is propagated
// from Config to Device.
func TestDeviceIgnoreForCustomDataPointSet(t *testing.T) {
	t.Parallel()

	cfg := baseDeviceCfg()
	cfg.IgnoreForCustomDataPoint = true
	d := device.New(cfg)
	if !d.IgnoreForCustomDataPoint {
		t.Error("IgnoreForCustomDataPoint must be true when set in Config")
	}
}

// ─── HasCustomDataPointDefinition ─────────────────────────────────────────────

// TestDeviceHasCustomDataPointDefinitionFalseByDefault verifies the zero value.
func TestDeviceHasCustomDataPointDefinitionFalseByDefault(t *testing.T) {
	t.Parallel()

	d := device.New(baseDeviceCfg())
	if d.HasCustomDataPointDefinition {
		t.Error("HasCustomDataPointDefinition must be false by default")
	}
}

// TestDeviceHasCustomDataPointDefinitionSet verifies that the flag is propagated.
func TestDeviceHasCustomDataPointDefinitionSet(t *testing.T) {
	t.Parallel()

	cfg := baseDeviceCfg()
	cfg.HasCustomDataPointDefinition = true
	d := device.New(cfg)
	if !d.HasCustomDataPointDefinition {
		t.Error("HasCustomDataPointDefinition must be true when set in Config")
	}
}

// ─── IgnoreOnInitialLoad ──────────────────────────────────────────────────────

// TestDeviceIgnoreOnInitialLoadFalseByDefault verifies the zero value.
func TestDeviceIgnoreOnInitialLoadFalseByDefault(t *testing.T) {
	t.Parallel()

	d := device.New(baseDeviceCfg())
	if d.IgnoreOnInitialLoad {
		t.Error("IgnoreOnInitialLoad must be false by default")
	}
}

// TestDeviceIgnoreOnInitialLoadSet verifies that the flag is propagated.
func TestDeviceIgnoreOnInitialLoadSet(t *testing.T) {
	t.Parallel()

	cfg := baseDeviceCfg()
	cfg.IgnoreOnInitialLoad = true
	d := device.New(cfg)
	if !d.IgnoreOnInitialLoad {
		t.Error("IgnoreOnInitialLoad must be true when set in Config")
	}
}

// ─── RxMode ──────────────────────────────────────────────────────────────────

// TestDeviceRxModeUndefinedByDefault verifies the zero value: a Config that
// never sets RxMode propagates hmenum.RxModeUndefined, matching a device the
// CCU reports no RX_MODE bitmask for.
func TestDeviceRxModeUndefinedByDefault(t *testing.T) {
	t.Parallel()

	d := device.New(baseDeviceCfg())
	if d.RxMode != hmenum.RxModeUndefined {
		t.Errorf("RxMode = %d, want RxModeUndefined (default)", d.RxMode)
	}
}

// TestDeviceRxModeSet verifies that RxMode is propagated verbatim from
// Config to Device, including a combined bitmask (WAKEUP | LAZY_CONFIG).
func TestDeviceRxModeSet(t *testing.T) {
	t.Parallel()

	cfg := baseDeviceCfg()
	cfg.RxMode = hmenum.RxModeWakeup | hmenum.RxModeLazyConfig
	d := device.New(cfg)
	if !d.RxMode.Has(hmenum.RxModeWakeup) || !d.RxMode.Has(hmenum.RxModeLazyConfig) {
		t.Errorf("RxMode = %d, want WAKEUP|LAZY_CONFIG set", d.RxMode)
	}
	if d.RxMode.Has(hmenum.RxModeAlways) {
		t.Errorf("RxMode = %d, must not carry ALWAYS", d.RxMode)
	}
}

// ─── Channel.IseID ───────────────────────────────────────────────────────────

// TestChannelIseIDDefaultsToZero verifies that a channel's IseID defaults to 0.
func TestChannelIseIDDefaultsToZero(t *testing.T) {
	t.Parallel()

	d := device.New(baseDeviceCfg())
	d.AddChannel("VCU0000001:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", "")
	ch := d.Channel("VCU0000001:1")
	if ch == nil {
		t.Fatal("channel VCU0000001:1 not found")
	}
	if ch.IseID() != 0 {
		t.Errorf("Channel.IseID = %d, want 0 (default)", ch.IseID())
	}
}

// TestChannelIseIDSet verifies that IseID can be set on a Channel directly.
func TestChannelIseIDSet(t *testing.T) {
	t.Parallel()

	d := device.New(baseDeviceCfg())
	d.AddChannel("VCU0000001:1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", "")
	ch := d.Channel("VCU0000001:1")
	if ch == nil {
		t.Fatal("channel VCU0000001:1 not found")
	}
	ch.SetIseID(99)
	if ch.IseID() != 99 {
		t.Errorf("Channel.IseID = %d, want 99 after SetIseID", ch.IseID())
	}
}
