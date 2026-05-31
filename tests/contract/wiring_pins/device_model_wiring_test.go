// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_FinalizeInit_CalledInDevicePipeline pins that device_pipeline.go
// calls Channel.FinalizeInit.  Removing that call would silently leave
// channels without their post-init computed fields populated.
func TestPin_FinalizeInit_CalledInDevicePipeline(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/device_pipeline.go",
		"internal/model/device", "FinalizeInit",
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
func TestPin_RemoveChannel_CalledInCentral(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/central.go",
		"internal/model/device", "RemoveChannel",
	)
}
