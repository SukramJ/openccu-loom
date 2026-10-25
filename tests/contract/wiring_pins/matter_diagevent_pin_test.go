// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"strings"
	"testing"

	"github.com/SukramJ/go-fabric/bridge"
)

// TestABridgeWithoutARingStillServesAnEmptyTrace pins the property that
// lets the recording points sit where they have to sit.
//
// The record calls are on the Matter receive path, taken for every
// packet. A bridge assembled without a ring — a test, a build that never
// wires one — must read as "nothing recorded" rather than panicking
// mid-handshake. The nil-safety lives in the ring, but the bridge is
// what would carry the panic, so it is pinned from here.
func TestABridgeWithoutARingStillServesAnEmptyTrace(t *testing.T) {
	t.Parallel()

	b := &bridge.Bridge{}
	if got := b.DiagnosticEvents(); len(got) != 0 {
		t.Fatalf("an unwired bridge returned %d events", len(got))
	}
}

// TestTheNodeIsTheEventsRouteSource: the pairing trace ring is attached
// by go-fabric's node facade before the bridge serves its first datagram
// (node.New, AttachDiagnosticEvents); GET /api/v1/node/events is
// go-fabric-ui's, reading Node.DiagnosticEvents of the node the daemon
// hands it. What the daemon owns is handing over that node and no other
// — a route built over anything else would serve an empty trace while
// the bridge records. The hand-over is pinned with the rest of the node
// page family's options in matter_node_ui_test.go; this pin keeps the
// trace's own half: the facade's events are the bridge's.
func TestTheNodeIsTheEventsRouteSource(t *testing.T) {
	src := collapseSpaces(readMatterComposition(t))
	if !strings.Contains(src, "Node: bundle.node,") {
		t.Fatal("wireMatterRuntime does not hand go-fabric-ui the node whose bridge records the trace")
	}
}
