// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package fabric

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"
)

// TestPublicKey_InvalidCurvePoint verifies that PublicKey() returns
// ErrInvalidPublicKey when RootPubKey holds syntactically valid but
// off-curve bytes. This exercises the `x == nil` branch inside PublicKey.
func TestPublicKey_InvalidCurvePoint(t *testing.T) {
	t.Parallel()
	// Build a Fabric directly (bypassing New's validation) with 65 bytes
	// that have 0x04 prefix but an all-zero X||Y which is not on P-256.
	raw := make([]byte, PublicKeySize)
	raw[0] = 0x04 // uncompressed prefix
	// X=0, Y=0 is not a valid P-256 curve point.
	f := &Fabric{RootPubKey: raw}
	_, err := f.PublicKey()
	if !errors.Is(err, ErrInvalidPublicKey) {
		t.Fatalf("PublicKey with off-curve point: err=%v, want ErrInvalidPublicKey", err)
	}
}

// generateRootPubKey returns a fresh uncompressed P-256 public-key
// byte slice for use in tests. Matter fabrics in production are
// signed by a long-lived root CA — generating one per test is fine
// because we only exercise the derivation, not certificate validity.
func generateRootPubKey(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return elliptic.Marshal(elliptic.P256(), priv.X, priv.Y) //nolint:staticcheck // SA1019: test fixture
}

// TestNewRejectsMalformedRootKey covers the three failure modes:
// wrong length, wrong prefix, off-curve point.
func TestNewRejectsMalformedRootKey(t *testing.T) {
	cases := []struct {
		name string
		key  []byte
	}{
		{"too short", make([]byte, 32)},
		{"wrong prefix", append([]byte{0x05}, make([]byte, 64)...)},
		{"off curve", append([]byte{0x04}, make([]byte, 64)...)}, // all-zero x,y is not on the curve
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(1, 2, tc.key, 0xFFF1); !errors.Is(err, ErrInvalidPublicKey) {
				t.Fatalf("err=%v, want ErrInvalidPublicKey", err)
			}
		})
	}
}

// TestCompressedFabricIDDeterministic — the same inputs always
// produce the same 8-byte output. This is the contract every other
// node on the fabric relies on for the mDNS hostname.
func TestCompressedFabricIDDeterministic(t *testing.T) {
	key := generateRootPubKey(t)
	a, err := New(0xCAFEBABE_DEADBEEF, 0x1, key, 0xFFF1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(0xCAFEBABE_DEADBEEF, 0x1, key, 0xFFF1)
	if err != nil {
		t.Fatal(err)
	}
	if a.CompressedID != b.CompressedID {
		t.Fatalf("non-deterministic: a=% X b=% X", a.CompressedID, b.CompressedID)
	}
}

// TestCompressedFabricIDChangesPerFabricID confirms a different
// FabricID under the same root key yields a different compressed
// identifier (otherwise two fabrics would collide on the LAN).
func TestCompressedFabricIDChangesPerFabricID(t *testing.T) {
	key := generateRootPubKey(t)
	a, _ := New(1, 1, key, 0xFFF1)
	b, _ := New(2, 1, key, 0xFFF1)
	if a.CompressedID == b.CompressedID {
		t.Fatalf("FabricID drift did not change CompressedID: % X", a.CompressedID)
	}
}

// TestCompressedFabricIDChangesPerRootKey confirms a different root
// key under the same FabricID yields a different compressed
// identifier (otherwise impersonation by re-keying the root CA would
// be undetectable on the LAN).
func TestCompressedFabricIDChangesPerRootKey(t *testing.T) {
	keyA := generateRootPubKey(t)
	keyB := generateRootPubKey(t)
	a, _ := New(1, 1, keyA, 0xFFF1)
	b, _ := New(1, 1, keyB, 0xFFF1)
	if a.CompressedID == b.CompressedID {
		t.Fatalf("Root-key swap did not change CompressedID: % X", a.CompressedID)
	}
}

// TestPublicKeyDecodes confirms the in-memory uncompressed bytes can
// be re-decoded into an ecdsa.PublicKey for signature verification.
func TestPublicKeyDecodes(t *testing.T) {
	key := generateRootPubKey(t)
	f, err := New(1, 1, key, 0xFFF1)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := f.PublicKey()
	if err != nil {
		t.Fatalf("PublicKey: %v", err)
	}
	if pk.Curve != elliptic.P256() {
		t.Fatalf("curve=%v, want P-256", pk.Curve)
	}
}
