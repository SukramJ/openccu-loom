// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package registry

import (
	"errors"
	"sync"
	"testing"
)

func TestCentralRegistry(t *testing.T) {
	r := NewCentralRegistry()
	if err := r.Register("main", "dummy"); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("main", "other"); !errors.Is(err, ErrCentralExists) {
		t.Fatalf("second register err=%v", err)
	}
	c, err := r.Get("main")
	if err != nil || c != "dummy" {
		t.Fatalf("Get=%v err=%v", c, err)
	}
	if _, err := r.Get("ghost"); !errors.Is(err, ErrCentralNotFound) {
		t.Fatalf("Get unknown err=%v", err)
	}
	if !r.Remove("main") {
		t.Fatal("Remove should report true")
	}
	if r.Remove("main") {
		t.Fatal("Remove should be idempotent false")
	}
}

func TestCentralRegistryNames(t *testing.T) {
	r := NewCentralRegistry()
	if names := r.Names(); len(names) != 0 {
		t.Fatalf("expected empty names, got %v", names)
	}
	_ = r.Register("beta", "b")
	_ = r.Register("alpha", "a")
	names := r.Names()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %v", names)
	}
	if names[0] != "alpha" || names[1] != "beta" {
		t.Fatalf("names not sorted: %v", names)
	}
}

func TestCentralRegistryNamesAfterRemove(t *testing.T) {
	r := NewCentralRegistry()
	_ = r.Register("x", "X")
	_ = r.Register("y", "Y")
	r.Remove("x")
	names := r.Names()
	if len(names) != 1 || names[0] != "y" {
		t.Fatalf("expected [y], got %v", names)
	}
}

func TestCentralRegistryConcurrent(t *testing.T) {
	r := NewCentralRegistry()
	const n = 20
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Register may return ErrCentralExists if same key is used, ignore.
			_ = r.Register("central", i)
			_ = r.Names()
			_, _ = r.Get("central")
		}(i)
	}
	wg.Wait()
}

func TestCentralRegistryLen(t *testing.T) {
	r := NewCentralRegistry()
	if r.Len() != 0 {
		t.Fatal("Len must be 0 for empty registry")
	}
	_ = r.Register("alpha", struct{}{})
	_ = r.Register("beta", struct{}{})
	if r.Len() != 2 {
		t.Fatalf("Len()=%d, want 2", r.Len())
	}
	r.Remove("alpha")
	if r.Len() != 1 {
		t.Fatalf("Len()=%d after Remove, want 1", r.Len())
	}
}

func TestCentralRegistryContains(t *testing.T) {
	r := NewCentralRegistry()
	if r.Contains("x") {
		t.Fatal("Contains must return false for unknown name")
	}
	_ = r.Register("x", struct{}{})
	if !r.Contains("x") {
		t.Fatal("Contains must return true after Register")
	}
	r.Remove("x")
	if r.Contains("x") {
		t.Fatal("Contains must return false after Remove")
	}
}

func TestCentralRegistryValues(t *testing.T) {
	r := NewCentralRegistry()
	if v := r.Values(); len(v) != 0 {
		t.Fatalf("Values on empty registry = %v, want empty", v)
	}
	_ = r.Register("beta", "ccuB")
	_ = r.Register("alpha", "ccuA")
	vals := r.Values()
	if len(vals) != 2 {
		t.Fatalf("Values()=%v, want 2 entries", vals)
	}
	// Must be sorted by name: alpha first, beta second.
	if vals[0] != "ccuA" || vals[1] != "ccuB" {
		t.Fatalf("Values()=%v, want [ccuA, ccuB] (sorted by name)", vals)
	}
}
