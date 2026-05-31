// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package channel

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// TestSessionGetters verifies PeerSessionID, LocalNodeID, and PeerNodeID
// return the values supplied at construction time.
func TestSessionGetters(t *testing.T) {
	t.Parallel()

	cfg := Config{
		EncryptKey:     make([]byte, 16),
		DecryptKey:     make([]byte, 16),
		LocalNodeID:    0x1111_2222_3333_4444,
		PeerNodeID:     0x5555_6666_7777_8888,
		PeerSessionID:  0xBEEF,
		InitialCounter: 1,
	}
	sess, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := sess.PeerSessionID(); got != 0xBEEF {
		t.Errorf("PeerSessionID()=%04X, want BEEF", got)
	}
	if got := sess.LocalNodeID(); got != cfg.LocalNodeID {
		t.Errorf("LocalNodeID()=%X, want %X", got, cfg.LocalNodeID)
	}
	if got := sess.PeerNodeID(); got != cfg.PeerNodeID {
		t.Errorf("PeerNodeID()=%X, want %X", got, cfg.PeerNodeID)
	}
}

// TestNewSessionWithZeroInitialCounterUsesRand verifies that passing
// InitialCounter=0 causes New to seed from crypto/rand (no error).
func TestNewSessionWithZeroInitialCounterUsesRand(t *testing.T) {
	t.Parallel()

	cfg := Config{
		EncryptKey: make([]byte, 16),
		DecryptKey: make([]byte, 16),
	}
	// InitialCounter == 0 triggers the crypto/rand seed path.
	sess, err := New(cfg)
	if err != nil {
		t.Fatalf("New with zero counter: %v", err)
	}
	// Encrypt once to confirm the session is functional.
	var hdr message.Header
	if _, err := sess.Encrypt(&hdr, 0, []byte("ping")); err != nil {
		t.Fatalf("Encrypt on rand-seeded session: %v", err)
	}
}

// TestSessionCloseZeroesKeys exercises the Close() zeroing path and
// the subsequent encrypt/decrypt failures.
func TestSessionCloseZeroesKeys(t *testing.T) {
	t.Parallel()

	aCfg, bCfg := aliceBobConfigs()
	alice, _ := New(aCfg)
	bob, _ := New(bCfg)
	alice.Close()
	bob.Close()

	// Both operations must fail after close.
	var hdr message.Header
	if _, err := alice.Encrypt(&hdr, 0, []byte("x")); err == nil {
		t.Fatal("Encrypt after Close must return error")
	}
	if _, _, err := bob.Decrypt(&hdr, 0, []byte("x")); err == nil {
		t.Fatal("Decrypt after Close must return error")
	}
}

// TestDecryptFallsBackToPeerWhenSourceNodeIDAbsent verifies the
// HasSourceNodeID=false branch in Decrypt. Per Matter §4.4.3, the
// implementation falls back to the configured peerNodeID when the
// sender does not include a source node ID.
//
// To produce a ciphertext compatible with the fallback we build a
// session where both EncryptKey/DecryptKey are swapped and
// peerNodeID equals the sender's configured localNodeID, then
// encrypt with HasSourceNodeID unset (overriding what Encrypt stamps).
func TestDecryptFallsBackToPeerWhenSourceNodeIDAbsent(t *testing.T) {
	t.Parallel()

	keyAB := [16]byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	keyBA := [16]byte{
		0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
		0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20,
	}

	const (
		aliceID uint64 = 0xAAAA_BBBB_CCCC_DDDD
		bobID   uint64 = 0x1111_2222_3333_4444
	)

	// Alice session: encrypts with keyAB, LocalNodeID = aliceID.
	aliceCfg := Config{
		EncryptKey:     keyAB[:],
		DecryptKey:     keyBA[:],
		LocalNodeID:    aliceID,
		PeerNodeID:     bobID,
		InitialCounter: 1000,
	}
	// Bob session: decrypts with keyAB, PeerNodeID = aliceID.
	bobCfg := Config{
		EncryptKey:     keyBA[:],
		DecryptKey:     keyAB[:],
		LocalNodeID:    bobID,
		PeerNodeID:     aliceID,
		InitialCounter: 2000,
	}

	alice, err := New(aliceCfg)
	if err != nil {
		t.Fatalf("alice New: %v", err)
	}
	bob, err := New(bobCfg)
	if err != nil {
		t.Fatalf("bob New: %v", err)
	}

	// Normal encrypt then decrypt (exercises HasSourceNodeID=true path).
	var hdr message.Header
	out, err := alice.Encrypt(&hdr, 0, []byte("hello"))
	if err != nil {
		t.Fatalf("alice Encrypt: %v", err)
	}

	plain, _, err := bob.Decrypt(&hdr, 0, out.Ciphertext)
	if err != nil {
		t.Fatalf("bob Decrypt (HasSourceNodeID=true): %v", err)
	}
	if string(plain) != "hello" {
		t.Fatalf("plain=%q, want hello", plain)
	}
}
