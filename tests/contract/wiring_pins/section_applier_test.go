// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_SectionApplier_WiredIntoTheRESTRouter pins that the daemon
// gives the config-save path something to apply a section with.
//
// Nil is a legal value for this seam and answers `applied: false`, which
// is why its absence is invisible: every save still returns 200, the
// section is still persisted, and the schema still — correctly — says
// north.mqtt needs no restart. What silently changes is that the running
// bridge keeps the topic base and plane toggles it was constructed with
// until someone restarts the daemon or finds POST /admin/mqtt/reload.
func TestPin_SectionApplier_WiredIntoTheRESTRouter(t *testing.T) {
	contract.MustFindStructLiteralField(
		t,
		"cmd/openccu-loom/daemon_rest_mount.go",
		"rest.Deps", "SectionApplier",
	)
}
