// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_HubSetConnectivity_CalledInHubWiring pins that hub_wiring.go wires
// the per-interface reachability tracker via Hub.SetConnectivity.
// Without this call the Hub never receives interface health events. This
// is a method call on unit.HubModel, not a package function, so it is
// pinned by receiver + method name; HubModel is the distinctive part of
// the receiver expression and survives a rename of the enclosing unit
// variable.
func TestPin_HubSetConnectivity_CalledInHubWiring(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/hub_wiring.go",
		"HubModel", "SetConnectivity",
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
// This is a method call on unit.Hub, not a package function, so it is
// pinned by receiver + method name; Hub (the *coordinators.HubCoordinator
// field) is the distinctive part of the receiver expression.
func TestPin_SetProgramExecutor_CalledInHubWiring(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/hub_wiring.go",
		"Hub", "SetProgramExecutor",
	)
}

// TestPin_SetServiceMessageSuppressor_WiredInHubWiring pins that
// hub_wiring.go wires the durable service-message suppressor via
// HubCoordinator.SetServiceMessageSuppressor. Without this call
// permanent service-message suppression (`POST
// /service-messages/{id}/disable`, the ServiceMessages aggregate's
// Disable path) silently becomes a no-op instead of reaching the CCU's
// Interface.suppressServiceMessages. This is a method call on unit.Hub,
// not a package function, so it is pinned by receiver + method name.
func TestPin_SetServiceMessageSuppressor_WiredInHubWiring(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/hub_wiring.go",
		"Hub", "SetServiceMessageSuppressor",
	)
}

// TestPin_SetServiceMessageReader_WiredInHubWiring pins that hub_wiring.go
// wires the suppressed-parameter reader via
// HubCoordinator.SetServiceMessageReader so the management view can be
// reconciled against the CCU's live getSuppressedServiceMessages. This is
// a method call on unit.Hub, not a package function, so it is pinned by
// receiver + method name.
func TestPin_SetServiceMessageReader_WiredInHubWiring(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/hub_wiring.go",
		"Hub", "SetServiceMessageReader",
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

// TestPin_SetSysvarValueWriter_WiredInHubWiring pins that hub_wiring.go
// hands the JSON-RPC writer to HubCoordinator as the sysvar value path.
//
// It was missing for a whole release, and nothing noticed because the
// coordinator's own setter returned success without a writer. An operator
// who configured an alarm zone output of class `sysvar_mirror` got the
// system variable created — that runs through a separately wired creator
// — and never got its value written. The mirror's error branch never ran,
// so no log line appeared either; a CCU program reading that variable
// simply waited for a trigger that could not arrive.
//
// The sibling pin above (SetProgramExecutor) covers the same writer being
// handed over for programs. The two calls sit on adjacent lines, which is
// how the omission stayed plausible to a reader.
//
// This is a method call on unit.Hub, not a package function, so it is
// pinned by receiver + method name.
func TestPin_SetSysvarValueWriter_WiredInHubWiring(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/hub_wiring.go",
		"Hub", "SetSysvarValueWriter",
	)
}

// TestPin_LoadSysvars_UsesHubSysvarLookup pins that the sysvar load path looks
// an existing system variable up before writing it, so a refresh updates the
// data point in place. Replacing the entry instead would drop every observer
// reference held on it — the SPA and MQTT would keep a data point the model no
// longer owns, and stop seeing changes.
//
// Method call on the hub model; `h` is that model throughout the function, and
// the receiver matcher compares whole segments.
func TestPin_LoadSysvars_UsesHubSysvarLookup(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/hub_wiring.go",
		"h", "Sysvar",
	)
}
