// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestPrimaryClient_EmptyReturnsNil verifies PrimaryClient returns nil
// when no clients are registered.
func TestPrimaryClient_EmptyReturnsNil(t *testing.T) {
	t.Parallel()
	c := NewClientCoordinator()
	if got := c.PrimaryClient(); got != nil {
		t.Fatalf("PrimaryClient on empty coordinator = non-nil, want nil")
	}
}

// TestPrimaryClient_CandidateSetDefined verifies that primaryCandidateInterfaces
// contains the expected trio of preferred interfaces.
func TestPrimaryClient_CandidateSetDefined(t *testing.T) {
	t.Parallel()
	want := []hmenum.Interface{
		hmenum.InterfaceHmIPRF,
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
	}
	if len(primaryCandidateInterfaces) != len(want) {
		t.Fatalf("len(primaryCandidateInterfaces) = %d, want %d", len(primaryCandidateInterfaces), len(want))
	}
	for i, iface := range want {
		if primaryCandidateInterfaces[i] != iface {
			t.Errorf("primaryCandidateInterfaces[%d] = %v, want %v", i, primaryCandidateInterfaces[i], iface)
		}
	}
}

// TestPrimaryClient_FallbackToFirstOnDisconnected verifies that when no
// candidate is connected, PrimaryClient falls back to the first registered
// entry (nil Client → Connected() = false, so we reach the last fallback).
func TestPrimaryClient_FallbackToFirstOnDisconnected(t *testing.T) {
	t.Parallel()
	c := NewClientCoordinator()
	// Register two entries with nil Client (Connected() returns false).
	// Only CUxD (non-candidate); no candidates registered.
	_ = c.Register(&ClientEntry{InterfaceID: "CUxD.local", Interface: hmenum.InterfaceCUxD, Client: nil})
	// Must return the first entry's Client (nil) rather than panic.
	got := c.PrimaryClient()
	if got != nil {
		// nil Client is expected because none are connected.
		t.Fatalf("PrimaryClient = non-nil client, want nil (no connected clients)")
	}
}
