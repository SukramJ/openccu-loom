// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	bdbidef "thingfabric.dev/go-fabric/cluster/spec/bridgeddevicebasicinformation"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EMatterReachableFollowsTheCCU holds a bridged device's Reachable
// attribute against the CCU's own report, through the running daemon:
//
//  1. A device the CCU reports UNREACH is served unreachable, and served
//     reachable again once the CCU clears it. The node hears the flip only
//     through the reachability forward the composition root wires.
//  2. A device that is already unreachable when the daemon starts — the CCU
//     booting not ready, so the bridge binds the device only once the
//     central turns ready — is served unreachable from its first read. A
//     go-fabric device starts reachable and no flip will come for a state
//     that was already there, so only the reconciler's statement before the
//     node binds the device can make this true.
//
// Each ReachableChanged event is counted at the reconciler
// (TestReconcilerSetReachableReportsEachFlipOnce): the black-box surface
// lists no node-side events.
func TestE2EMatterReachableFollowsTheCCU(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{EnableMatter: true, StartCCUNotReady: true, AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	h.CCU().V().SetReady(true)
	awaitCentralReady(t, h, harness.CentralName)
	if n := h.EnableAllExposures(t); n == 0 {
		t.Fatal("the node offers no source to expose; the check would hold vacuously")
	}
	settledBridgeEndpoints(t, h, 5*time.Second, 90*time.Second)

	var (
		addr string
		eps  []uint16
	)
	for _, raw := range getJSONArray(t, h, "/api/v1/devices", "items") {
		dev, _ := raw.(map[string]any)
		a, _ := dev["address"].(string)
		if a == "" || !deviceAvailable(t, h, a) {
			continue
		}
		if e := publishedMatterEndpoints(t, h, a); len(e) > 0 {
			addr, eps = a, e
			break
		}
	}
	if addr == "" {
		t.Fatal("no available device has a published Matter endpoint")
	}
	t.Logf("device under test: %s, endpoints %v", addr, eps)
	allServe := func(want bool) func() bool {
		return func() bool {
			for _, ep := range eps {
				if got, ok := servedReachable(t, h, ep); !ok || got != want {
					return false
				}
			}
			return true
		}
	}
	if !allServe(true)() {
		t.Fatalf("%s is served unreachable before any UNREACH", addr)
	}

	// Phase 1 — the flip and back.
	if err := h.CCU().V().RPC().SetDeviceUnreachable(addr, true); err != nil {
		t.Fatalf("simulate UNREACH on %s: %v", addr, err)
	}
	if !waitFor(t, 30*time.Second, allServe(false)) {
		t.Fatalf("%s: Reachable still true after the CCU reported UNREACH", addr)
	}
	if err := h.CCU().V().RPC().SetDeviceUnreachable(addr, false); err != nil {
		t.Fatalf("clear UNREACH on %s: %v", addr, err)
	}
	if !waitFor(t, 30*time.Second, allServe(true)) {
		t.Fatalf("%s: Reachable still false after the CCU cleared UNREACH", addr)
	}

	// Phase 2 — unreachable before the bridge binds it.
	if err := h.CCU().V().RPC().SetDeviceUnreachable(addr, true); err != nil {
		t.Fatalf("simulate UNREACH on %s: %v", addr, err)
	}
	if !waitFor(t, 30*time.Second, allServe(false)) {
		t.Fatalf("%s: Reachable still true after the second UNREACH", addr)
	}
	h.CCU().V().SetReady(false)
	h.Restart(t)
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login after restart: %v", err)
	}
	h.CCU().V().SetReady(true)
	awaitCentralReady(t, h, harness.CentralName)
	settledBridgeEndpoints(t, h, 5*time.Second, 90*time.Second)
	if !waitFor(t, 30*time.Second, func() bool { return !deviceAvailable(t, h, addr) }) {
		t.Fatalf("the daemon reports %s available after the restart although the CCU holds it UNREACH; the seed check would hold vacuously", addr)
	}
	after := publishedMatterEndpoints(t, h, addr)
	if len(after) == 0 {
		t.Fatalf("%s has no published Matter endpoint after the restart", addr)
	}
	for _, ep := range after {
		got, ok := servedReachable(t, h, ep)
		if !ok {
			t.Errorf("%s endpoint %d serves no Reachable attribute", addr, ep)
			continue
		}
		if got {
			t.Errorf("%s endpoint %d: Reachable = true after a restart with the device UNREACH, want false", addr, ep)
		}
	}
}

// publishedMatterEndpoints are the endpoint numbers the Matter projection of
// the device at addr names as published.
func publishedMatterEndpoints(t *testing.T, h *harness.Harness, addr string) []uint16 {
	t.Helper()
	status, out := getProjection(t, h, "/api/v1/devices/"+addr+"/projection?plane=matter")
	if status != http.StatusOK || len(out.Planes) != 1 {
		t.Fatalf("%s: projection status %d, planes %+v", addr, status, out.Planes)
	}
	var eps []uint16
	for _, raw := range out.Planes[0].Entries {
		var e measurementEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("decode entry: %v", err)
		}
		if e.Verdict != "published" {
			continue
		}
		id, err := strconv.ParseUint(e.Identity["endpoint_id"], 10, 16)
		if err != nil {
			t.Fatalf("%s: published entry %d/%s names endpoint %q: %v", addr, e.Channel, e.Key, e.Identity["endpoint_id"], err)
		}
		eps = append(eps, uint16(id))
	}
	return eps
}

// servedReachable is the BridgedDeviceBasicInformation Reachable value
// endpoint ep serves; ok is false when it serves none.
func servedReachable(t *testing.T, h *harness.Harness, ep uint16) (reachable, ok bool) {
	t.Helper()
	raw, ok := h.EndpointAttribute(t, ep, bdbidef.ClusterID, bdbidef.AttrReachable)
	if !ok {
		return false, false
	}
	if err := json.Unmarshal(raw, &reachable); err != nil {
		t.Fatalf("endpoint %d: Reachable is not a bool: %s", ep, raw)
	}
	return reachable, true
}
