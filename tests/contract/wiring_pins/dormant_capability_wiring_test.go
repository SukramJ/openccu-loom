// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// This file guards against the "implemented-but-never-wired" bug class:
// a capability gate / setter / probe whose type, interface, and unit tests
// all exist, yet whose production call site is missing — so the feature
// silently never engages. The archetype was the Matter ACL gate, where
// CheckACL + the IM gates existed but the dispatcher's ACL source was never
// attached, leaving every stored ACL entry unenforced. Several more of the
// same shape were found in the same sweep (connectivity reconcile probe,
// ping-pong PONG correlation, BridgedDevice reachability, CASE-fabric and
// failsafe hooks, MQTT origin version).
//
// Each pin asserts the production call exists in the file that is supposed to
// make it. A green unit test for the capability is NOT enough — these pins
// fail the build the moment the wiring is deleted, even though the capability
// keeps passing its own tests. When a wiring site is intentionally moved,
// update the pin to the new file; when a capability is intentionally retired,
// delete its pin in the same change that removes the wiring.

// --- Matter ACL gate (the archetype) ---

// TestPin_ACLLister_AttachedInDaemon pins that the daemon attaches the
// store-backed ACL source to the Matter bridge. Without it the
// TopologyDispatcher has no ACL source, CheckACL fails open, and every
// AccessControl entry the controller writes is silently unenforced.
func TestPin_ACLLister_AttachedInDaemon(t *testing.T) {
	contract.MustFindMethodCall(t,
		"cmd/openccu-loom",
		"bridge", "AttachACLLister")
}

// TestPin_Dispatcher_SetACLLister_CalledInBridge pins that the bridge forwards
// the attached lister into the dispatcher gate. AttachACLLister without this
// forward would store the lister but leave CheckACL sourceless.
func TestPin_Dispatcher_SetACLLister_CalledInBridge(t *testing.T) {
	contract.MustFindMethodCall(t,
		"internal/north/matter/bridge/bridge.go",
		"dispatcher", "SetACLLister")
}

// --- D1: connectivity reconcile probe ---

// TestPin_ConnectivityProbe_WiredInHubWiring pins that the hub wiring installs
// the Interface.listInterfaces probe as the Reconciler's Connect source.
// reconcileConnectivity needs BOTH the Connectivity cache (already seeded) AND
// this probe; with the probe nil the slow-cadence connectivity-drift sync
// short-circuits and never fires.
func TestPin_ConnectivityProbe_WiredInHubWiring(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"internal/central/adapter/hub_wiring.go",
		"internal/central/adapter", "NewJSONRPCConnectivityProbe")
}

// --- D2: ping-pong PONG correlation ---

// TestPin_SetPingPongTracker_WiredInPingPongBus pins that the PONG-ingest hook
// is installed on the EventCoordinator. Without it inbound PONG callbacks are
// dropped and the keepalive watchdog never correlates a real PONG.
func TestPin_SetPingPongTracker_WiredInPingPongBus(t *testing.T) {
	contract.MustFindMethodCall(t,
		"internal/central/adapter/pingpong_wiring.go",
		"Events", "SetPingPongTracker")
}

// TestPin_RecordPong_WiredInPingPongBus pins that the PONG tracker routes to
// the per-interface client's RecordPong. Without this call RecordPing
// accumulates unmatched pending pings and mismatch/health detection is driven
// only by sweep timeouts, never by actual PONG correlation.
func TestPin_RecordPong_WiredInPingPongBus(t *testing.T) {
	contract.MustFindMethodCall(t,
		"internal/central/adapter/pingpong_wiring.go",
		"Client", "RecordPong")
}

// TestPin_NotifyCallback_WiredInCallbackHandler pins that the inbound callback
// handler stamps the per-client callback-liveness timestamp for every event.
// Without this call the timestamp is refreshed only on reconnect, so on a
// quiet CCU IsCallbackAlive goes stale 180 s after each reconnect, the
// check_connection watchdog declares the channel dead, and the daemon
// reconnects in an endless ~180 s loop. The behavioural end-to-end guard lives
// in tests/contract/callback_liveness_contract_test.go.
func TestPin_NotifyCallback_WiredInCallbackHandler(t *testing.T) {
	contract.MustFindMethodCall(t,
		"internal/central/adapter/callback_handlers.go",
		"Client", "NotifyCallback")
}

