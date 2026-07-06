// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package channel

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

func aliceBobConfigs() (a, b Config) {
	keyAB := []byte{
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	}
	keyBA := []byte{
		0xf0, 0xe1, 0xd2, 0xc3, 0xb4, 0xa5, 0x96, 0x87,
		0x78, 0x69, 0x5a, 0x4b, 0x3c, 0x2d, 0x1e, 0x0f,
	}
	a = Config{
		EncryptKey: keyAB, DecryptKey: keyBA,
		LocalNodeID:    0x1111_2222_3333_4444,
		PeerNodeID:     0x5555_6666_7777_8888,
		InitialCounter: 1,
	}
	b = Config{
		EncryptKey: keyBA, DecryptKey: keyAB,
		LocalNodeID:    0x5555_6666_7777_8888,
		PeerNodeID:     0x1111_2222_3333_4444,
		InitialCounter: 1000,
	}
	return a, b
}

// TestRoundTripAliceToBob — Alice encrypts, Bob decrypts; Bob
// recovers the original plaintext.
func TestRoundTripAliceToBob(t *testing.T) {
	aCfg, bCfg := aliceBobConfigs()
	alice, err := New(aCfg)
	if err != nil {
		t.Fatalf("alice: %v", err)
	}
	bob, err := New(bCfg)
	if err != nil {
		t.Fatalf("bob: %v", err)
	}

	var hdr message.Header
	hdr.SessionID = 0x1234
	plaintext := []byte("hello matter session")
	out, err := alice.Encrypt(&hdr, 0, plaintext)
	if err != nil {
		t.Fatalf("alice.Encrypt: %v", err)
	}
	if hdr.MessageCounter != 1 {
		t.Errorf("counter=%d, want 1", hdr.MessageCounter)
	}
	// Secure unicast omits the Source Node ID on the wire (S flag = 0) —
	// the nonce binds to the local node id via session context, not the
	// header. Mirrors matter.js NodeSession.ts encode.
	if hdr.HasSourceNodeID || hdr.SourceNodeID != 0 {
		t.Errorf("secure unicast must not stamp source node id: has=%v src=%X",
			hdr.HasSourceNodeID, hdr.SourceNodeID)
	}

	got, _, err := bob.Decrypt(&hdr, 0, out.Ciphertext)
	if err != nil {
		t.Fatalf("bob.Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("decrypted=%q, want %q", got, plaintext)
	}
}

// TestReplayDuplicateFlagged — second copy of the same frame still
// authenticates (we need the plaintext to extract the ProtocolHeader
// for the StandaloneAck) but Decrypt flags it as duplicate so the
// caller skips re-processing. Per Matter §4.12 + matter.js
// MessageExchange.ts:294-300.
func TestReplayDuplicateFlagged(t *testing.T) {
	aCfg, bCfg := aliceBobConfigs()
	alice, _ := New(aCfg)
	bob, _ := New(bCfg)

	var hdr message.Header
	out, err := alice.Encrypt(&hdr, 0, []byte("once"))
	if err != nil {
		t.Fatal(err)
	}
	if _, dup, err := bob.Decrypt(&hdr, 0, out.Ciphertext); err != nil || dup {
		t.Fatalf("first Decrypt: err=%v dup=%v (want err=nil dup=false)", err, dup)
	}
	plain, dup, err := bob.Decrypt(&hdr, 0, out.Ciphertext)
	if err != nil {
		t.Fatalf("second Decrypt err=%v (want nil)", err)
	}
	if !dup {
		t.Fatalf("dup=false on retransmit; want true so caller can skip re-processing")
	}
	if !bytes.Equal(plain, []byte("once")) {
		t.Fatalf("plaintext on duplicate = %q, want preserved", plain)
	}
}

// TestTamperedCiphertextRejected — flipping a payload byte must
// surface ErrUnauthenticated.
func TestTamperedCiphertextRejected(t *testing.T) {
	aCfg, bCfg := aliceBobConfigs()
	alice, _ := New(aCfg)
	bob, _ := New(bCfg)
	var hdr message.Header
	out, _ := alice.Encrypt(&hdr, 0, []byte("important"))
	out.Ciphertext[0] ^= 0x01
	if _, _, err := bob.Decrypt(&hdr, 0, out.Ciphertext); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

// TestTamperedHeaderRejected — the header is the AAD; mutating it
// invalidates the MIC.
func TestTamperedHeaderRejected(t *testing.T) {
	aCfg, bCfg := aliceBobConfigs()
	alice, _ := New(aCfg)
	bob, _ := New(bCfg)
	var hdr message.Header
	out, _ := alice.Encrypt(&hdr, 0, []byte("important"))
	hdr.SessionID = 0xFFFF // controller-level Session-ID swap
	if _, _, err := bob.Decrypt(&hdr, 0, out.Ciphertext); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated", err)
	}
}

// TestMonotonicCounters — successive encrypts step the counter by 1.
func TestMonotonicCounters(t *testing.T) {
	aCfg, _ := aliceBobConfigs()
	alice, _ := New(aCfg)
	var prev uint32
	for i := range 5 {
		var hdr message.Header
		out, err := alice.Encrypt(&hdr, 0, []byte{byte(i)})
		if err != nil {
			t.Fatal(err)
		}
		if i > 0 && out.Counter != prev+1 {
			t.Fatalf("step %d: counter=%d, prev=%d", i, out.Counter, prev)
		}
		prev = out.Counter
	}
}

// TestEncryptOnClosedSessionFails locks the post-Close invariant.
func TestEncryptOnClosedSessionFails(t *testing.T) {
	aCfg, _ := aliceBobConfigs()
	alice, _ := New(aCfg)
	alice.Close()
	if _, err := alice.Encrypt(&message.Header{}, 0, nil); !errors.Is(err, ErrSessionInactive) {
		t.Fatalf("err = %v, want ErrSessionInactive", err)
	}
}

// TestConcurrentEncryptAndClose exercises the closed flag under
// concurrent access: one goroutine keeps encrypting while another
// closes the session, mirroring a live send racing an inbound
// shutdown signal. Run with `go test -race`.
func TestConcurrentEncryptAndClose(t *testing.T) {
	aCfg, _ := aliceBobConfigs()
	alice, err := New(aCfg)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for range 2000 {
			var hdr message.Header
			_, _ = alice.Encrypt(&hdr, 0, []byte("x"))
		}
	}()
	go func() {
		defer wg.Done()
		alice.Close()
	}()
	wg.Wait()
}

