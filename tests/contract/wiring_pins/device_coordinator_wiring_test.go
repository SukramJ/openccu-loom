// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_IdentifyMissingDeviceDescriptions_CalledInHandleNewDevices pins
// that the DeviceCoordinator calls IdentifyMissingDeviceDescriptions during
// HandleNewDevices.  Without this call, new devices that lack descriptions
// are silently accepted without logging the gap.
func TestPin_IdentifyMissingDeviceDescriptions_CalledInHandleNewDevices(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/coordinators/device.go",
		"internal/central/coordinators", "IdentifyMissingDeviceDescriptions",
	)
}
