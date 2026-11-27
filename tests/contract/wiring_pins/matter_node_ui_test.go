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
// selection takes effect on the next restart; leave NodeConfig unset and
// the status page shows no configuration. Every one of those reads as
// "Matter is off" or "the UI is slow", never as a wiring defect — which is
// why the wiring is pinned here, in the source the daemon ships.
func TestNodePageFamilyIsWiredThroughTheCompositionRoot(t *testing.T) {
	t.Parallel()

	matter := collapseSpaces(readMatterComposition(t))
	for _, want := range []struct{ what, expr string }{
		{"the node switch an erase sets", "Node: bundle.rt.nodes,"},
		{"the switch over the first node", "nodes: fabricui.NewNodeSwitch(matterNodeSource{n}),"},
		{"the erase", "Erase: bundle.rt.Erase,"},
		{"loom's exposure provider", "ExposureProvider: matteradapter.NewExposureProvider(matteradapter.ExposureProviderConfig{"},
		{"the selection's change report to the WebSocket hub", "Exposure: exposureEvents{sink: matterNodeEventSink(wsHub, auditRec),"},
		{"the candidate walk", "Candidates: candidates.MatterCandidates,"},
		{"the allowlist store", "Store: mids,"},
		{"the audit ledger", "Audit: auditRec,"},
		{"the reassemble hook", "Reassemble: matterReassembler(bundle.sync, auditRec, logger),"},
		{"the event source", "Events: bundle.events,"},
		{"the configuration the status page shows", "NodeConfig: matterNodeConfigView(mcfg),"},
		{"the assembler's findings as host warnings", "findings := matterWarnings(bundle.assembler, catalogs, cfg.Locale)"},
		{"the findings and the runtime's scrub warning", "return append(findings(ctx), bundle.rt.warnings(catalogs, cfg.Locale)...)"},
	} {
		if !strings.Contains(matter, want.expr) {
			t.Errorf("wireMatterRuntime never hands go-fabric-ui %s (%q)", want.what, want.expr)
		}
	}
	// The WebSocket hub is fed from the event source the SSE stream reads
	// and from the node itself: go-fabric-ui observes the node for each
	// open SSE stream alone, so the fabric and window transitions reach the
	// hub only through the bridge's own observer, which hangs on every node
	// the runtime brings up (matterRuntime.attach), an erase's included.
	if !strings.Contains(matter, "closers = append(closers, wireMatterNodeEvents(bundle.events, nil, wsHub, auditRec), bundle.rt.release)") {
		t.Error("the event source never reaches the WebSocket hub — the SPA hears no reassembly and no selection change")
	}
	if !strings.Contains(matter, "return wireMatterNodeEvents(nil, n, wsHub, auditRec)") {
		t.Error("the node events never reach the WebSocket hub — the SPA, which reads the hub rather than the SSE stream, hears no commissioning, no fabric removal and no window")
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
	// The node streams end when the server begins its shutdown: Done is
	// handed to go-fabric-ui and closed from the server's shutdown hook.
	// Without it an open stream holds the graceful shutdown until its
	// deadline (TestServerShutdownEndsOpenNodeStreams measures the effect).
	if !strings.Contains(mount, "deps.NodeUI.Done = nodeStreamsDone") ||
		!strings.Contains(mount, "restServer.RegisterOnShutdown(sync.OnceFunc(func() { close(nodeStreamsDone) }))") {
		t.Error("the node page family's Done is not closed from the REST server's shutdown hook — an open node stream holds every graceful shutdown until its deadline")
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
