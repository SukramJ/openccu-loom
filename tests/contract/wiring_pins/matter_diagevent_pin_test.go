// SPDX-License-Identifier: MIT
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

// TestTheBridgeIsTheRESTSurfacesEventReporter: the pairing trace ring is
// attached by go-fabric's node facade before the bridge serves its first
// datagram (node.New, AttachDiagnosticEvents); what the daemon owns is
// exposing that bridge as the REST events reporter. A reporter built over
// anything else would serve an empty trace while the bridge records.
func TestTheBridgeIsTheRESTSurfacesEventReporter(t *testing.T) {
	src := collapseSpaces(readMatterComposition(t))
	if !strings.Contains(src, "wiring.diagEvents = mb") {
		t.Fatal("wireMatterRuntime does not expose the live bridge as the diagnostic events reporter")
	}
	mount := collapseSpaces(readRestMount(t))
	if !strings.Contains(mount, "MatterDiagnosticEvents: d.matter.diagEvents") {
		t.Fatal("the REST router never receives the diagnostic events reporter")
	}
}
