// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestNewWiresCacheSizeProviders pins the boot wiring between the two
// description registries and the cache coordinator's size metrics.
//
// The coordinator's own metrics tests call SetSizeProviders themselves, so
// they stayed green while no production path ever called it: every
// CacheMetricsSnapshot served on /api/v1/diagnostics reported
// device_descriptions and paramset_descriptions as 0 for the daemon's whole
// lifetime, and understated the snapshot's total by the same amount.
//
// It therefore goes through [New] alone and writes into the registries the
// real device pipeline writes into.
func TestNewWiresCacheSizeProviders(t *testing.T) {
	t.Parallel()

	u, err := New(Config{Name: "cache-size-wiring"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	iface := hmtypes.NewWireInterfaceID("cache-size-wiring", hmenum.InterfaceHmIPRF)
	u.DescRegistry.Put(iface, hmproto.DeviceDescription{Address: "VCU0000001", Type: "HmIP-PS"})
	u.ParamsetReg.Put(iface, "VCU0000001:1", hmenum.ParamsetKeyValues, hmproto.Paramset{
		"STATE": hmproto.ParameterData{Type: hmenum.ParameterTypeBool},
	})

	if got := u.Cache.MetricsDeviceDescriptionsSize(); got != 1 {
		t.Errorf("MetricsDeviceDescriptionsSize() = %d, want 1", got)
	}
	if got := u.Cache.MetricsParamsetDescriptionsSize(); got != 1 {
		t.Errorf("MetricsParamsetDescriptionsSize() = %d, want 1", got)
	}
}
