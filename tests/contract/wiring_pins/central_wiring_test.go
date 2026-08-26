// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
// Clients.IsAlive() inside EvaluateCentralState.  IsAlive is the gate that
// determines whether all BIN-RPC / XML-RPC callback connections are
// healthy; removing the call would make the state machine ignore
// connection loss. This is a method call on the Unit's Clients field, so
// it is pinned by receiver + method name, not by package function.
func TestPin_IsAlive_CalledInEvaluateCentralState(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/central.go",
		"Clients", "IsAlive",
	)
}

// TestPin_SyncCentralState_CalledInCentral pins that central.go calls
// Health.SyncCentralState so that subsequent client-health transitions feed
// back into EvaluateCentralState.  Without this wiring the central would
// never react to interface reconnects. This is a method call on the Unit's
// Health field, so it is pinned by receiver + method name, not by package
// function.
func TestPin_SyncCentralState_CalledInCentral(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/central.go",
		"Health", "SyncCentralState",
	)
}
