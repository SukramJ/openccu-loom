// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package channel_test

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
)

// newPrivacyTestConfig returns a Config with EncryptKey != DecryptKey
// so PrivacyKey() and PeerPrivacyKey() can be distinguished. Inlined
// here (instead of reusing session_test.go's aliceBobConfigs) because
// this file lives in package channel_test, not channel — the helper
// would otherwise be unreachable.
func newPrivacyTestConfig() channel.Config {
	encKey := []byte{
		0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
	}
	decKey := []byte{
		0xf0, 0xe1, 0xd2, 0xc3, 0xb4, 0xa5, 0x96, 0x87,
		0x78, 0x69, 0x5a, 0x4b, 0x3c, 0x2d, 0x1e, 0x0f,
	}
	return channel.Config{
		EncryptKey:     encKey,
		DecryptKey:     decKey,
		LocalNodeID:    0x1111_2222_3333_4444,
		PeerNodeID:     0x5555_6666_7777_8888,
		InitialCounter: 1,
	}
}

// TestDerivePrivacyKey_BadLength — 15-byte and 17-byte keys both
// return ErrPrivacyKeySource.
func TestDerivePrivacyKey_BadLength(t *testing.T) {
	t.Parallel()
	for _, l := range []int{15, 17} {
		_, err := channel.DerivePrivacyKey(make([]byte, l))
		if !errors.Is(err, channel.ErrPrivacyKeySource) {
			t.Errorf("len=%d: err=%v, want ErrPrivacyKeySource", l, err)
		}
	}
}

// TestDerivePrivacyKey_Deterministic — same 16-byte input yields
// byte-identical, 16-byte, non-nil output on both calls.
func TestDerivePrivacyKey_Deterministic(t *testing.T) {
	t.Parallel()
	key := make([]byte, 16)
	key[0] = 0xAB
	a, err := channel.DerivePrivacyKey(key)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	b, err := channel.DerivePrivacyKey(key)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(a) != channel.PrivacyKeySize {
		t.Fatalf("len(a)=%d, want %d", len(a), channel.PrivacyKeySize)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("outputs differ: % X vs % X", a, b)
	}
}

// TestDerivePrivacyKey_DifferentInputsDifferOutputs — two distinct
// 16-byte session keys produce different privacy keys.
func TestDerivePrivacyKey_DifferentInputsDifferOutputs(t *testing.T) {
	t.Parallel()
	key1 := make([]byte, 16)
	key2 := make([]byte, 16)
	key2[0] = 0xFF
	a, err := channel.DerivePrivacyKey(key1)
	if err != nil {
		t.Fatalf("key1: %v", err)
	}
	b, err := channel.DerivePrivacyKey(key2)
	if err != nil {
		t.Fatalf("key2: %v", err)
	}
	if bytes.Equal(a, b) {
		t.Fatal("distinct inputs produced identical privacy keys")
	}
}

// TestDerivePrivacyKey_NotEqualToInput — HKDF actually transforms
// the bytes; output must differ from input.
func TestDerivePrivacyKey_NotEqualToInput(t *testing.T) {
	t.Parallel()
	key := []byte{
		0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
		0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
	}
	out, err := channel.DerivePrivacyKey(key)
	if err != nil {
		t.Fatalf("DerivePrivacyKey: %v", err)
	}
	if bytes.Equal(out, key) {
		t.Fatal("output equals input — HKDF did not transform the key")
	}
}

// TestPrivacyMask_BadKeyLength — a 15-byte privacyKey returns
// ErrPrivacyKeySource.
func TestPrivacyMask_BadKeyLength(t *testing.T) {
	t.Parallel()
	mic := make([]byte, 16)
	_, err := channel.PrivacyMask(make([]byte, 15), 0x0001, mic)
	if !errors.Is(err, channel.ErrPrivacyKeySource) {
		t.Fatalf("err=%v, want ErrPrivacyKeySource", err)
	}
}

// TestPrivacyMask_ShortMIC — a 13-byte MIC (short of the 16-byte
// AES-CCM tag) returns ErrPrivacyMICShort.
func TestPrivacyMask_ShortMIC(t *testing.T) {
	t.Parallel()
	key := make([]byte, 16)
	_, err := channel.PrivacyMask(key, 0x0001, make([]byte, 13))
	if !errors.Is(err, channel.ErrPrivacyMICShort) {
		t.Fatalf("err=%v, want ErrPrivacyMICShort", err)
	}
}

