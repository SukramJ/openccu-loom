// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_OpenWindow_PaseReject pins that AdministratorCommissioning.MatterInvoke
// contains the PASE-session pre-check before OpenCommissioningWindow. The check
// enforces that Multi-Admin commissioning windows can only be opened over a CASE
// session. Removing this guard would allow a PASE peer to open a commissioning
// window, violating Matter §11.19.8.1.
func TestPin_OpenWindow_PaseReject(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/north/matter/cluster/wire/admincommissioning.go",
		"internal/north/matter/im", "FabricFilterFromContext",
	)
}

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
