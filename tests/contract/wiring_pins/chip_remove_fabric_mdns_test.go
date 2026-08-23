// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_OnMDNSReannounce_WiredInDaemon pins that daemon.go wires an
// OnMDNSReannounce callback.  This hook is called when the operational
// mDNS advertisement must be refreshed (e.g. after a fabric update);
// missing the wiring would cause Apple Home and Google Home to lose
// discovery after the first fabric change.
func TestPin_OnMDNSReannounce_WiredInDaemon(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom",
		"internal/north/matter/cluster/core", "OnMDNSReannounce",
	)
}
