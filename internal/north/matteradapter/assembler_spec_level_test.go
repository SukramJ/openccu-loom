// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"testing"

	fabricdevice "github.com/SukramJ/go-fabric/device"
	"github.com/SukramJ/go-fabric/surface"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// TestAssemble_SpecLevelWithholdsAHandBuiltFallback pins that an endpoint
// the specification level withholds is not served through the hand-built
// fallback. A cluster-serving source is built through the device layer,
// and a composition the layer refuses falls back to a hand-built Spec;
// that Spec carries no surface report, so go-fabric's node would serve it
// past the level. The stub advertises Closure (0x0230), a device type
// levels below 1.5.1 do not know (go-fabric schema.DeviceTypeLevels),
// and serves none of its clusters: without a level the layer refuses it
// and the fallback serves it; at 1.4.2 the level withholds it first.
func TestAssemble_SpecLevelWithholdsAHandBuiltFallback(t *testing.T) {
	for _, tc := range []struct {
		level     surface.Level
		endpoints int
	}{
		// root + aggregator + the hand-built Closure
		{"", 3},
		// root + aggregator
		{"1.4.2", 2},
	} {
		t.Run("level="+string(tc.level), func(t *testing.T) {
			dev := newDevice("GAR0001", "Garage")
			ch := addChannel(dev, "GAR0001:1", 1)
			ch.SetCustomDataPoint(&stubEndpointSource{
				key:        dpKey("GAR0001:1", "DOOR_COMMAND"),
				deviceType: uint16(fabricdevice.ClosureID),
			})
			cfg := validConfig()
			cfg.Build.Surface = surface.Policies{Level: tc.level}
			a := matteradapter.New(cfg, nil)

			top, err := assembleDevicesWith(context.Background(), a, newFakeStore(), testEndpointConfig(),
				[]matteradapter.DeviceSnapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}})
			if err != nil {
				t.Fatalf("Assemble: %v", err)
			}
			if len(top.Endpoints) != tc.endpoints {
				t.Fatalf("endpoints = %d, want %d", len(top.Endpoints), tc.endpoints)
			}
		})
	}
}
