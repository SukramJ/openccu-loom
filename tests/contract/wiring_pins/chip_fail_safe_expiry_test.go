// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_FailSafeExpiry_RollsBackHalfPairedFabric pins that the daemon
// wires OnFailSafeExpiry (not just ClearPendingState) as the FailSafe-expired
// callback. OnFailSafeExpiry rolls back any half-paired fabric
// (pendingInstallFabricIndex) that AddNOC committed but CommissioningComplete
// never confirmed. Using ClearPendingState alone would leave the fabric slot
// occupied, causing FabricConflict on the next pair attempt.
// Mirrors chip CommissioningWindowManager::OnFailSafeTimerExpired.
func TestPin_FailSafeExpiry_RollsBackHalfPairedFabric(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom",
		"internal/north/matter/cluster/core", "OnFailSafeExpiry",
	)
}
