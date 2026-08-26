// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	// OnFailSafeExpiry is a method on OperationalCredentials (the local
	// `gcOpCreds` value), not a package-level function — pin the method
	// call directly.
	contract.MustFindMethodCall(
		t,
		"cmd/openccu-loom",
		"gcOpCreds", "OnFailSafeExpiry",
	)
}
