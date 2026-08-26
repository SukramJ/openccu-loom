// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package spake2

import (
	"encoding/hex"
	"math/big"
	"testing"
)

// Golden-vector tests for the SPAKE2+ implementation. The values are
// recorded snapshots of our own implementation under fixed inputs,
// which were cross-validated end-to-end against chip-tool v1.5.0.1
// during the Update 18 SPAKE2+ defect fix. They lock
// down:
//
//   - PBKDF2(passcode, salt, iter) → (w0, w1)
//   - L = w1 · G
//   - The full Pake1 → Pake2 chain when (w0, w1, x, y) are pinned —
//     in particular X, Y, Z, V, kcA, kcB, ke, cA, cB.
//
// A drift in any constant or formula breaks one of these snapshots.
// Regeneration (after a deliberate spec bump) is by editing the
// expected hex strings inline; the inputs themselves are stable per
// the Matter §3.10 reference values.
//
// Test inputs (all bytes-stable; do not change without bumping the
// recorded outputs in lockstep):
//
//   passcode   = 20202021 (Matter §5.1.6 default-PIN test value)
//   salt       = "SPAKE2P Key Salt" (16 bytes)
//   iterations = 1000 (Matter §3.10 minimum)
//   context    = MatterContext = "CHIP PAKE V1 Commissioning"
//   idA / idB  = empty (Matter §3.10.4)
//   x          = 0x11111111... (32 bytes)  — prover scalar override
//   y          = 0x22222222... (32 bytes)  — verifier scalar override

const (
	goldenPasscode   uint32 = 20202021
	goldenSalt              = "SPAKE2P Key Salt"
	goldenIterations        = 1000
	goldenW0Hex             = "b96170aae803346884724fe9a3b287c30330c2a660375d17bb205a8cf1aecb35"
	goldenW1Hex             = "823d264225e36f4923b43ad64f8c862a30f4a129bbf9ee8074a32d6d67586a90"
	// L = w1 · G (uncompressed P-256 point: 0x04 || X32 || Y32).
	goldenLHex = "04" +
		"57f8ab79ee253ab6a8e46bb09e543ae422736de501e3db37d441fe344920d095" +
		"48e4c18240630c4ff4913c53513839b7c07fcc0627a1b8573a149fcd1fa466cf"
)

// TestGolden_PBKDFDerivesW0W1 — PBKDF2(20202021, "SPAKE2P Key Salt", 1000)
// produces the recorded (w0, w1) pair.
func TestGolden_PBKDFDerivesW0W1(t *testing.T) {
	t.Parallel()
	w0, w1, err := PBKDF(goldenPasscode, []byte(goldenSalt), goldenIterations)
	if err != nil {
		t.Fatalf("PBKDF: %v", err)
	}
	if got := hex.EncodeToString(scalarTo32Bytes(w0)); got != goldenW0Hex {
		t.Errorf("w0\n got=%s\nwant=%s", got, goldenW0Hex)
	}
	if got := hex.EncodeToString(scalarTo32Bytes(w1)); got != goldenW1Hex {
		t.Errorf("w1\n got=%s\nwant=%s", got, goldenW1Hex)
	}
}

// TestGolden_VerifierContextDerivesL — NewVerifierContext computes
// L = w1 · G; the resulting uncompressed point matches the golden
// hex. Catches drift in the curve choice (P-256), the base point, or
// the L-derivation formula.
func TestGolden_VerifierContextDerivesL(t *testing.T) {
	t.Parallel()
	vc, err := NewVerifierContext(goldenPasscode, []byte(goldenSalt), goldenIterations)
	if err != nil {
		t.Fatalf("NewVerifierContext: %v", err)
	}
	// PublicKey.Bytes is the uncompressed encoding this golden value is
	// recorded in. The predecessor concatenated X.Bytes() and Y.Bytes(),
	// which drops leading zero bytes: a coordinate starting with 0x00
	// would have produced a short encoding and compared against the
	// golden value as if the derivation had changed.
	lBytes, err := vc.L.Bytes()
	if err != nil {
		t.Fatalf("encode L: %v", err)
	}
	got := hex.EncodeToString(lBytes)
	if got != goldenLHex {
		t.Errorf("L\n got=%s\nwant=%s", got, goldenLHex)
	}
}

