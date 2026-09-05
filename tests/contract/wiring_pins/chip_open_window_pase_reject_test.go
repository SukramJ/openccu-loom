// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// The PASE-session pre-check in AdministratorCommissioning.MatterInvoke —
// which enforces that a Multi-Admin commissioning window can only be opened
// over a CASE session (Matter §11.19.8.1) — sits in
// github.com/SukramJ/go-fabric/cluster/wire and is no longer reachable by a
// source pin in this repository.

// TestPin_OpenWindow_FailSafeCheck pins that the daemon wires
// SetIsFailSafeArmed on the AdministratorCommissioning cluster, ensuring the
// FailSafe-disarmed pre-condition for OpenCommissioningWindow is active at
// runtime. Mirrors chip CommissioningWindowManager +
// AdministratorCommissioningCluster VerifyOrExit(IsFailSafeFullyDisarmed).
func TestPin_OpenWindow_FailSafeCheck(t *testing.T) {
	// SetIsFailSafeArmed is a method on the AdministratorCommissioning
	// cluster (the local `admComm` value), not a package-level function —
	// pin the method call directly.
	contract.MustFindMethodCall(
		t,
		"cmd/openccu-loom",
		"admComm", "SetIsFailSafeArmed",
	)
}
