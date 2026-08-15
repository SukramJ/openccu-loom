// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/matter/diagevent"
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

// TestAnAttachedRingIsWhatTheBridgeServes closes the other half: the
// setter has to reach the reader, or the REST surface would answer empty
// while the receive path recorded into a ring nobody serves.
func TestAnAttachedRingIsWhatTheBridgeServes(t *testing.T) {
	t.Parallel()

	ring := diagevent.NewRing(8)
	ring.Record(diagevent.Event{Kind: diagevent.KindPairing, Message: "recorded"})

	b := &bridge.Bridge{}
	b.AttachDiagnosticEvents(ring)

	got := b.DiagnosticEvents()
	if len(got) != 1 || got[0].Message != "recorded" {
		t.Fatalf("bridge served %v, want the attached ring's contents", got)
	}
}
