// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import "testing"

// TestConnectivityDataPointsNilBeforeSet verifies that ConnectivityDataPoints
// returns nil when SetConnectivity has not been called.
func TestConnectivityDataPointsNilBeforeSet(t *testing.T) {
	h := NewHub("ccu1")
	if got := h.ConnectivityDataPoints(); got != nil {
		t.Fatalf("expected nil before SetConnectivity, got %v", got)
	}
}

// TestConnectivityDataPointsRoundtrip verifies that SetConnectivity followed
// by ConnectivityDataPoints returns the same pointer.
func TestConnectivityDataPointsRoundtrip(t *testing.T) {
	h := NewHub("ccu1")
	c := NewConnectivity()
	got := h.SetConnectivity(c)
	if got != h {
		t.Fatal("SetConnectivity must return the Hub for chaining")
	}
	if dp := h.ConnectivityDataPoints(); dp != c {
		t.Fatalf("ConnectivityDataPoints = %v, want %v", dp, c)
	}
}

// TestConnectivityDataPointsDetach verifies that passing nil to SetConnectivity
// clears the stored pointer.
func TestConnectivityDataPointsDetach(t *testing.T) {
	h := NewHub("ccu1")
	c := NewConnectivity()
	h.SetConnectivity(c)
	h.SetConnectivity(nil)
	if got := h.ConnectivityDataPoints(); got != nil {
		t.Fatalf("expected nil after detach, got %v", got)
	}
}
