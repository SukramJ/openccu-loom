// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"testing"
)

// TestRegistrySerialSuffix verifies that SerialSuffix returns the correct
// per-CCU routing-key discriminator (last-10 chars lower-cased) and
// returns "" for unknown or not-yet-connected centrals.
func TestRegistrySerialSuffix(t *testing.T) {
	t.Parallel()

	r := NewRegistry()

	// Unknown central → empty string.
	if got := r.SerialSuffix("no-such-ccu"); got != "" {
		t.Fatalf("unknown central SerialSuffix = %q, want %q", got, "")
	}

	// Registered central with a full-length serial.
	c, err := New(Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := r.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}
	c.SetSystemInformation(SystemInfo{Serial: "3014F711A0001234"})
	if got, want := r.SerialSuffix("ccu-01"), "11a0001234"; got != want {
		t.Fatalf("SerialSuffix(full serial) = %q, want %q", got, want)
	}

	// Short serial — returned whole, lower-cased.
	c.SetSystemInformation(SystemInfo{Serial: "ABC"})
	if got, want := r.SerialSuffix("ccu-01"), "abc"; got != want {
		t.Fatalf("SerialSuffix(short serial) = %q, want %q", got, want)
	}
}

// TestRegistryCanonicalSerialPreservesCase pins the difference that made the
// HA integration re-discover an already-configured daemon on every restart:
// CanonicalSerial must return the exact string GET /system/ccu reports
// (case-preserved), where SerialSuffix lower-cases it for routing keys. The
// mDNS ccus= advertisement uses CanonicalSerial so it matches the config
// entry's unique_id (the /system/ccu serial) under HA's case-sensitive compare.
func TestRegistryCanonicalSerialPreservesCase(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	if got := r.CanonicalSerial("no-such-ccu"); got != "" {
		t.Fatalf("unknown central CanonicalSerial = %q, want %q", got, "")
	}

	c, err := New(Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := r.Register(c); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Upper-case-hex serial: the last 10 chars carry letters.
	c.SetSystemInformation(SystemInfo{Serial: "3014F711A0001F58"})
	canonical, suffix := r.CanonicalSerial("ccu-01"), r.SerialSuffix("ccu-01")
	if want := "11A0001F58"; canonical != want {
		t.Fatalf("CanonicalSerial = %q, want %q (case-preserved)", canonical, want)
	}
	if want := "11a0001f58"; suffix != want {
		t.Fatalf("SerialSuffix = %q, want %q (lower-cased)", suffix, want)
	}
	if canonical == suffix {
		t.Fatal("CanonicalSerial must differ from the lower-cased SerialSuffix for a letter-bearing serial")
	}
}

// TestRegistryUnregister verifies atomic remove with bool return.
func TestRegistryUnregister(t *testing.T) {
	r := NewRegistry()

	c1, _ := New(Config{Name: "ccuA"})
	c2, _ := New(Config{Name: "ccuB"})
	_ = r.Register(c1)
	_ = r.Register(c2)

	if got := r.Unregister("ccuA"); !got {
		t.Fatal("Unregister: expected true for registered name")
	}
	if _, ok := r.Get("ccuA"); ok {
		t.Fatal("Unregister: entry still present after remove")
	}
	// Second call is idempotent — returns false.
	if got := r.Unregister("ccuA"); got {
		t.Fatal("Unregister: expected false for already-removed name")
	}
	// ccuB is still present.
	if _, ok := r.Get("ccuB"); !ok {
		t.Fatal("Unregister: wrong entry removed")
	}
}

// TestRegistryUnregisterUnknown verifies that Unregister("nonexistent")
// returns false without panicking ( idempotency).
func TestRegistryUnregisterUnknown(t *testing.T) {
	r := NewRegistry()
	if got := r.Unregister("nope"); got {
		t.Fatal("expected false for unknown name")
	}
}

// TestRegistryLen verifies Len returns the current count.
func TestRegistryLen(t *testing.T) {
	r := NewRegistry()
	if r.Len() != 0 {
		t.Fatalf("empty registry Len=%d, want 0", r.Len())
	}
	c1, _ := New(Config{Name: "ccuA"})
	c2, _ := New(Config{Name: "ccuB"})
	_ = r.Register(c1)
	if r.Len() != 1 {
		t.Fatalf("after 1 register Len=%d, want 1", r.Len())
	}
	_ = r.Register(c2)
	if r.Len() != 2 {
		t.Fatalf("after 2 register Len=%d, want 2", r.Len())
	}
	r.Unregister("ccuA")
	if r.Len() != 1 {
		t.Fatalf("after unregister Len=%d, want 1", r.Len())
	}
}