// TestPrivacyMask_AcceptsExactly16Bytes — a full 16-byte AES-CCM MIC
// is valid and returns a 16-byte keystream block without error.
func TestPrivacyMask_AcceptsExactly16Bytes(t *testing.T) {
	t.Parallel()
	key := make([]byte, 16)
	mic := make([]byte, 16)
	mask, err := channel.PrivacyMask(key, 0x0001, mic)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mask) != channel.PrivacyKeySize {
		t.Fatalf("mask len=%d, want %d", len(mask), channel.PrivacyKeySize)
	}
}

// TestPrivacyMask_UsesLast11BytesOfMIC — the nonce reads mic[5:16], so
// two MICs that share their last 11 bytes but differ in the first 5
// must produce identical masks (proves the mic[5:16] slice, not
// mic[0:11] or mic[len-14:]).
func TestPrivacyMask_UsesLast11BytesOfMIC(t *testing.T) {
	t.Parallel()
	key := make([]byte, 16)
	base := []byte{
		0x01, 0x02, 0x03, 0x04, 0x05, // first 5 bytes — NOT in the nonce
		0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, // last 11 — these matter
	}
	other := append([]byte(nil), base...)
	other[0], other[1], other[2], other[3], other[4] = 0xFF, 0xFE, 0xFD, 0xFC, 0xFB
	maskBase, err := channel.PrivacyMask(key, 0x0042, base)
	if err != nil {
		t.Fatalf("base mic: %v", err)
	}
	maskOther, err := channel.PrivacyMask(key, 0x0042, other)
	if err != nil {
		t.Fatalf("other mic: %v", err)
	}
	if !bytes.Equal(maskBase, maskOther) {
		t.Fatalf("masks differ though last 11 MIC bytes match: % X vs % X", maskBase, maskOther)
	}
}

// TestPrivacyMask_DifferentSessionIDsDifferMasks — same key+mic,
// different sessionIDs produce different masks.
func TestPrivacyMask_DifferentSessionIDsDifferMasks(t *testing.T) {
	t.Parallel()
	key := make([]byte, 16)
	mic := make([]byte, 16)
	mask1, err := channel.PrivacyMask(key, 0x0001, mic)
	if err != nil {
		t.Fatalf("sid=1: %v", err)
	}
	mask2, err := channel.PrivacyMask(key, 0x0002, mic)
	if err != nil {
		t.Fatalf("sid=2: %v", err)
	}
	if bytes.Equal(mask1, mask2) {
		t.Fatal("different sessionIDs produced identical masks")
	}
}

// TestApplyPrivacyMask_RoundTrip — applying the same mask twice
// (XOR symmetry) returns the slice to its original content.
func TestApplyPrivacyMask_RoundTrip(t *testing.T) {
	t.Parallel()
	key := make([]byte, 16)
	key[0] = 0x55
	mic := make([]byte, 16)
	mask, err := channel.PrivacyMask(key, 0x0099, mic)
	if err != nil {
		t.Fatalf("PrivacyMask: %v", err)
	}
	header := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04}
	original := bytes.Clone(header)
	if err := channel.ApplyPrivacyMask(mask, header); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if err := channel.ApplyPrivacyMask(mask, header); err != nil {
		t.Fatalf("second apply: %v", err)
	}
	if !bytes.Equal(header, original) {
		t.Fatalf("after round-trip: % X, want % X", header, original)
	}
}

// TestApplyPrivacyMask_BadMaskLength — a 15-byte mask returns a
// non-nil error.
func TestApplyPrivacyMask_BadMaskLength(t *testing.T) {
	t.Parallel()
	if err := channel.ApplyPrivacyMask(make([]byte, 15), []byte{0x01}); err == nil {
		t.Fatal("expected error for 15-byte mask, got nil")
	}
}

// TestApplyPrivacyMask_OverSizedSlice — a 17-byte headerSlice returns
// a non-nil error.
func TestApplyPrivacyMask_OverSizedSlice(t *testing.T) {
	t.Parallel()
	if err := channel.ApplyPrivacyMask(make([]byte, 16), make([]byte, 17)); err == nil {
		t.Fatal("expected error for 17-byte headerSlice, got nil")
	}
}

