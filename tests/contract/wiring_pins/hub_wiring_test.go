// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_HubSetConnectivity_CalledInHubWiring pins that hub_wiring.go wires
// the per-interface reachability tracker via Hub.SetConnectivity.
// Without this call the Hub never receives interface health events.
func TestPin_HubSetConnectivity_CalledInHubWiring(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/hub_wiring.go",
		"internal/model/hub", "SetConnectivity",
	)
}

// TestPin_LoadSysvars_UsesHubSysvarLookup pins that loadSysvars in
// hub_wiring.go calls Hub.Sysvar() for in-place updates of existing
// sysvars.  Without this lookup, every CCU sync would reallocate all
// sysvar objects instead of updating them in place, breaking any
// existing observers that hold references.
func TestPin_LoadSysvars_UsesHubSysvarLookup(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/hub_wiring.go",
		"internal/model/hub", "Sysvar",
	)
}

// TestPin_UpdateFirmwareUpdater_WiredOnHub pins that hub_wiring.go sets
// Update.FirmwareUpdater so that Update.Install() can trigger the CCU-side
// firmware update. Without this assignment Install() always returns
// ErrNoFirmwareUpdater even when Hub.FirmwareUpdater is wired.
func TestPin_UpdateFirmwareUpdater_WiredOnHub(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/hub_wiring.go",
		"internal/model/hub", "FirmwareUpdater",
	)
}

// TestPin_SetProgramExecutor_CalledInHubWiring pins that hub_wiring.go calls
// HubCoordinator.SetProgramExecutor to wire the CCU-side program execution
// hook.  Without this call, program execution silently becomes a no-op.
func TestPin_SetProgramExecutor_CalledInHubWiring(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/hub_wiring.go",
		"internal/central/coordinators", "SetProgramExecutor",
	)
}
