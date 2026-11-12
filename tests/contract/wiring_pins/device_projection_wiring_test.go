// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_DeviceProjection_WiredInDaemon pins that the daemon hands the
// device projector to both surfaces that answer with it. Each is mounted
// only when its field is set, so a dropped line removes the route or the
// tool without a compile error. The effect is asserted against the
// running daemon in tests/e2e/device_projection_test.go.
func TestPin_DeviceProjection_WiredInDaemon(t *testing.T) {
	for _, lit := range []string{"rest.Deps", "mcp.Deps"} {
		contract.MustFindStructLiteralField(t, "cmd/openccu-loom/daemon_rest_mount.go", lit, "Projection")
	}
}
