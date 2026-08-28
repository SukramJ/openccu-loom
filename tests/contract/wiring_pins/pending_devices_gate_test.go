// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_PendingDevices_StoreConstructedInDaemon pins that the
// composition root actually builds the durable deferred-creation queue.
//
// Without it every other piece of this feature compiles, tests green and
// does nothing: the coordinator falls back to its in-memory queue, so an
// unaccepted device is materialised by the next boot's pull and its inbox
// entry disappears with the process — which is exactly the behaviour the
// gate was built to end, and it is invisible until someone restarts the
// daemon with a device still waiting.
func TestPin_PendingDevices_StoreConstructedInDaemon(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"cmd/openccu-loom/daemon.go",
		"", "NewPendingDeviceStore")
}

// TestPin_PendingDevices_RestoredBeforeBringUp pins that the queue is
// restored from the composition root's bring-up path.
//
// Order is the whole feature. The gated south-bound bring-up is the pull
// that materialises the fleet; restoring the queue afterwards would mean
// every held-back device is already in the model by the time anything
// could hold it back. buildAndStart is the shared entry point of both the
// boot path (WireCentrals) and runtime adoption (AddCentral), so pinning
// it here covers a central adopted while the daemon runs as well.
func TestPin_PendingDevices_RestoredBeforeBringUp(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"internal/central/adapter/central_bringup.go",
		"", "WirePendingDevices")
}

// TestPin_PendingDevices_SinkWiredIntoCoordinator pins that the wiring
// helper reaches the coordinator's seam. A helper that restores rows into
// a local variable and never hands them to the coordinator would satisfy
// the two pins above while the gate stayed shut.
func TestPin_PendingDevices_SinkWiredIntoCoordinator(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"internal/central/adapter/pending_device_persistence.go",
		"", "SetPendingDeviceSink")
}

// TestPin_PendingDevices_GateConsultedByThePull pins that the boot pull
// asks whether a device is held back.
//
// This is the load-bearing line: the queue can be persisted, restored and
// wired perfectly, and without this consultation IngestFromBackend still
// materialises everything. The symptom would be the feature appearing to
// work until a restart.
func TestPin_PendingDevices_GateConsultedByThePull(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"internal/central/adapter/device_pipeline.go",
		"", "withholdParked")
	contract.MustFindCallerInFile(t,
		"internal/central/adapter/device_pipeline.go",
		"", "IsParked")
}

// TestPin_DeferredCreationToggle_AppliedOnReload pins that a live config
// edit actually reaches the per-central handler.
//
// `delay_new_device_creation` is not classified restart-required, so the
// config surface tells the operator the edit is applied. It was read at
// exactly two points, both during bring-up — so before this the surface
// was lying, and switching the toggle off left every already-held device
// withheld from the ecosystems with nothing to explain why.
func TestPin_DeferredCreationToggle_AppliedOnReload(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"cmd/openccu-loom/reload.go",
		"", "ApplyDeferredCreationBehavior")
}

// TestPin_DeferredCreationToggle_ManagerBound pins the other half: the
// reload path can only reach the handles if the composition root hands
// them over. Without this the call above finds a nil manager and silently
// does nothing, which is indistinguishable from working.
func TestPin_DeferredCreationToggle_ManagerBound(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"cmd/openccu-loom/daemon.go",
		"", "SetBringUpManager")
}

// TestPin_ReleaseState_OnTheMCPSurface pins that an assistant driving the
// daemon sees the onboarding state too.
//
// MCP is the surface this repository's own rules warn gets forgotten,
// "because nothing fails when they are skipped" — and it was skipped when
// the release gate shipped: an assistant saw a withheld device exactly
// like any other.
func TestPin_ReleaseState_OnTheMCPSurface(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"internal/north/mcp/tools.go",
		"", "Released")
}
