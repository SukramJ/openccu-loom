// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestIngestFromBackendDropsDescriptionsTheCCUNoLongerReports pins the
// boot-time reconciliation of the persisted description cache against
// the live device list.
//
// A device unpaired while the daemon was down keeps its persisted
// descriptions; the warm-boot hydration puts them straight back into the
// registries, and the pull is add-only, so nothing removed them. The
// cached-device pass then re-registered the ghost in the device
// registry — and a later re-pair at the same serial was seen as
// "already known", so no DeviceCreatedEvent was published and no
// north-bound plane learned about the device until the next restart.
func TestIngestFromBackendDropsDescriptionsTheCCUNoLongerReports(t *testing.T) {
	t.Parallel()

	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	const ifaceID = "HmIP-RF"
	const live, ghost = "LIVEDEV01", "GHOSTDEV1"
	wire := hmtypes.ParseWireInterfaceID(ifaceID)

	// Warm-boot state: both devices restored from the persisted cache.
	for _, addr := range []string{live, ghost} {
		c.DescRegistry.Put(wire, hmproto.DeviceDescription{
			Address: addr, Type: "HmIP-BSM", Children: []string{addr + ":1"},
		})
		c.DescRegistry.Put(wire, hmproto.DeviceDescription{
			Address: addr + ":1", Parent: addr, Type: "SWITCH_VIRTUAL_RECEIVER",
		})
		c.DeviceRegistry.Put(registry.DeviceEntry{Interface: wire, Address: addr, Model: "HmIP-BSM"})
	}

	// The CCU now reports only the live device.
	b := &paramsetFakeOps{
		listDevicesFn: func(_ context.Context) ([]hmproto.DeviceDescription, error) {
			return []hmproto.DeviceDescription{
				{Address: live, Type: "HmIP-BSM", Children: []string{live + ":1"}},
				{Address: live + ":1", Parent: live, Type: "SWITCH_VIRTUAL_RECEIVER"},
			}, nil
		},
		getParamsetDescriptionFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
			return nil, nil
		},
		getParamsetFn: func(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
			return map[string]any{}, nil
		},
	}
	p := NewDevicePipeline(c).WithVisibility(newProductionVisibilityGate())
	if err := p.IngestFromBackend(
		t.Context(), ifaceID, hmenum.InterfaceHmIPRF, b, &fakeWriter{}, nil, slog.Default(),
	); err != nil {
		t.Fatalf("IngestFromBackend: %v", err)
	}

	if _, ok := c.DescRegistry.Get(wire, ghost); ok {
		t.Errorf("%s: root description survived a pull that does not report it", ghost)
	}
	if _, ok := c.DescRegistry.Get(wire, ghost+":1"); ok {
		t.Errorf("%s:1: channel description survived a pull that does not report it", ghost)
	}
	if _, ok := c.DeviceRegistry.Get(wire, ghost); ok {
		t.Errorf("%s: device-registry entry survived a pull that does not report it", ghost)
	}
	if _, ok := c.ModelRegistry.Get(ghost); ok {
		t.Errorf("%s: resurrected into the model from a stale description", ghost)
	}

	// The negative half: the live device must be untouched.
	if _, ok := c.DescRegistry.Get(wire, live); !ok {
		t.Errorf("%s: description of a device the CCU still reports was dropped", live)
	}
	if _, ok := c.ModelRegistry.Get(live); !ok {
		t.Errorf("%s: not materialised", live)
	}
}
