// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Mirrors matter.js packages/protocol/test/session/secure/CasePairingTest.ts.
// Each test case loads a vector from testdata/matterjs-vectors.json and
// asserts that openccu-loom's CASE Sigma primitives produce byte-identical
// output to the matter.js HEAD reference values.
//
// Test-vector provenance:
//   - sigma2_*         — CasePairingTest.ts:14-63 (Sigma2 key schedule + AES-CCM)
//   - sigma3_*         — CasePairingTest.ts:75-129 (Sigma3 key schedule + AES-CCM)
//   - session_keys_*   — CasePairingTest.ts:117-128 (final session-key derivation)
//
// If any assertion fails that is a REAL drift against matter.js — stop,
// report, do not paper over.

package sigma

import (
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/aesccm"
)

//go:embed testdata/matterjs-vectors.json
var matterJSVectorsJSON []byte

// matterJSSigmaVector is the on-disk shape of one sigma test vector.
type matterJSSigmaVector struct {
	Label       string            `json:"label"`
	Description string            `json:"description"`
	Input       map[string]string `json:"input"`
	Expected    map[string]string `json:"expected"`
}

// loadMatterJSSigmaVectors parses and returns all vectors from the embedded JSON.
func loadMatterJSSigmaVectors(t *testing.T) []matterJSSigmaVector {
	t.Helper()
	var vecs []matterJSSigmaVector
	if err := json.Unmarshal(matterJSVectorsJSON, &vecs); err != nil {
		t.Fatalf("loadMatterJSSigmaVectors: %v", err)
	}
	return vecs
}

// mustHexSigma decodes a hex string; fatal on error.
func mustHexSigma(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("mustHex %q: %v", s, err)
	}
	return b
}

// mustNonce builds a 13-byte AES-CCM nonce from an ASCII label (zero-padded).
func mustNonce(t *testing.T, ascii string) []byte {
	t.Helper()
	n := make([]byte, aesccm.NonceSize)
	if len(ascii) > aesccm.NonceSize {
		t.Fatalf("nonce label %q too long (max %d)", ascii, aesccm.NonceSize)
	}
	copy(n, ascii)
	return n
}

// TestParityMatterJS_Sigma_Vectors runs all matter.js CASE Sigma parity cases.
func TestParityMatterJS_Sigma_Vectors(t *testing.T) {
	t.Parallel()
	vecs := loadMatterJSSigmaVectors(t)

	for _, v := range vecs {
		t.Run(v.Label, func(t *testing.T) {
			t.Parallel()
			switch v.Label {
			case "sigma2_salt_derivation":
				testSigma2Salt(t, v)
			case "sigma2_key_hkdf":
				testSigma2KeyHKDF(t, v)
			case "sigma2_aesccm_encryption":
				testSigma2AESCCMEncryption(t, v)
			case "sigma3_salt_derivation":
				testSigma3Salt(t, v)
			case "sigma3_key_hkdf":
				testSigma3KeyHKDF(t, v)
			case "sigma3_aesccm_decryption":
				testSigma3AESCCMDecryption(t, v)
			case "session_keys_derivation":
				testSessionKeysDerivation(t, v)
			default:
				t.Skipf("unknown vector label %q", v.Label)
			}
		})
	}
}

// testSigma2Salt verifies sigma2Salt() = IPK || respRandom || respEphPub || SHA256(sigma1).
// Mirrors matter.js CasePairingTest.ts:24-28.
func testSigma2Salt(t *testing.T, v matterJSSigmaVector) {
	t.Helper()
	ipk := mustHexSigma(t, v.Input["ipk"])
	respRandom := mustHexSigma(t, v.Input["responder_random"])
	respEphPub := mustHexSigma(t, v.Input["responder_eph_pub"])
	sigma1Bytes := mustHexSigma(t, v.Input["sigma1_bytes"])
	want := mustHexSigma(t, v.Expected["sigma2_salt"])

	got := sigma2Salt(ipk, respRandom, respEphPub, sigma1Bytes)

	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("sigma2Salt mismatch\n got=%x\nwant=%x", got, want)
	}
}

// testSigma2KeyHKDF verifies S2K = HKDF(sharedSecret, sigma2Salt, "Sigma2", 16).
// Mirrors matter.js CasePairingTest.ts:30-32.
func testSigma2KeyHKDF(t *testing.T, v matterJSSigmaVector) {
	t.Helper()
	sharedSecret := mustHexSigma(t, v.Input["shared_secret"])
	salt := mustHexSigma(t, v.Input["sigma2_salt"])
	info := v.Input["hkdf_info"]
	want := mustHexSigma(t, v.Expected["sigma2_key"])

	got, err := hkdfDerive(sharedSecret, salt, info, 16)
	if err != nil {
		t.Fatalf("hkdfDerive: %v", err)
	}

	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("sigma2Key mismatch\n got=%x\nwant=%x", got, want)
	}
}

