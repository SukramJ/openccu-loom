// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_WireDeviceRefreshedToBus_CalledInDevicePipeline pins that
// device_pipeline.go runs the device-level refresh bridge over every device
// it materialises. Without that call every generic data point still records
// its RefreshedAt and the REST `availability.LastRefreshed` still answers
// correctly, but no DeviceRefreshedEvent is ever published — the
// `device.refreshed` WebSocket frame goes silent and the SPA's "last
// refreshed" column freezes at whatever the page loaded with. The bridge's
// own behaviour (one throttled event per device, unchanged re-reads
// included) is asserted in internal/central/adapter/device_refreshed_wiring_test.go.
func TestPin_WireDeviceRefreshedToBus_CalledInDevicePipeline(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/device_pipeline.go",
		"", "wireDeviceRefreshedToBus",
	)
}

// TestPin_DeviceRefreshedSubscriber_StartedInDaemon pins that the
// composition root constructs the WebSocket relay for DeviceRefreshedEvent.
// The subscriber's Start/StartCentral/Stop contract is covered by the
// ws-package ownership table; this pin is what ties it to the running
// daemon — the bus event alone reaches no client.
func TestPin_DeviceRefreshedSubscriber_StartedInDaemon(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom/daemon_sysstatus.go",
		"internal/north/rest/ws", "NewDeviceRefreshedSubscriber",
	)
}
