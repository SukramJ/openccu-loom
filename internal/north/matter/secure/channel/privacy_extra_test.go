// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package channel

import (
	"errors"
	"testing"
)

// TestCloseZeroesPrivacyKeyAfterDerivation verifies that Close() zeroes
// the cached privacy keys when they were previously derived. This exercises
// the non-nil loop bodies in Close().
func TestCloseZeroesPrivacyKeyAfterDerivation(t *testing.T) {
	t.Parallel()

	aCfg, _ := aliceBobConfigs()
	alice, err := New(aCfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Derive privacy keys to populate the cached fields.
	pk, err := alice.PrivacyKey()
	if err != nil || len(pk) == 0 {
		t.Fatalf("PrivacyKey: err=%v len=%d", err, len(pk))
	}
	ppk, err := alice.PeerPrivacyKey()
	if err != nil || len(ppk) == 0 {
		t.Fatalf("PeerPrivacyKey: err=%v len=%d", err, len(ppk))
	}

	// Now Close must exercise the zeroing loops.
	alice.Close()

	// After close, PrivacyKey must return ErrSessionInactive.
	if _, err := alice.PrivacyKey(); !errors.Is(err, ErrSessionInactive) {
		t.Fatalf("PrivacyKey after Close: err=%v, want ErrSessionInactive", err)
	}
}

// TestDerivePrivacyKeyWrongLength exercises the error path in DerivePrivacyKey.
func TestDerivePrivacyKeyWrongLength(t *testing.T) {
	t.Parallel()

	if _, err := DerivePrivacyKey(make([]byte, 8)); err == nil {
		t.Fatal("DerivePrivacyKey with wrong length must return error")
	}
}

// TestDerivePrivacyKeyDeterministic verifies that two calls with the same
// key produce identical output (HKDF is deterministic).
func TestDerivePrivacyKeyDeterministic(t *testing.T) {
	t.Parallel()

	key := make([]byte, 16)
	for i := range key {
		key[i] = byte(i + 1)
	}
	k1, err := DerivePrivacyKey(key)
	if err != nil {
		t.Fatalf("DerivePrivacyKey: %v", err)
	}
	k2, err := DerivePrivacyKey(key)
	if err != nil {
		t.Fatalf("DerivePrivacyKey: %v", err)
	}
	if len(k1) != PrivacyKeySize || len(k2) != PrivacyKeySize {
		t.Fatalf("key length: k1=%d k2=%d, want %d", len(k1), len(k2), PrivacyKeySize)
	}
	for i := range k1 {
		if k1[i] != k2[i] {
			t.Fatalf("non-deterministic at byte %d", i)
		}
	}
}

// TestPrivacyKeyClosedSession checks that PrivacyKey on a closed session
// returns ErrSessionInactive.
func TestPrivacyKeyClosedSession(t *testing.T) {
	t.Parallel()

	aCfg, _ := aliceBobConfigs()
	sess, _ := New(aCfg)
	sess.Close()
	if _, err := sess.PrivacyKey(); !errors.Is(err, ErrSessionInactive) {
		t.Fatalf("PrivacyKey on closed: err=%v, want ErrSessionInactive", err)
	}
}

// TestPeerPrivacyKeyClosedSession checks that PeerPrivacyKey on a closed
// session returns ErrSessionInactive.
func TestPeerPrivacyKeyClosedSession(t *testing.T) {
	t.Parallel()

	aCfg, _ := aliceBobConfigs()
	sess, _ := New(aCfg)
	sess.Close()
	if _, err := sess.PeerPrivacyKey(); !errors.Is(err, ErrSessionInactive) {
		t.Fatalf("PeerPrivacyKey on closed: err=%v, want ErrSessionInactive", err)
	}
}
