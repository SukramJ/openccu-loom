// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// With the firmware-check toggle off, an updatable device materialises
// as non-updatable so no firmware-update entity spawns; with it on
// (default) the updatable flag propagates.
func TestDevicePipelineFirmwareCheckGate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		firmwareCheck bool
		wantUpdatable bool
	}{
		{"enabled_default", true, true},
		{"disabled", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, _ := central.New(central.Config{Name: "ccu-01"})
			p := NewDevicePipeline(c).WithFirmwareCheck(tc.firmwareCheck)

			bt := true
			if err := p.Ingest(context.Background(), "HmIP-RF", hmenum.InterfaceHmIPRF, []hmproto.DeviceDescription{
				{Address: "0001ABCD", Type: "HmIP-STH", Firmware: "2.0", FirmwareUpdatable: &bt},
				{Address: "0001ABCD:0", Parent: "0001ABCD"},
			}); err != nil {
				t.Fatalf("ingest: %v", err)
			}
			d, ok := c.ModelRegistry.Get("0001ABCD")
			if !ok {
				t.Fatal("device missing")
			}
			if d.Updatable != tc.wantUpdatable {
				t.Fatalf("Updatable=%v, want %v (firmwareCheck=%v)", d.Updatable, tc.wantUpdatable, tc.firmwareCheck)
			}
			if got := d.Firmware().Info().Updatable; got != tc.wantUpdatable {
				t.Fatalf("Firmware.Updatable=%v, want %v", got, tc.wantUpdatable)
			}
		})
	}
}
