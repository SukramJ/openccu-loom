// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import "testing"

// TestNorthMatter_WithDefaults_ZeroValue verifies that every documented
// zero-value default is applied when the struct is entirely unset. This is
// the single defaulting point the bridge core, the commissioning-window
// opener, the mDNS advertisement, and the REST setup-payload endpoint all
// consume — a gap here would split the bridge identity across callers.
func TestNorthMatter_WithDefaults_ZeroValue(t *testing.T) {
	t.Parallel()

	var m NorthMatter
	got := m.WithDefaults()

	if got.VendorID != 0xFFF1 {
		t.Errorf("VendorID = 0x%04X, want 0xFFF1", got.VendorID)
	}
	if got.ProductID != 0x8000 {
		t.Errorf("ProductID = 0x%04X, want 0x8000", got.ProductID)
	}
	if got.NodeLabel != "openccu-loom" {
		t.Errorf("NodeLabel = %q, want %q", got.NodeLabel, "openccu-loom")
	}
	if got.Discriminator != 0xF00 {
		t.Errorf("Discriminator = 0x%04X, want 0xF00", got.Discriminator)
	}
	if got.MDNSAdvertise != "zeroconf" {
		t.Errorf("MDNSAdvertise = %q, want %q", got.MDNSAdvertise, "zeroconf")
	}
	if got.Commissioning.Iterations != 1000 {
		t.Errorf("Commissioning.Iterations = %d, want 1000", got.Commissioning.Iterations)
	}
}

// TestNorthMatter_WithDefaults_ExplicitValuesPreserved verifies that fields
// the operator already set survive WithDefaults unchanged — defaulting must
// only fill genuinely unset (zero-value) fields, never override a saved
// choice.
func TestNorthMatter_WithDefaults_ExplicitValuesPreserved(t *testing.T) {
	t.Parallel()

	m := NorthMatter{
		VendorID:      0x1234,
		ProductID:     0x5678,
		NodeLabel:     "my-bridge",
		Discriminator: 0x123,
		MDNSAdvertise: "noop",
		Commissioning: NorthMatterCommissioning{Iterations: 5000},
	}
	got := m.WithDefaults()

	if got.VendorID != 0x1234 {
		t.Errorf("VendorID = 0x%04X, want 0x1234 (unchanged)", got.VendorID)
	}
	if got.ProductID != 0x5678 {
		t.Errorf("ProductID = 0x%04X, want 0x5678 (unchanged)", got.ProductID)
	}
	if got.NodeLabel != "my-bridge" {
		t.Errorf("NodeLabel = %q, want %q (unchanged)", got.NodeLabel, "my-bridge")
	}
	if got.Discriminator != 0x123 {
		t.Errorf("Discriminator = 0x%04X, want 0x123 (unchanged)", got.Discriminator)
	}
	if got.MDNSAdvertise != "noop" {
		t.Errorf("MDNSAdvertise = %q, want %q (unchanged)", got.MDNSAdvertise, "noop")
	}
	if got.Commissioning.Iterations != 5000 {
		t.Errorf("Commissioning.Iterations = %d, want 5000 (unchanged)", got.Commissioning.Iterations)
	}
}

// TestNorthMatter_WithDefaults_DoesNotMutateReceiver verifies that
// WithDefaults returns a defaulted copy without mutating the original —
// the persisted config must keep its own unset fields on disk so operators
// keep seeing (and saving) their own values.
func TestNorthMatter_WithDefaults_DoesNotMutateReceiver(t *testing.T) {
	t.Parallel()

	m := NorthMatter{}
	_ = m.WithDefaults()

	if m.VendorID != 0 {
		t.Errorf("receiver VendorID mutated: got 0x%04X, want 0", m.VendorID)
	}
	if m.ProductID != 0 {
		t.Errorf("receiver ProductID mutated: got 0x%04X, want 0", m.ProductID)
	}
	if m.NodeLabel != "" {
		t.Errorf("receiver NodeLabel mutated: got %q, want empty", m.NodeLabel)
	}
	if m.Discriminator != 0 {
		t.Errorf("receiver Discriminator mutated: got 0x%04X, want 0", m.Discriminator)
	}
	if m.MDNSAdvertise != "" {
		t.Errorf("receiver MDNSAdvertise mutated: got %q, want empty", m.MDNSAdvertise)
	}
	if m.Commissioning.Iterations != 0 {
		t.Errorf("receiver Commissioning.Iterations mutated: got %d, want 0", m.Commissioning.Iterations)
	}
}
