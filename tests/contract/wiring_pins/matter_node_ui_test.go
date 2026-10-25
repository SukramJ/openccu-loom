// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestNodePageFamilyIsWiredThroughTheCompositionRoot pins the
// composition root's half of the Matter pages: go-fabric-ui's handler
// serves the node routes, and it serves them only as well as the options
// wireMatterRuntime fills. Leave Node unset and GET /node/status answers
// `enabled: false` on a running bridge; leave Exposure unset and the
// selection page is hidden; leave Events unset and no page hears a
// controller commissioning the node; leave Reassemble unset and a saved
// selection takes effect on the next restart. Every one of those reads as
// "Matter is off" or "the UI is slow", never as a wiring defect — which is
// why the wiring is pinned here, in the source the daemon ships.
func TestNodePageFamilyIsWiredThroughTheCompositionRoot(t *testing.T) {
	t.Parallel()

	matter := collapseSpaces(readMatterComposition(t))
	for _, want := range []struct{ what, expr string }{
		{"the node", "Node: bundle.node,"},
		{"loom's exposure provider", "Exposure: matteradapter.NewExposureProvider(matteradapter.ExposureProviderConfig{"},
		{"the candidate walk", "Candidates: candidates.MatterCandidates,"},
		{"the allowlist store", "Store: mids,"},
		{"the audit ledger", "Audit: auditRec,"},
		{"the reassemble hook", "Reassemble: matterReassembler(bundle.sync, auditRec),"},
		{"the event source", "Events: bundle.events,"},
	} {
		if !strings.Contains(matter, want.expr) {
			t.Errorf("wireMatterRuntime never hands go-fabric-ui %s (%q)", want.what, want.expr)
		}
	}
	// The node's own fabric and window transitions reach the pages only
	// through observers chained at node.New (go-fabric-ui
	// docs/embedding.md): a later registration does not exist.
	if !strings.Contains(matter, "Observers: uiEvents.Observers(node.Observers{}),") {
		t.Error("the node's observers are not go-fabric-ui's — a controller commissioning the bridge reaches no page and no WebSocket topic")
	}
	// The WebSocket hub is fed from the same source the SSE stream reads.
	if !strings.Contains(matter, "closers = append(closers, wireMatterNodeEvents(bundle.events, wsHub, auditRec))") {
		t.Error("the node events never reach the WebSocket hub — the SPA, which reads the hub rather than the SSE stream, sees none of them")
	}
	// A reassembly the handler did not trigger (a central becoming ready,
	// hot-plug) still announces itself.
	if !strings.Contains(matter, "bundle.events.Publish(fabricui.Event{Type: fabricui.EventEndpointsChanged") {
		t.Error("a reassembly outside the handler publishes no endpoints_changed — the endpoint list goes stale until the operator reloads")
	}

	mount := collapseSpaces(readRestMount(t))
	if !strings.Contains(mount, "NodeUI: d.matter.nodeUI,") {
		t.Error("the REST router never receives the node page family's options, so every node route answers as a disabled bridge however well the bridge itself is wired")
	}
	if !strings.Contains(mount, "IsAdmin: func(r *http.Request) bool {") || !strings.Contains(mount, "return ok && id.HasRole(auth.RoleAdmin)") {
		t.Error("the router's IsAdmin is not derived from the request's identity — go-fabric-ui's writes and the pairing codes are then either open to every caller or refused to every caller")
	}
	if !strings.Contains(mount, "Matter: d.matter.status,") {
		t.Error("the MCP status seam is not fed from the daemon's status adapter")
	}
}

func readMatterComposition(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, filepath.Join("cmd", "openccu-loom", "daemon_matter.go"))
}

func readRestMount(t *testing.T) string {
	t.Helper()
	return readRepoFile(t, filepath.Join("cmd", "openccu-loom", "daemon_rest_mount.go"))
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Join(filepath.Dir(thisFile), "..", "..", "..")
	src, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(src)
}

// collapseSpaces folds runs of spaces and tabs to a single space so a
// pin matches the code rather than gofmt's column alignment, which
// shifts whenever a longer field name joins the same struct literal.
func collapseSpaces(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	prevSpace := false
	for _, r := range src {
		if r == ' ' || r == '\t' {
			if !prevSpace {
				b.WriteByte(' ')
			}
			prevSpace = true
			continue
		}
		prevSpace = false
		b.WriteRune(r)
	}
	return b.String()
}