// TestPin_KeepaliveEnablesPingPong pins that the periodic keepalive probes
// with ping-pong tracking ON. The PONG-correlation wiring above is inert
// unless the keepalive actually records outbound pings — the prior bug was a
// fully wired tracker fed by a probe that passed handlePingPong=false.
func TestPin_KeepaliveEnablesPingPong(t *testing.T) {
	contract.MustFindMethodCall(t,
		"internal/central/jobs.go",
		"Client", "CheckConnectionAvailability")
}

// --- D3: BridgedDevice reachability ---

// TestPin_NotifyDeviceReachable_WiredInDaemon pins that the daemon forwards CCU
// device-availability transitions into the Matter bridge. Without it every
// bridged endpoint stays Reachable=true forever and a dead CCU device still
// shows online in Apple/Google Home.
func TestPin_NotifyDeviceReachable_WiredInDaemon(t *testing.T) {
	contract.MustFindMethodCall(t,
		"cmd/openccu-loom",
		"mb", "NotifyDeviceReachable")
}

// --- D4: stale CASE session teardown on NOC rotation ---

// TestPin_SetOnFabricUpdated_WiredInDaemon pins that the daemon wires the
// UpdateNOC hook that aborts other CASE sessions for the updated fabric.
// Without it a NOC rotation leaves stale sessions keyed to the old credential.
func TestPin_SetOnFabricUpdated_WiredInDaemon(t *testing.T) {
	contract.MustFindMethodCall(t,
		"cmd/openccu-loom",
		"opCreds", "SetOnFabricUpdated")
}

// --- D5: aborted-CASE dedupe-map reaper ---

// TestPin_SetOnEvict_WiredInDaemon pins that the CASE provider's TTL reaper is
// wired to forget the bridge's Sigma1 dedupe entry. Without it every aborted
// Sigma1-only handshake leaks one map entry for the daemon's lifetime.
func TestPin_SetOnEvict_WiredInDaemon(t *testing.T) {
	contract.MustFindMethodCall(t,
		"cmd/openccu-loom",
		"caseProvider", "SetOnEvict")
}

// --- D6: uncommissioned first-pairing window ---

// TestPin_SetFabricCounter_WiredInDaemon pins that AdministratorCommissioning
// learns the fabric count. Without it an uncommissioned bridge cannot detect
// it is uncommissioned and the 48-hour first-pairing window collapses to the
// 900-second cap.
func TestPin_SetFabricCounter_WiredInDaemon(t *testing.T) {
	contract.MustFindMethodCall(t,
		"cmd/openccu-loom",
		"admComm", "SetFabricCounter")
}

// --- D7: MQTT Discovery origin version ---

// TestPin_SetOriginVersion_WiredInDaemon pins that the daemon stamps the build
// version onto the MQTT Discovery origin block. Without it HA shows the bridge
// origin sw_version as its zero default.
func TestPin_SetOriginVersion_WiredInDaemon(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"cmd/openccu-loom",
		"internal/north/mqtt", "SetOriginVersion")
}

// --- Periodic client-data refresh ---

// TestPin_SetLoadAndRefreshFn_WiredInCCUWiring pins that the southbound wiring
// installs the data-point reload handler via wireLoadAndRefresh. Without it the
// registered central.refresh_client_data scheduler job (default 5 min) fails
// every tick with "LoadAndRefreshDataPointData not wired" and the
// push-event-first reconciliation safety net — the periodic
// fetch-all-device-data sweep — never runs. The call lives in hub_retry.go's
// wireLoadAndRefresh, shared by the boot path and the background hub-recovery
// path (so a transient boot-time WireHub failure self-heals).
func TestPin_SetLoadAndRefreshFn_WiredInCCUWiring(t *testing.T) {
	contract.MustFindMethodCall(t,
		"internal/central/adapter/hub_retry.go",
		"unit", "SetLoadAndRefreshFn")
}
