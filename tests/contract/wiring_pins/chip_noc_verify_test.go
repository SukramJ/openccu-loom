// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_NewVerifier_CalledInDaemon pins that daemon.go calls
// mattercert.NewVerifier when processing AddNOC operations.  The verifier
// validates the Node Operational Certificate chain; removing or skipping
// this call would allow incorrectly signed NOCs to be added silently,
// breaking Matter fabric security.
func TestPin_NewVerifier_CalledInDaemon(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom",
		"internal/north/matter/secure/mattercert", "NewVerifier",
	)
}
