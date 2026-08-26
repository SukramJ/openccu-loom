// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package commissioning_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/commissioning"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// minimalCSRInput returns a valid CSRInput using the test chain's DAC key.
func minimalCSRInput(dacKey *ecdsa.PrivateKey) commissioning.CSRInput {
	return commissioning.CSRInput{
		CSRNonce:             validNonce32(),
		AttestationChallenge: validChallenge(),
		DACPrivateKey:        dacKey,
	}
}

// TestBuildCSR_ShortNonce rejects CSR nonces shorter than 32 bytes.
func TestBuildCSR_ShortNonce(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalCSRInput(tc.DACKey)
	in.CSRNonce = make([]byte, 31)
	_, err := commissioning.BuildCSR(in)
	if !errors.Is(err, commissioning.ErrCSRNonce) {
		t.Fatalf("err=%v, want ErrCSRNonce", err)
	}
}

// TestBuildCSR_LongNonce rejects CSR nonces longer than 32 bytes.
func TestBuildCSR_LongNonce(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalCSRInput(tc.DACKey)
	in.CSRNonce = make([]byte, 33)
	_, err := commissioning.BuildCSR(in)
	if !errors.Is(err, commissioning.ErrCSRNonce) {
		t.Fatalf("err=%v, want ErrCSRNonce", err)
	}
}

// TestBuildCSR_ShortChallenge rejects attestation challenges shorter than 16 bytes.
func TestBuildCSR_ShortChallenge(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalCSRInput(tc.DACKey)
	in.AttestationChallenge = make([]byte, 15)
	_, err := commissioning.BuildCSR(in)
	if !errors.Is(err, commissioning.ErrAttestationChallenge) {
		t.Fatalf("err=%v, want ErrAttestationChallenge", err)
	}
}

// TestBuildCSR_LongChallenge rejects attestation challenges longer than 16 bytes.
func TestBuildCSR_LongChallenge(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalCSRInput(tc.DACKey)
	in.AttestationChallenge = make([]byte, 17)
	_, err := commissioning.BuildCSR(in)
	if !errors.Is(err, commissioning.ErrAttestationChallenge) {
		t.Fatalf("err=%v, want ErrAttestationChallenge", err)
	}
}

// TestBuildCSR_NilPrivateKey rejects a nil DAC private key.
func TestBuildCSR_NilPrivateKey(t *testing.T) {
	t.Parallel()
	in := minimalCSRInput(nil)
	in.DACPrivateKey = nil
	_, err := commissioning.BuildCSR(in)
	if err == nil {
		t.Fatal("expected error for nil DAC key, got nil")
	}
}

// TestBuildCSR_Success checks the structural guarantees on CSRResult.
func TestBuildCSR_Success(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalCSRInput(tc.DACKey)

	res, err := commissioning.BuildCSR(in)
	if err != nil {
		t.Fatalf("BuildCSR: %v", err)
	}

	// PrivateKey must be a P-256 key.
	if res.PrivateKey == nil {
		t.Fatal("PrivateKey is nil")
	}
	if res.PrivateKey.Curve != elliptic.P256() {
		t.Fatal("PrivateKey is not P-256")
	}

	if len(res.Elements) == 0 {
		t.Fatal("Elements is empty")
	}
	if len(res.Signature) != 64 {
		t.Fatalf("Signature length=%d, want 64", len(res.Signature))
	}
	if len(res.CSR) == 0 {
		t.Fatal("CSR is empty")
	}
}

// TestBuildCSR_CSRIsValid parses the embedded PKCS#10 CSR and checks the signature.
func TestBuildCSR_CSRIsValid(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalCSRInput(tc.DACKey)

	res, err := commissioning.BuildCSR(in)
	if err != nil {
		t.Fatalf("BuildCSR: %v", err)
	}

	req, err := x509.ParseCertificateRequest(res.CSR)
	if err != nil {
		t.Fatalf("ParseCertificateRequest: %v", err)
	}
	if err := req.CheckSignature(); err != nil {
		t.Fatalf("CSR CheckSignature: %v", err)
	}
}

