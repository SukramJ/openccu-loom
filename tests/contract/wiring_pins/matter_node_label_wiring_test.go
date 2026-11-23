// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestMatterNodeLabelIsWiredThroughTheCompositionRoot pins the composition
// root's half of the live node label. north.matter.node_label is no longer
// restart-required, so the config surface tells the operator a saved label
// is in effect; that is true only while the running node's labeler reaches
// both the config save (section applier) and the file watcher (reload
// deps). Drop either hand-over and the save answers "applied" for nothing,
// or the label waits for a restart behind a surface that says it does not.
// The effect is measured end to end by
// TestE2EMatterNodeLabelSaveReachesTheRunningBridge (tests/e2e, the built
// daemon) and in cmd/openccu-loom (TestMatterNodeLabelFollowsTheConfiguration,
// TestHotReloadHandlerAppliesAChangedNodeLabel); this pin names the lines in
// the ordinary test run, which does not build the daemon.
func TestMatterNodeLabelIsWiredThroughTheCompositionRoot(t *testing.T) {
	t.Parallel()

	matter := collapseSpaces(readMatterComposition(t))
	for _, want := range []struct{ what, expr string }{
		{"the labeler built over the started node", "nodeLabel := newMatterNodeLabeler(n, db, hostID.AppliedNodeLabel, logger)"},
		{"the label saved while the bridge was off, applied before Start", "nodeLabel.Apply(ctx, mc.NodeLabel)"},
		{"the pinned root serial", "rootSerial := hostID.RootSerial"},
		{"the labeler handed to the wiring", "wiring.nodeLabel = bundle.nodeLabel"},
	} {
		if !strings.Contains(matter, want.expr) {
			t.Errorf("startMatterBridge / wireMatterRuntime never wires %s (%q)", want.what, want.expr)
		}
	}

	daemon := collapseSpaces(readRepoFile(t, filepath.Join("cmd", "openccu-loom", "daemon.go")))
	if !strings.Contains(daemon, "deps.SetMatterNodeLabeler(matter.nodeLabel)") {
		t.Error("the reload deps never receive the node labeler — neither the config save nor the file watcher reaches the running node")
	}
	mount := collapseSpaces(readRestMount(t))
	if !strings.Contains(mount, "newSectionApplier(mqttReload, matterLabelSection{deps: d.reload, logger: logger}, logger)") {
		t.Error("the config save's section applier has no north.matter applier — a saved node_label is reported applied by nobody")
	}
}