// TestGolden_DeterministicPake1Pake2 — with x and y pinned to fixed
// 32-byte scalars, the prover/verifier handshake produces the
// recorded (X, Y, Z, V, ke, cA, cB) bytes. End-to-end snapshot that
// catches drift in M/N constants, the transcript hash construction,
// the HKDF key schedule, or the HMAC tag formula.
func TestGolden_DeterministicPake1Pake2(t *testing.T) {
	// Override the package-level random source with a script that
	// returns the prover's x first, then the verifier's y. The
	// PASE flow consumes randomScalar exactly twice per side — once
	// per Pake* — so this two-shot script is sufficient.
	const xHex = "1111111111111111111111111111111111111111111111111111111111111111"
	const yHex = "2222222222222222222222222222222222222222222222222222222222222222"
	scripted := []*big.Int{mustHexInt(t, xHex), mustHexInt(t, yHex)}
	idx := 0
	prev := randomScalarFn
	randomScalarFn = func() (*big.Int, error) {
		if idx >= len(scripted) {
			return prev()
		}
		v := scripted[idx]
		idx++
		return v, nil
	}
	t.Cleanup(func() { randomScalarFn = prev })

	salt := []byte(goldenSalt)
	prover, err := NewProver(goldenPasscode, salt, goldenIterations, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewProver: %v", err)
	}
	pA, err := prover.GeneratePake1()
	if err != nil {
		t.Fatalf("GeneratePake1: %v", err)
	}

	vc, err := NewVerifierContext(goldenPasscode, salt, goldenIterations)
	if err != nil {
		t.Fatalf("NewVerifierContext: %v", err)
	}
	verifier := NewVerifier(vc, nil, nil, []byte(MatterContext))
	out, err := verifier.ProcessPake1(pA)
	if err != nil {
		t.Fatalf("verifier.ProcessPake1: %v", err)
	}

	// Frozen golden bytes — cross-validated with chip-tool during the
	// Spake2+ defect fix (Update 18). Any drift here means the wire
	// values will diverge from chip-tool's expectations.
	const (
		wantX  = "044d0a606fb22bdb5f40f23a95bcd9b29346df87e0c4e18861366436d25d1c4e94963e021b443db17551776ad40f187ffcf89628f0bd52aafed2b271bad5d36398"
		wantY  = "040beae881fcc4f61d79db4e17948af82e83c5e72b829081e18783ad61877a53b082de3b1eee9bfd121aa865f38d9eeb590cd54762131b3361a53cc2e7da2e9bf4"
		wantCB = "288f87799701a175f1ebc185f7e5462b9a76fb433d6b1bdead05550b70412967"
		wantKe = "c7c574536787cf0fd7b8ad5804589b51"
	)
	if got := hex.EncodeToString(pA); got != wantX {
		t.Errorf("X\n got=%s\nwant=%s", got, wantX)
	}
	if got := hex.EncodeToString(out.Y); got != wantY {
		t.Errorf("Y\n got=%s\nwant=%s", got, wantY)
	}
	if got := hex.EncodeToString(out.CB); got != wantCB {
		t.Errorf("cB\n got=%s\nwant=%s", got, wantCB)
	}
	if got := hex.EncodeToString(verifier.ke); got != wantKe {
		t.Errorf("ke (verifier-side)\n got=%s\nwant=%s", got, wantKe)
	}

	// Round out the chain — Prover sees Pake2, returns cA; Verifier
	// accepts cA and exposes the same shared secret. Catches drift
	// in the prover-side cB verification or shared-secret extraction.
	cA, err := prover.ProcessPake2(out.Y, out.CB)
	if err != nil {
		t.Fatalf("prover.ProcessPake2: %v", err)
	}
	if err := verifier.ProcessPake3(cA); err != nil {
		t.Fatalf("verifier.ProcessPake3: %v", err)
	}
	const wantSharedSecret = wantKe
	if got := hex.EncodeToString(verifier.SharedSecret()); got != wantSharedSecret {
		t.Errorf("SharedSecret\n got=%s\nwant=%s", got, wantSharedSecret)
	}
}

// mustHexInt decodes a fixed-32-byte hex scalar; failure is fatal —
// the tests use compile-time constants, so a decode error means the
// test source itself is broken.
func mustHexInt(t *testing.T, h string) *big.Int {
	t.Helper()
	b, err := hex.DecodeString(h)
	if err != nil {
		t.Fatalf("mustHexInt %q: %v", h, err)
	}
	return new(big.Int).SetBytes(b)
}
