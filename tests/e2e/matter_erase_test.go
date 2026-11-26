// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// bootReasonSoftwareReset is GeneralDiagnostics BootReasonEnum
// SoftwareReset (go-fabric cluster/core, mattercore.BootReasonSoftwareReset).
const bootReasonSoftwareReset = 6

// TestE2EMatterEraseBringsAFreshNodeBack drives the maintenance page's
// erase through the built daemon. go-fabric's node is single-use, so the
// daemon has to bring a new node up on the erased store without a restart
// of its own:
//
//   - the status page offers the erase;
//   - after it the pages read a new node: it states BootReason
//     SoftwareReset, is unpaired with its own window open, and holds the
//     operator's selection again (the daemon's exposure rows survive);
//   - a restart afterwards boots on the erased store with the selection.
func TestE2EMatterEraseBringsAFreshNodeBack(t *testing.T) {
	h := harness.Start(t, harness.Options{EnableMatter: true, StartCCUNotReady: true, AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	h.CCU().V().SetReady(true)
	awaitCentralReady(t, h, harness.CentralName)

	if st := h.NodeStatus(t); !st.Erase {
		t.Fatal("GET /node/status reports no erase: the daemon wires none")
	}
	if n := h.EnableAllExposures(t); n == 0 {
		t.Fatal("the node offers no source to expose")
	}
	selected := len(h.NodeEndpoints(t))
	if selected == 0 {
		t.Fatal("the selection put no endpoint on the bridge; the comparison after the erase would compare nothing")
	}
	if st := h.NodeStatus(t); st.BootReason == nil || *st.BootReason == bootReasonSoftwareReset {
		t.Fatalf("boot_reason before the erase = %v; the check after it could not tell the nodes apart", st.BootReason)
	}

	h.EraseNode(t)

	st := h.NodeStatus(t)
	if st.BootReason == nil || *st.BootReason != bootReasonSoftwareReset {
		t.Fatalf("boot_reason after the erase = %v, want %d: the pages do not read a new node", st.BootReason, bootReasonSoftwareReset)
	}
	if !st.WindowOpen {
		t.Fatalf("the erased bridge has no window open (kind %q); a new, unpaired node opens its own", st.WindowKind)
	}
	if got := awaitEndpointCount(t, h, selected); got != selected {
		t.Fatalf("the new node holds %d bridged endpoints, want the %d selected before the erase", got, selected)
	}

	h.Restart(t)
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login after restart: %v", err)
	}
	awaitCentralReady(t, h, harness.CentralName)
	if got := awaitEndpointCount(t, h, selected); got != selected {
		t.Fatalf("after a restart on the erased store the node holds %d bridged endpoints, want %d", got, selected)
	}
	if st := h.NodeStatus(t); !st.WindowOpen {
		t.Fatal("after a restart on the erased store the unpaired node has no window open")
	}
}

// awaitEndpointCount polls the bridged endpoints until the node holds want
// of them or ten seconds passed, and returns the last count: a reassembly
// after the CCU's ready event runs debounced, off the request.
func awaitEndpointCount(t *testing.T, h *harness.Harness, want int) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		got := len(h.NodeEndpoints(t))
		if got == want || time.Now().After(deadline) {
			return got
		}
		time.Sleep(100 * time.Millisecond)
	}
}
