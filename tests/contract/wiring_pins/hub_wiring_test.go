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

// TestPin_UpdateFirmwareUpdater_WiredOnHub pins that hub_wiring.go wires the
// Update firmware-updater via the guarded Update.SetFirmwareUpdater setter so
// that Update.Install() can trigger the CCU-side firmware update. Without it
// Install() always returns ErrNoFirmwareUpdater even when the Hub mutators are
// wired. (Guarded setter — not direct field assignment — so the background
// WireHub recovery can re-apply it without racing a concurrent Install.)
func TestPin_UpdateFirmwareUpdater_WiredOnHub(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/hub_wiring.go",
		"Update", "SetFirmwareUpdater",
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

// TestPin_SetServiceMessageSuppressor_WiredInHubWiring pins that
// hub_wiring.go wires the durable service-message suppressor via
// HubCoordinator.SetServiceMessageSuppressor. Without this call
// permanent service-message suppression (`POST
// /service-messages/{id}/disable`, the ServiceMessages aggregate's
// Disable path) silently becomes a no-op instead of reaching the CCU's
// Interface.suppressServiceMessages.
func TestPin_SetServiceMessageSuppressor_WiredInHubWiring(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/hub_wiring.go",
		"internal/central/coordinators", "SetServiceMessageSuppressor",
	)
}

// TestPin_SetServiceMessageReader_WiredInHubWiring pins that hub_wiring.go
// wires the suppressed-parameter reader via
// HubCoordinator.SetServiceMessageReader so the management view can be
// reconciled against the CCU's live getSuppressedServiceMessages.
func TestPin_SetServiceMessageReader_WiredInHubWiring(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/hub_wiring.go",
		"internal/central/coordinators", "SetServiceMessageReader",
	)
}

// TestPin_WireServiceMessageSuppressor_CalledInCcuWiring pins that the
// central bring-up (ccu_wiring.go) invokes WireServiceMessageSuppressor
// after the interface clients are registered. Without this call the
// suppressor is defined but never installed, leaving suppression
// unwired.
func TestPin_WireServiceMessageSuppressor_CalledInCcuWiring(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/ccu_wiring.go",
		"internal/central/adapter", "WireServiceMessageSuppressor",
	)
}
