// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_FinalizeInit_CalledInDevicePipeline pins that device_pipeline.go
// calls Channel.FinalizeInit.  Removing that call would silently leave
// channels without their post-init computed fields populated. This is a
// method call on the per-device channel loop variable, so it is pinned by
// receiver + method name, not by package function.
func TestPin_FinalizeInit_CalledInDevicePipeline(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/device_pipeline.go",
		"ch", "FinalizeInit",
	)
}

// TestPin_OnConfigChanged_CalledInReloadDeviceConfig pins that device.go
// calls Channel.OnConfigChanged during ReloadDeviceConfig.  Dropping that
// call would silently leave derived data points stale after a MASTER-paramset
// reload.
func TestPin_OnConfigChanged_CalledInReloadDeviceConfig(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/model/device/device.go",
		"internal/model/device", "OnConfigChanged",
	)
}

// TestPin_RemoveChannel_CalledInCentral pins that central.go calls
// Device.RemoveChannel during RemoveDevice.  This ensures channel teardown
// (observer unsubscribe, custom-DP cleanup) is not accidentally dropped.
// This is a method call on the removed-device local variable, so it is
// pinned by receiver + method name, not by package function.
func TestPin_RemoveChannel_CalledInCentral(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/central.go",
		"dev", "RemoveChannel",
	)
}