// TestApplyPrivacyMask_ZeroSliceIsNoOp — empty headerSlice with a
// valid mask causes no error and no panic.
func TestApplyPrivacyMask_ZeroSliceIsNoOp(t *testing.T) {
	t.Parallel()
	if err := channel.ApplyPrivacyMask(make([]byte, 16), []byte{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestApplyPrivacyMask_PartialSlice — a 4-byte header gets XORed
// with exactly the first 4 bytes of the mask.
func TestApplyPrivacyMask_PartialSlice(t *testing.T) {
	t.Parallel()
	mask := []byte{
		0x11, 0x22, 0x33, 0x44,
		0x55, 0x66, 0x77, 0x88,
		0x99, 0xaa, 0xbb, 0xcc,
		0xdd, 0xee, 0xff, 0x00,
	}
	header := []byte{0x01, 0x02, 0x03, 0x04}
	if err := channel.ApplyPrivacyMask(mask, header); err != nil {
		t.Fatalf("ApplyPrivacyMask: %v", err)
	}
	want := []byte{
		0x01 ^ 0x11,
		0x02 ^ 0x22,
		0x03 ^ 0x33,
		0x04 ^ 0x44,
	}
	if !bytes.Equal(header, want) {
		t.Fatalf("header=% X, want % X", header, want)
	}
}

// TestSession_PrivacyKey_LazilyDerived — PrivacyKey() is stable
// across two calls (cached) and returns a 16-byte key.
func TestSession_PrivacyKey_LazilyDerived(t *testing.T) {
	t.Parallel()
	aCfg := newPrivacyTestConfig()
	sess, err := channel.New(aCfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	k1, err := sess.PrivacyKey()
	if err != nil {
		t.Fatalf("first PrivacyKey: %v", err)
	}
	k2, err := sess.PrivacyKey()
	if err != nil {
		t.Fatalf("second PrivacyKey: %v", err)
	}
	if len(k1) != channel.PrivacyKeySize {
		t.Fatalf("key length=%d, want %d", len(k1), channel.PrivacyKeySize)
	}
	if !bytes.Equal(k1, k2) {
		t.Fatalf("PrivacyKey not stable: % X vs % X", k1, k2)
	}
}

// TestSession_PeerPrivacyKey_DiffersFromOwn — when EncryptKey !=
// DecryptKey, PrivacyKey() and PeerPrivacyKey() must differ.
func TestSession_PeerPrivacyKey_DiffersFromOwn(t *testing.T) {
	t.Parallel()
	aCfg := newPrivacyTestConfig()
	// aliceBobConfigs returns EncryptKey != DecryptKey for alice.
	sess, err := channel.New(aCfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	own, err := sess.PrivacyKey()
	if err != nil {
		t.Fatalf("PrivacyKey: %v", err)
	}
	peer, err := sess.PeerPrivacyKey()
	if err != nil {
		t.Fatalf("PeerPrivacyKey: %v", err)
	}
	if bytes.Equal(own, peer) {
		t.Fatal("PrivacyKey and PeerPrivacyKey are identical — keys must differ")
	}
}

// TestSession_PrivacyKey_ReturnsErrAfterClose — PrivacyKey() and
// PeerPrivacyKey() both return ErrSessionInactive after Close().
func TestSession_PrivacyKey_ReturnsErrAfterClose(t *testing.T) {
	t.Parallel()
	aCfg := newPrivacyTestConfig()
	sess, err := channel.New(aCfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sess.Close()
	if _, err := sess.PrivacyKey(); !errors.Is(err, channel.ErrSessionInactive) {
		t.Fatalf("PrivacyKey after Close: err=%v, want ErrSessionInactive", err)
	}
	if _, err := sess.PeerPrivacyKey(); !errors.Is(err, channel.ErrSessionInactive) {
		t.Fatalf("PeerPrivacyKey after Close: err=%v, want ErrSessionInactive", err)
	}
}

// TestConcurrentPrivacyKeyDerivationAndClose reproduces the live
// shape: the operational manager's idle reaper closes a session on its
// own goroutine while the receive path unmasks a privacy-flagged frame
// for that same session. Both touch the session key material, so Close
// must zeroise it under the lock the accessors hold, and the accessors
// must hand out material the caller can keep reading after they
// return. Run with `go test -race`.
func TestConcurrentPrivacyKeyDerivationAndClose(t *testing.T) {
	t.Parallel()
	for range 200 {
		sess, err := channel.New(newPrivacyTestConfig())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		// Prime the cache so the racing reader exercises the cached
		// slice as well as the encKey/decKey derivation source.
		if _, err := sess.PrivacyKey(); err != nil {
			t.Fatalf("PrivacyKey: %v", err)
		}

		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			if k, err := sess.PeerPrivacyKey(); err == nil {
				_ = bytes.Contains(k, []byte{0xFF})
			}
		}()
		go func() {
			defer wg.Done()
			if k, err := sess.PrivacyKey(); err == nil {
				_ = bytes.Contains(k, []byte{0xFF})
			}
		}()
		go func() {
			defer wg.Done()
			sess.Close()
		}()
		wg.Wait()
	}
}
