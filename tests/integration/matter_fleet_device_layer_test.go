// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"thingfabric.dev/go-fabric/endpoint"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestMatterWholeFleetBuildsThroughTheDeviceLayer assembles every device
// model godevccu embeds, with measurements and secondary channels on and no
// allow-list, and pins that each bridged endpoint is a go-fabric device and
// that no source of the fleet is refused.
//
// The device layer is the only way a source becomes an endpoint: a device
// type it lacks, or a composition it refuses, is withheld as a finding. A
// finding over this fleet is therefore a source of a real CCU device model
// that loom cannot serve, and the category and golden fleets are too small
// to meet most of them. The fleet once carried 59 such sources, every
// derived battery level, served as endpoints with device type 0.
func TestMatterWholeFleetBuildsThroughTheDeviceLayer(t *testing.T) {
	srv := startMockCCUWithDevices(t, nil) // nil: every embedded model
	backend := backends.NewCcuBackend(&xmlrpcBackendCaller{client: newXMLRPCClient(t, srv.URL())}, nil, nil)
	c, err := central.New(central.Config{Name: "fleet-ccu"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c).WithVisibility(visibility.NewRegistry())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := pipeline.IngestFromBackend(ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, slog.New(slog.DiscardHandler)); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	devs := c.ModelRegistry.List()

	cfg := smokeValidConfig(t)
	cfg.IncludeMeasurements = true
	cfg.ExposeSecondaryChannels = true
	a := matteradapter.New(cfg, slog.New(slog.DiscardHandler))
	top := assembleSmoke(t, a, newSmokeFakeStore(), smokeEndpointConfig(t), []matteradapter.DeviceSnapshot{{CentralName: "fleet-ccu", Devices: devs}})

	bridged := top.Bridged()
	// A fleet that failed to load would pass every check below without
	// judging anything. 2258 endpoints over 401 devices when this was written.
	if len(devs) < 300 || len(bridged) < 1500 {
		t.Fatalf("devices = %d, bridged endpoints = %d: the embedded fleet did not load", len(devs), len(bridged))
	}
	for _, ep := range bridged {
		if _, ok := ep.Source.(interface{ Spec() endpoint.Spec }); !ok {
			t.Errorf("endpoint %d (%s, device type 0x%04X): source %T is not a go-fabric device",
				ep.ID, ep.ChannelAddress, ep.DeviceType, ep.Source)
		}
		if ep.DeviceType == 0 {
			t.Errorf("endpoint %d (%s) has device type 0", ep.ID, ep.ChannelAddress)
		}
	}
	for _, f := range a.Findings() {
		t.Errorf("finding %s: %s (device type 0x%04X): %s", f.Code, f.Key, f.DeviceType, f.Err)
	}
}
