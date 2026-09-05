// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"strings"
	"testing"

	"github.com/SukramJ/go-fabric/bridge"

	"github.com/SukramJ/openccu-loom/tests/contract"
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
//
// That the Bridge type itself forwards an attached ring's contents to
// DiagnosticEvents() is proven directly against the real constructor in
// internal/north/matter/bridge/bridge_diagnostic_events_test.go
// (TestDiagnosticRingAttachIsSafeWhileTheReceivePathRecords) — building
// a bare &bridge.Bridge{} and calling the setter on it here would only
// re-prove that same fact and never touch the composition root, which
// is the half that actually breaks when someone edits daemon_matter.go.
// This pin instead asserts what only the composition root can prove:
// that production calls the setter at all, and that it does so before
// Start — the ordering daemon_matter.go documents as load-bearing (the
// receive path reads the ring without the bridge lock, so attaching
// after Start races the serve goroutine and loses every pairing moment
// recorded up to that point).
func TestAnAttachedRingIsWhatTheBridgeServes(t *testing.T) {
	contract.MustFindMethodCall(t, "cmd/openccu-loom/daemon_matter.go", "bridge", "AttachDiagnosticEvents")

	src := readMatterComposition(t)
	attachIdx := strings.Index(src, "AttachDiagnosticEvents(")
	startIdx := strings.Index(src, "bridge.Start(ctx)")
	if attachIdx < 0 {
		t.Fatal("AttachDiagnosticEvents call not found in daemon_matter.go")
	}
	if startIdx < 0 {
		t.Fatal("bridge.Start(ctx) call not found in daemon_matter.go")
	}
	if attachIdx > startIdx {
		t.Errorf(
			"AttachDiagnosticEvents is wired after bridge.Start(ctx) in daemon_matter.go — " +
				"this races the serve goroutine (which reads the ring without the bridge lock) " +
				"and loses every pairing moment recorded before the attach",
		)
	}
}
