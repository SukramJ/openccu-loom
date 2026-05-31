// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_EvaluateCentralState_CalledInCentralStart pins that central.go
// calls EvaluateCentralState during the Start sequence.  Without this call
// the initial state transition is never triggered and dependent subsystems
// never receive the first health-state event.
func TestPin_EvaluateCentralState_CalledInCentralStart(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/central.go",
		"internal/central", "EvaluateCentralState",
	)
}

// TestPin_IsAlive_CalledInEvaluateCentralState pins that central.go calls
// ClientCoordinator.IsAlive() inside EvaluateCentralState.  IsAlive is the
// gate that determines whether all BIN-RPC / XML-RPC callback connections
// are healthy; removing the call would make the state machine ignore
// connection loss.
func TestPin_IsAlive_CalledInEvaluateCentralState(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/central.go",
		"internal/central/coordinators", "IsAlive",
	)
}

// TestPin_SyncCentralState_CalledInCentral pins that central.go calls
// Health.SyncCentralState so that subsequent client-health transitions feed
// back into EvaluateCentralState.  Without this wiring the central would
// never react to interface reconnects.
func TestPin_SyncCentralState_CalledInCentral(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/central.go",
		"internal/health", "SyncCentralState",
	)
}