// TestNewRejectsBadKey passes a non-16-byte key.
func TestNewRejectsBadKey(t *testing.T) {
	if _, err := New(Config{EncryptKey: make([]byte, 8), DecryptKey: make([]byte, 16)}); err == nil {
		t.Fatal("expected error on short EncryptKey")
	}
	if _, err := New(Config{EncryptKey: make([]byte, 16), DecryptKey: make([]byte, 8)}); err == nil {
		t.Fatal("expected error on short DecryptKey")
	}
}

// TestSecurityFlagsBindToNonce changes the secFlags byte; decryption
// must fail because the nonce diverges.
func TestSecurityFlagsBindToNonce(t *testing.T) {
	aCfg, bCfg := aliceBobConfigs()
	alice, _ := New(aCfg)
	bob, _ := New(bCfg)
	var hdr message.Header
	out, _ := alice.Encrypt(&hdr, 0x00, []byte("p"))
	if _, _, err := bob.Decrypt(&hdr, 0x80, out.Ciphertext); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("err = %v, want ErrUnauthenticated (secFlags drift)", err)
	}
}

// TestNonceConstructionMatchesSpec locks the byte layout per Matter
// Core Spec §4.4.3 — secFlags || counter (LE) || srcNodeID (LE).
func TestNonceConstructionMatchesSpec(t *testing.T) {
	got := buildNonce(0x05, 0x11223344, 0xAABBCCDD_EEFF0011)
	want := []byte{
		0x05,                   // secFlags
		0x44, 0x33, 0x22, 0x11, // counter LE
		0x11, 0x00, 0xFF, 0xEE, 0xDD, 0xCC, 0xBB, 0xAA, // srcNodeID LE
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("nonce=% X, want % X", got, want)
	}
}
