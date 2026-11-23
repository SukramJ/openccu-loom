// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"encoding/json"
	"testing"

	basicdef "thingfabric.dev/go-fabric/cluster/spec/basicinformation"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EMatterNodeLabelSaveReachesTheRunningBridge drives
// north.matter.node_label through the built daemon the way the Web UI
// does — a north.matter section save — and reads the label the running
// node's root endpoint serves:
//
//   - the save answers applied and not restart-required, and the served
//     NodeLabel is the saved one at once, without a restart;
//   - after a restart the node serves the saved label still, and the root
//     SerialNumber is the one it served before, although the label it was
//     once derived from changed.
//
// It crosses every hand-over the composition root makes for the live label
// — the labeler into the reload deps, the section applier into the REST
// router — which the unit tests in cmd/openccu-loom construct themselves.
func TestE2EMatterNodeLabelSaveReachesTheRunningBridge(t *testing.T) {
	h := harness.Start(t, harness.Options{EnableMatter: true, StartCCUNotReady: true, AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	h.CCU().V().SetReady(true)
	awaitCentralReady(t, h, harness.CentralName)

	serial := rootString(t, h, basicdef.AttrSerialNumber)
	if serial == "" {
		t.Fatal("the root endpoint serves an empty SerialNumber")
	}
	const label = "Bridge Keller"
	if got := rootString(t, h, basicdef.AttrNodeLabel); got == label {
		t.Fatalf("the node serves %q before the save; the test cannot tell a save from the default", got)
	}

	applied, restart := h.SaveMatterSection(t, func(s map[string]any) { s["node_label"] = label })
	if !applied || restart {
		t.Fatalf("north.matter save with a changed node_label = applied %v, restart_required %v; want applied true, restart_required false",
			applied, restart)
	}
	if got := rootString(t, h, basicdef.AttrNodeLabel); got != label {
		t.Fatalf("served NodeLabel after the save = %q, want %q — the save does not reach the running node", got, label)
	}

	h.Restart(t)
	if got := rootString(t, h, basicdef.AttrNodeLabel); got != label {
		t.Fatalf("served NodeLabel after a restart = %q, want the saved %q", got, label)
	}
	if got := rootString(t, h, basicdef.AttrSerialNumber); got != serial {
		t.Fatalf("served SerialNumber after the label changed and the daemon restarted = %q, want the pinned %q", got, serial)
	}
}

// rootString reads a string attribute of the root BasicInformation cluster.
func rootString(t *testing.T, h *harness.Harness, attribute uint32) string {
	t.Helper()
	raw := h.RootAttribute(t, basicdef.ClusterID, attribute)
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("root BasicInformation attribute 0x%04X = %s, want a JSON string: %v", attribute, raw, err)
	}
	return s
}
