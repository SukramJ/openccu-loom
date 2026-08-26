// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package channel_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex %q: %v", s, err)
	}
	return b
}

// TestDerivePrivacyKey_ChipGroupVector locks the HKDF derivation against
// the CHIP group test vector reproduced in matter.js
// packages/protocol/test/codec/MessagePrivacyTest.ts (deriveKey).
func TestDerivePrivacyKey_ChipGroupVector(t *testing.T) {
	t.Parallel()
	operationalKey := mustHex(t, "ca92d7a0942d1a511a0e26ad074f4c2f")
	want := mustHex(t, "bfe9da016a765365f2dd97a9f939e425")
	got, err := channel.DerivePrivacyKey(operationalKey)
	if err != nil {
		t.Fatalf("DerivePrivacyKey: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("privacy key = %x, want %x", got, want)
	}
}

// TestPrivacyMask_ChipObfuscateVector is the behavioural parity check
// for header privacy: masking must be AES-CTR over a 13-byte nonce of
// SessionID(BE) || MIC[5:16], not an AES-ECB block over a 16-byte
// SessionID||MIC[len-14:] IV. The keystream and cleartext/obfuscated
// pair are the CHIP vector reproduced in matter.js
// packages/protocol/test/codec/MessagePrivacyTest.ts (obfuscate).
func TestPrivacyMask_ChipObfuscateVector(t *testing.T) {
	t.Parallel()
	privacyKey := mustHex(t, "bfe9da016a765365f2dd97a9f939e425")
	// privacyNonce = db7d408217b3c0c921a2fca4e1 → SessionID 0xdb7d and
	// mic[5:16] = 408217b3c0c921a2fca4e1. The first 5 MIC bytes are
	// unused by the nonce, so any filler works.
	sessionID := uint16(0xdb7d)
	mic := mustHex(t, "0000000000408217b3c0c921a2fca4e1") // 16 bytes
	cleartext := mustHex(t, "7956341201000000000000000200")
	obfuscated := mustHex(t, "d926afce24c8a0981bdd44f4e730")

	mask, err := channel.PrivacyMask(privacyKey, sessionID, mic)
	if err != nil {
		t.Fatalf("PrivacyMask: %v", err)
	}

	// Obfuscate: cleartext XOR keystream == obfuscated.
	region := append([]byte(nil), cleartext...)
	// Region exceeds one AES block? No — 14 bytes ≤ 16, so a single mask
	// application is the full AES-CTR keystream for the region.
	if err := channel.ApplyPrivacyMask(mask, region); err != nil {
		t.Fatalf("ApplyPrivacyMask (obfuscate): %v", err)
	}
	if !bytes.Equal(region, obfuscated) {
		t.Fatalf("obfuscate mismatch:\n got=%x\nwant=%x", region, obfuscated)
	}

	// Symmetric: obfuscated XOR keystream == cleartext.
	back := append([]byte(nil), obfuscated...)
	if err := channel.ApplyPrivacyMask(mask, back); err != nil {
		t.Fatalf("ApplyPrivacyMask (deobfuscate): %v", err)
	}
	if !bytes.Equal(back, cleartext) {
		t.Fatalf("deobfuscate mismatch:\n got=%x\nwant=%x", back, cleartext)
	}
}
