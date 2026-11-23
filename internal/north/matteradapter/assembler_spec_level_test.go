// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"testing"

	fabricdevice "thingfabric.dev/go-fabric/device"
	"thingfabric.dev/go-fabric/surface"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// TestAssemble_UnbuildableSourceIsAFindingNotAnEndpoint pins that a source
// the device layer cannot build yields no endpoint, only a finding — there is
// no hand-built Spec standing in for it, so the node serves nothing no
// go-fabric device stands behind. The stub advertises Closure (0x0230), a
// device type levels below 1.5.1 do not know (go-fabric
// schema.DeviceTypeLevels), and serves none of its clusters: without a level
// the layer refuses the composition; at 1.4.2 the level withholds it first.
// The two are distinct findings, because one is the operator's choice and
// the other is not.
func TestAssemble_UnbuildableSourceIsAFindingNotAnEndpoint(t *testing.T) {
	for _, tc := range []struct {
		level surface.Level
		code  matteradapter.FindingCode
	}{
		{"", matteradapter.FindingDeviceRefused},
		{"1.4.2", matteradapter.FindingDeviceWithheld},
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
			// root + aggregator only
			if len(top.Endpoints) != 2 {
				t.Fatalf("endpoints = %d, want 2", len(top.Endpoints))
			}
			findings := a.Findings()
			if len(findings) != 1 || findings[0].Code != tc.code {
				t.Fatalf("findings = %+v, want one %q", findings, tc.code)
			}
		})
	}
}
