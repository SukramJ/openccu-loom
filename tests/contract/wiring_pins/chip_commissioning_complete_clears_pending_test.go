// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_CommissioningComplete_ClearsPendingFabric pins that the daemon
// wires SetOnCommissioningComplete so that a successful CommissioningComplete
// calls ClearPendingState on OperationalCredentials, which resets
// pendingInstallFabricIndex to zero. Without this hook a subsequent
// ArmFailSafe expiry would invoke revertAddNOC on an already-confirmed
// fabric and delete its ACL / GroupKey / Fabric row.
// Mirrors chip FailSafeContext::Reset() called on the CommissioningComplete
// success path in CommissioningWindowManager.
func TestPin_CommissioningComplete_ClearsPendingFabric(t *testing.T) {
	// SetOnCommissioningComplete is a method on the GeneralCommissioning
	// cluster (the local `gc` value), not a package-level function — pin
	// the method call directly.
	contract.MustFindMethodCall(
		t,
		"cmd/openccu-loom",
		"gc", "SetOnCommissioningComplete",
	)
}
