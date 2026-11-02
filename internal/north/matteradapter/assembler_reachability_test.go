// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// A bridged endpoint's Reachable follows the CCU device, at assembly and
// live afterwards. The device kit's own reachability starts true and only
// SetReachable moves it, so an endpoint left on the kit's probe reports an
// offline device as reachable to every controller.
func TestAssemble_BridgedReachabilityFollowsTheDevice(t *testing.T) {
	t.Parallel()
	dev := buildCustomDPDevice("AAA0001", "X", "RGBW_LIGHT", 0x0101)
	dev.SetForcedAvailability(hmenum.ForcedDeviceAvailabilityForceFalse)

	top, err := assembleDevicesWith(context.Background(), matteradapter.New(validConfig(), nil), newFakeStore(), testEndpointConfig(),
		[]matteradapter.DeviceSnapshot{{CentralName: "ccu1", Devices: []*device.Device{dev}}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	ep := findBridgedByAddress(top, "AAA0001")
	if ep == nil {
		t.Fatal("bridged endpoint missing")
	}
	if ep.Reachable {
		t.Error("Reachable = true at assembly for an offline device, want false")
	}
	if ep.Availability == nil || ep.Availability() {
		t.Error("the availability probe reports an offline device as reachable")
	}

	dev.SetForcedAvailability(hmenum.ForcedDeviceAvailabilityForceTrue)
	if !ep.Availability() {
		t.Error("the availability probe did not follow the device back online")
	}
}
