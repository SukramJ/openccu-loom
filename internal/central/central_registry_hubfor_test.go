// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import "testing"

// TestHubForRegistered verifies HubFor returns the Hub when the central is
// registered and its HubModel has been populated (which New always does).
func TestHubForRegistered(t *testing.T) {
	r := NewRegistry()
	c, err := New(Config{Name: "ccu1"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := r.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}
	h := r.HubFor("ccu1")
	if h == nil {
		t.Fatal("HubFor: expected non-nil Hub for registered central")
	}
}

// TestHubForUnknown verifies HubFor returns nil for an unregistered name.
func TestHubForUnknown(t *testing.T) {
	r := NewRegistry()
	if h := r.HubFor("ghost"); h != nil {
		t.Fatalf("HubFor: expected nil for unknown name, got %v", h)
	}
}

// TestHubForAfterUnregister verifies HubFor returns nil once the central is
// removed from the registry.
func TestHubForAfterUnregister(t *testing.T) {
	r := NewRegistry()
	c, _ := New(Config{Name: "ccu2"})
	_ = r.Register(c)
	r.Unregister("ccu2")
	if h := r.HubFor("ccu2"); h != nil {
		t.Fatalf("HubFor after Unregister: expected nil, got %v", h)
	}
}
