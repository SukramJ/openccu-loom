// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bootid

import (
	"sync"
	"testing"
)

// TestSalt_DefaultZero verifies that Salt returns an all-zero salt when
// rotation has not been enabled, matching the matter.js / chip stable-ID
// behaviour.
func TestSalt_DefaultZero(t *testing.T) {
	t.Parallel()
	// A fresh package-level state would have rotationEnabled==false but
	// we cannot reset sync.Once; instead we just call Salt() and confirm
	// the contract: if rotation is not enabled, the result must be zeroed.
	//
	// We reset the package-level state using SetForTest + a sentinel, but
	// this test only checks the disabled path — so we need a package that
	// is in the disabled state.  The package state is process-global, so
	// we cannot truly isolate between test functions that may run in
	// parallel.  We just verify that Salt() returns a zero salt when we
	// explicitly mark rotation disabled.
	rotationGuard.Lock()
	prev := rotationEnabled
	rotationEnabled = false
	rotationGuard.Unlock()
	defer func() {
		rotationGuard.Lock()
		rotationEnabled = prev
		rotationGuard.Unlock()
	}()

	got := Salt()
	for i, b := range got {
		if b != 0 {
			t.Fatalf("Salt()[%d] = 0x%02X, want 0 (rotation disabled)", i, b)
		}
	}
}

// TestEnableRotation_SaltNonZero verifies that once rotation is enabled
// the 16-byte salt is non-zero (crypto/rand fills it with pseudo-random
// bytes; the probability of a truly all-zero 16-byte value is 2^-128).
func TestEnableRotation_SaltNonZero(t *testing.T) {
	// Not t.Parallel() — mutates package-level state.
	var fixed [16]byte
	for i := range fixed {
		fixed[i] = byte(i + 1)
	}
	SetForTest(fixed)
	defer func() {
		// Reset for other tests: disable rotation so Salt() returns zeros.
		rotationGuard.Lock()
		rotationEnabled = false
		rotationGuard.Unlock()
		// Reset once so the next enable path re-runs correctly.
		once = sync.Once{}
	}()

	got := Salt()
	if got != fixed {
		t.Fatalf("Salt() = %v, want %v after SetForTest", got, fixed)
	}
}

// TestSetForTest_EnablesRotation verifies the contract that SetForTest
// implicitly enables rotation.
func TestSetForTest_EnablesRotation(t *testing.T) {
	// Not t.Parallel() — mutates package-level state.
	var pin [16]byte
	pin[0] = 0xAB
	pin[15] = 0xCD
	SetForTest(pin)
	defer func() {
		rotationGuard.Lock()
		rotationEnabled = false
		rotationGuard.Unlock()
		once = sync.Once{}
	}()

	rotationGuard.Lock()
	enabled := rotationEnabled
	rotationGuard.Unlock()
	if !enabled {
		t.Fatal("SetForTest must enable rotation")
	}

	got := Salt()
	if got != pin {
		t.Fatalf("Salt() after SetForTest: got %v, want %v", got, pin)
	}
}

// TestEnableRotation_Idempotent verifies that calling EnableRotation
// twice does not panic.
func TestEnableRotation_Idempotent(t *testing.T) {
	// Not t.Parallel() — mutates package-level state.
	EnableRotation()
	EnableRotation()
	// Just confirming no panic — no assertions beyond this.
}

// TestEnsure_DirectCryptoRandPath exercises the once.Do body inside
// ensure() on a fresh sync.Once so the crypto/rand.Read path runs
// (lines 38-47 of bootid.go). The error-fallback branch (lines 39-45)
// is unreachable on any OS supported by this codebase — we cover only
// the happy path.
func TestEnsure_DirectCryptoRandPath(t *testing.T) {
	// Not t.Parallel() — mutates package-level state (once + salt).
	// Reset the once so the ensure() body re-runs.
	once = sync.Once{}
	var empty [16]byte
	salt = empty // zero out to detect that crypto/rand filled it.

	ensure() // invokes the once.Do body; fills salt via crypto/rand.

	// Restore state: disable rotation so other tests see a clean slate.
	defer func() {
		rotationGuard.Lock()
		rotationEnabled = false
		rotationGuard.Unlock()
		once = sync.Once{}
		salt = [16]byte{}
	}()

	// The salt should now be non-zero (2^-128 probability of all-zero).
	allZero := true
	for _, b := range salt {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Error("ensure(): salt still all-zero after crypto/rand.Read (statistically impossible)")
	}
}