// TestBuildCSR_PublicKeyMatchesPrivateKey asserts that the public key in the CSR
// matches result.PrivateKey.Public().
func TestBuildCSR_PublicKeyMatchesPrivateKey(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalCSRInput(tc.DACKey)

	res, err := commissioning.BuildCSR(in)
	if err != nil {
		t.Fatalf("BuildCSR: %v", err)
	}

	req, err := x509.ParseCertificateRequest(res.CSR)
	if err != nil {
		t.Fatalf("ParseCertificateRequest: %v", err)
	}

	// Compare via DER-encoded PKIX public keys to avoid deprecated X/Y field access.
	gotDER, err := x509.MarshalPKIXPublicKey(req.PublicKey)
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey (CSR): %v", err)
	}
	wantDER, err := x509.MarshalPKIXPublicKey(res.PrivateKey.Public())
	if err != nil {
		t.Fatalf("MarshalPKIXPublicKey (PrivateKey): %v", err)
	}
	if !bytes.Equal(gotDER, wantDER) {
		t.Fatal("CSR public key does not match result.PrivateKey.Public()")
	}
}

// TestBuildCSR_SignatureVerifiable checks that Signature is a valid ECDSA signature
// over SHA256(elements || challenge) verifiable with DACPrivateKey.PublicKey.
func TestBuildCSR_SignatureVerifiable(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	in := minimalCSRInput(tc.DACKey)

	res, err := commissioning.BuildCSR(in)
	if err != nil {
		t.Fatalf("BuildCSR: %v", err)
	}

	h := sha256.New()
	h.Write(res.Elements)
	h.Write(in.AttestationChallenge)
	digest := h.Sum(nil)

	r := new(big.Int).SetBytes(res.Signature[:32])
	s := new(big.Int).SetBytes(res.Signature[32:])

	if !ecdsa.Verify(&tc.DACKey.PublicKey, digest, r, s) {
		t.Fatal("NOCSR signature does not verify against DACPrivateKey.PublicKey")
	}
}

// TestBuildCSR_ElementsTLVContainsCSRAndNonce decodes the NOCSRElements TLV and
// verifies Tag 1 contains the CSR bytes and Tag 2 contains the nonce.
func TestBuildCSR_ElementsTLVContainsCSRAndNonce(t *testing.T) {
	t.Parallel()
	tc := newTestChain(t, time.Now())
	nonce := bytes.Repeat([]byte{0xBE}, 32)
	in := minimalCSRInput(tc.DACKey)
	in.CSRNonce = nonce

	res, err := commissioning.BuildCSR(in)
	if err != nil {
		t.Fatalf("BuildCSR: %v", err)
	}

	csrBytes, nonceBytes := decodedNOCSRElements(t, res.Elements)

	if !bytes.Equal(csrBytes, res.CSR) {
		t.Fatal("Tag 1 in NOCSRElements does not match result.CSR")
	}
	if !bytes.Equal(nonceBytes, nonce) {
		t.Fatalf("Tag 2 in NOCSRElements: got %X, want %X", nonceBytes, nonce)
	}
}

// decodedNOCSRElements extracts Tag 1 (CSR) and Tag 2 (nonce) from the
// NOCSRElements TLV. It fails the test if either field is missing.
func decodedNOCSRElements(t *testing.T, data []byte) (csr, nonce []byte) {
	t.Helper()
	dec := tlv.NewDecoder(data)
	// Consume opening anonymous struct.
	first, err := dec.Next()
	if err != nil {
		t.Fatalf("tlv.Next (struct open): %v", err)
	}
	if !first.IsContainer {
		t.Fatalf("expected container, got type=%v", first.Type)
	}

	for {
		el, err := dec.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			t.Fatalf("tlv.Next: %v", err)
		}
		if el.IsEndContainer {
			break
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch el.Tag.Number {
		case 0x01:
			csr = el.Octets
		case 0x02:
			nonce = el.Octets
		}
	}

	if csr == nil {
		t.Fatal("Tag 1 (CSR) not found in NOCSRElements")
	}
	if nonce == nil {
		t.Fatal("Tag 2 (nonce) not found in NOCSRElements")
	}
	return csr, nonce
}
