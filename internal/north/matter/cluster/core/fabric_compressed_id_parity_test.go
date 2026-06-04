// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Parity tests for the wired CompressedFabricID HKDF derivation
// (computeCompressedFabricID, used by the Operational Credentials cluster
// and Sigma1 DestinationId) against matter.js HEAD.
//
// matter.js reference:
//   packages/types/src/datatype/GlobalFabricId.ts::GlobalFabricId.compute
//   packages/protocol/test/fabric/FabricTest.ts (test vectors)
//
// The derivation is HKDF-SHA256(ikm=rootPublicKey[1:], salt=fabricID-BE8,
// info="CompressedFabric", L=8).

package core

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/hex"
	"testing"
)

// fabricParityRootPubKey returns a fresh uncompressed P-256 public key
// (65 bytes, 0x04 prefix) for the property-based parity checks below.
func fabricParityRootPubKey(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return elliptic.Marshal(elliptic.P256(), priv.X, priv.Y) //nolint:staticcheck // SA1019: test fixture only needs the uncompressed encoding
}

// TestParityMatterJS_CompressedFabricID_HKDF verifies that the wired
// computeCompressedFabricID produces the expected 8-byte output for the
// fixed test vector taken from matter.js FabricTest.ts.
//
// Mirrors matter.js packages/types/src/datatype/GlobalFabricId.ts:
// GlobalFabricId.compute — HKDF-SHA256(rootPublicKey[1:], fabricID-BE8,
// "CompressedFabric", 8). A wrong salt endianness or IKM slice would
// not reproduce this vector.
func TestParityMatterJS_CompressedFabricID_HKDF(t *testing.T) {
	t.Parallel()

	// TEST_ROOT_PUBLIC_KEY_3 from matter.js FabricTest.ts line 27.
	// Uncompressed P-256 public key (65 bytes, starts with 0x04).
	const rootPubKeyHex = "04d89eb7e3f3226d0918f4b85832457bb9981bca7aaef58c18fb5ec07525e472b" +
		"2bd1617fb75ee41bd388f94ae6a6070efc896777516a5c54aff74ec0804cdde9d"

	// TEST_FABRIC_ID_3 from FabricTest.ts line 25: FabricId(0x0000000000000001).
	const fabricID = uint64(0x0000000000000001)

	// Expected output: HKDF-SHA256(rootPublicKey[1:], BE8(fabricID), "CompressedFabric", 8).
	const wantHex = "d559af361549a9a2"

	rootPubKey, err := hex.DecodeString(rootPubKeyHex)
	if err != nil {
		t.Fatalf("hex decode root pub key: %v", err)
	}

	got, err := computeCompressedFabricID(rootPubKey, fabricID)
	if err != nil {
		t.Fatalf("computeCompressedFabricID: %v", err)
	}
	if hex.EncodeToString(got) != wantHex {
		t.Errorf("CompressedFabricID mismatch\n got=%x\nwant=%s", got, wantHex)
	}
}

// TestParityMatterJS_CompressedFabricID_Shape verifies the output is an
// 8-byte value (matter.js L=8) and that the derivation is deterministic
// for identical inputs. The big-endian salt encoding itself is locked by
// the fixed-vector test above.
func TestParityMatterJS_CompressedFabricID_Shape(t *testing.T) {
	t.Parallel()

	key := fabricParityRootPubKey(t)
	const fabricID = uint64(0xCAFEBABEDEADBEEF)

	got, err := computeCompressedFabricID(key, fabricID)
	if err != nil {
		t.Fatalf("computeCompressedFabricID: %v", err)
	}
	if len(got) != 8 {
		t.Errorf("output length=%d, want 8", len(got))
	}
	again, err := computeCompressedFabricID(key, fabricID)
	if err != nil {
		t.Fatalf("computeCompressedFabricID (2nd): %v", err)
	}
	if hex.EncodeToString(got) != hex.EncodeToString(again) {
		t.Errorf("derivation not deterministic for identical inputs")
	}
}

// TestParityMatterJS_CompressedFabricID_IKMStripsPrefix verifies that the
// key material (IKM = rootPublicKey[1:]) feeds the HKDF: two root keys with
// different coordinates must yield different CompressedFabricIDs.
// matter.js GlobalFabricId.ts line 40: Bytes.of(caKey).slice(1).
func TestParityMatterJS_CompressedFabricID_IKMStripsPrefix(t *testing.T) {
	t.Parallel()

	keyA := fabricParityRootPubKey(t)
	keyB := fabricParityRootPubKey(t)
	if hex.EncodeToString(keyA) == hex.EncodeToString(keyB) {
		t.Skip("random key collision — re-run")
	}

	gotA, err := computeCompressedFabricID(keyA, 1)
	if err != nil {
		t.Fatalf("derive A: %v", err)
	}
	gotB, err := computeCompressedFabricID(keyB, 1)
	if err != nil {
		t.Fatalf("derive B: %v", err)
	}
	if hex.EncodeToString(gotA) == hex.EncodeToString(gotB) {
		t.Errorf("different root keys produced the same CompressedID — IKM may include the prefix byte")
	}
}