// testSigma2AESCCMEncryption verifies AES-CCM Seal matches matter.js
// crypto.encrypt(sigma2Key, tbe2Plaintext, "NCASE_Sigma2N").
// Mirrors matter.js CasePairingTest.ts:58-62.
func testSigma2AESCCMEncryption(t *testing.T, v matterJSSigmaVector) {
	t.Helper()
	key := mustHexSigma(t, v.Input["sigma2_key"])
	nonce := mustNonce(t, v.Input["nonce_ascii"])
	plaintext := mustHexSigma(t, v.Input["plaintext_hex"])
	want := mustHexSigma(t, v.Expected["ciphertext_hex"])

	c, err := aesccm.New(key)
	if err != nil {
		t.Fatalf("aesccm.New: %v", err)
	}
	got, err := c.Seal(nil, nonce, plaintext, nil)
	if err != nil {
		t.Fatalf("aesccm.Seal: %v", err)
	}

	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("AES-CCM Sigma2 ciphertext mismatch\n got=%x\nwant=%x", got, want)
	}
}

// testSigma3Salt verifies sigma3Salt() = IPK || SHA256(sigma1 || sigma2).
// Mirrors matter.js CasePairingTest.ts:85-92.
func testSigma3Salt(t *testing.T, v matterJSSigmaVector) {
	t.Helper()
	ipk := mustHexSigma(t, v.Input["ipk"])
	sigma1Bytes := mustHexSigma(t, v.Input["sigma1_bytes"])
	sigma2Bytes := mustHexSigma(t, v.Input["sigma2_bytes"])
	want := mustHexSigma(t, v.Expected["sigma3_salt"])

	got := sigma3Salt(ipk, sigma1Bytes, sigma2Bytes)

	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("sigma3Salt mismatch\n got=%x\nwant=%x", got, want)
	}
}

// testSigma3KeyHKDF verifies S3K = HKDF(sharedSecret, sigma3Salt, "Sigma3", 16).
// Mirrors matter.js CasePairingTest.ts:94-96.
func testSigma3KeyHKDF(t *testing.T, v matterJSSigmaVector) {
	t.Helper()
	sharedSecret := mustHexSigma(t, v.Input["shared_secret"])
	salt := mustHexSigma(t, v.Input["sigma3_salt"])
	info := v.Input["hkdf_info"]
	want := mustHexSigma(t, v.Expected["sigma3_key"])

	got, err := hkdfDerive(sharedSecret, salt, info, 16)
	if err != nil {
		t.Fatalf("hkdfDerive: %v", err)
	}

	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("sigma3Key mismatch\n got=%x\nwant=%x", got, want)
	}
}

// testSigma3AESCCMDecryption verifies AES-CCM Open of the peer's
// encrypted3 payload matches the known TBE3 plaintext.
// Mirrors matter.js CasePairingTest.ts:98-102.
func testSigma3AESCCMDecryption(t *testing.T, v matterJSSigmaVector) {
	t.Helper()
	key := mustHexSigma(t, v.Input["sigma3_key"])
	nonce := mustNonce(t, v.Input["nonce_ascii"])
	ciphertext := mustHexSigma(t, v.Input["ciphertext_hex"])
	want := mustHexSigma(t, v.Expected["plaintext_hex"])

	c, err := aesccm.New(key)
	if err != nil {
		t.Fatalf("aesccm.New: %v", err)
	}
	got, err := c.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		t.Fatalf("aesccm.Open: %v", err)
	}

	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("AES-CCM Sigma3 plaintext mismatch\n got=%x\nwant=%x", got, want)
	}
}

// testSessionKeysDerivation verifies the final session decryptKey =
// HKDF(sharedSecret, IPK||SHA256(sigma1||sigma2||sigma3), "SessionKeys", 16).
// Mirrors matter.js CasePairingTest.ts:117-128.
//
// The matter.js test requests only 16 bytes (one direction key). In
// production openccu-loom derives 48 bytes and splits into I2RKey /
// R2IKey / AttestationChallenge. The first 16 bytes must match the
// matter.js reference.
func testSessionKeysDerivation(t *testing.T, v matterJSSigmaVector) {
	t.Helper()
	sharedSecret := mustHexSigma(t, v.Input["shared_secret"])
	ipk := mustHexSigma(t, v.Input["ipk"])
	sigma1Bytes := mustHexSigma(t, v.Input["sigma1_bytes"])
	sigma2Bytes := mustHexSigma(t, v.Input["sigma2_bytes"])
	sigma3Bytes := mustHexSigma(t, v.Input["sigma3_bytes"])
	want := mustHexSigma(t, v.Expected["decrypt_key"])

	salt := sessionKeysSalt(ipk, sigma1Bytes, sigma2Bytes, sigma3Bytes)

	// Request 48 bytes (full production key material) — matter.js
	// CasePairingTest.ts:121-126 requests 16 and labels the result
	// "decryptKey". The spec labels it I2RKey; the matter.js test
	// uses the same HKDF parameters with L=16. Verify first 16 bytes.
	fullKeys, err := hkdfDerive(sharedSecret, salt, v.Input["hkdf_info"], FinalKeyMaterialSize)
	if err != nil {
		t.Fatalf("hkdfDerive: %v", err)
	}

	got := fullKeys[0:16]
	if hex.EncodeToString(got) != hex.EncodeToString(want) {
		t.Errorf("sessionKeys[0:16] mismatch\n got=%x\nwant=%x", got, want)
	}
}
