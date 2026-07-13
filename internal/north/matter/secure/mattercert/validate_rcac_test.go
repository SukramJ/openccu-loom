// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mattercert_test

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/mattercert"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// buildRCACWith constructs a self-signed RCAC TLV with configurable
// extension fields. All parameters that are not overridden assume
// chip-valid defaults (keyCertSign set, PathLen=1, SKI==AKI).
//
// Mirrors chip CHIPCert.cpp:1116-1144 ValidateChipRCAC — used to pin
// each of the four new checks introduced in chip NEW-1.
type rcacBuildOpts struct {
	rcacID uint64

	// Extensions
	hasBasicConstraints        bool
	basicConstraintsIsCA       bool
	basicConstraintsHasPathLen bool
	basicConstraintsPathLen    uint8

	hasKeyUsage bool
	keyUsage    uint16

	hasSKI bool
	ski    []byte
	hasAKI bool
	aki    []byte

	// When foreignKey is set the cert is signed by a different key
	// (simulates a self-signature failure).
	foreignKey *ecdsa.PrivateKey
}

// buildRCACTLV returns a fully-signed RCAC TLV byte slice, timestamped
// with the current wall clock.
//
// A single nowEpoch() read is threaded through both the probe (signed)
// and the final (returned) cert so they share an identical NotBefore —
// see buildRCACFull for why a second nowEpoch() read here would flake
// the signature.
func buildRCACTLV(t *testing.T, selfPriv *ecdsa.PrivateKey, o rcacBuildOpts) []byte {
	t.Helper()
	return buildRCACTLVAt(t, selfPriv, o, nowEpoch())
}

// buildRCACTLVAt returns a fully-signed RCAC TLV byte slice using the
// caller-supplied timestamp for both the signed probe and the returned
// cert. Exposed (rather than folded into buildRCACTLV) so tests can pin
// a deterministic NotBefore, or deliberately drive the probe and the
// final cert with different timestamps to reproduce the historical
// clock-tick fixture bug.
func buildRCACTLVAt(t *testing.T, selfPriv *ecdsa.PrivateKey, o rcacBuildOpts, now uint64) []byte {
	t.Helper()
	pub := marshalPub(selfPriv)

	// Build a probe cert (zero signature) to compute TBS-DER.
	probeRaw := buildRCACProbe(t, pub, o, now)
	probeCert, err := mattercert.Decode(probeRaw)
	if err != nil {
		t.Fatalf("buildRCACTLVAt: decode probe: %v", err)
	}
	tbsDER, err := mattercert.TBSToDER(probeCert)
	if err != nil {
		t.Fatalf("buildRCACTLVAt: TBSToDER: %v", err)
	}
	hash := sha256.Sum256(tbsDER)

	signingKey := selfPriv
	if o.foreignKey != nil {
		signingKey = o.foreignKey
	}
	r, s, err := ecdsa.Sign(rand.Reader, signingKey, hash[:])
	if err != nil {
		t.Fatalf("buildRCACTLVAt: sign: %v", err)
	}
	sig := make([]byte, 64)
	rb, sb := r.Bytes(), s.Bytes()
	copy(sig[32-len(rb):32], rb)
	copy(sig[64-len(sb):64], sb)

	return buildRCACFull(t, pub, o, sig, now)
}

// buildRCACProbe builds a raw RCAC TLV with a 64-zero-byte signature
// placeholder — needed only to derive the TBS-DER bytes for signing.
// now must be the exact timestamp the caller will also use for the
// final signed cert (see buildRCACTLV).
func buildRCACProbe(t *testing.T, pubKey []byte, o rcacBuildOpts, now uint64) []byte {
	t.Helper()
	return buildRCACFull(t, pubKey, o, make([]byte, 64), now)
}

// buildRCACFull builds the raw RCAC TLV for the given signature and
// timestamp. now is caller-supplied rather than read internally: the
// TBS bytes that get signed and the cert bytes that get returned must
// carry an identical NotBefore, or the self-signature computed over
// one NotBefore will not verify against a cert re-serialised with a
// different one. matter.js keeps this invariant by signing and
// returning the same in-memory object (Certificate.ts:101
// signEcdsa(key, this.asUnsignedDer()); Rcac.ts:140 verifies
// verifyEcdsa(..., this.asUnsignedDer(), this.signature) over that
// same object) instead of re-deriving NotBefore per call.
func buildRCACFull(t *testing.T, pubKey []byte, o rcacBuildOpts, sig []byte, now uint64) []byte {
	t.Helper()
	e := tlv.NewEncoder()
	e.StartStruct(tlv.AnonymousTag())

	e.PutOctets(tlv.ContextTag(1), []byte{0x01}) // SerialNumber
	e.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)

	e.StartList(tlv.ContextTag(3)) // Issuer: RCAC-ID
	e.PutUint(tlv.ContextTag(20), o.rcacID)
	if err := e.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}

	e.PutUint(tlv.ContextTag(4), now-100) // NotBefore
	e.PutUint(tlv.ContextTag(5), 0)       // NotAfter = 0 (no expiry)

	e.StartList(tlv.ContextTag(6)) // Subject: RCAC-ID (same as Issuer → self-signed)
	e.PutUint(tlv.ContextTag(20), o.rcacID)
	if err := e.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}

	e.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	e.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	e.PutOctets(tlv.ContextTag(9), pubKey)

	e.StartList(tlv.ContextTag(10)) // Extensions

	if o.hasBasicConstraints {
		e.StartStruct(tlv.ContextTag(1)) // basic-constraints
		e.PutBool(tlv.ContextTag(1), o.basicConstraintsIsCA)
		if o.basicConstraintsHasPathLen {
			e.PutUint(tlv.ContextTag(2), uint64(o.basicConstraintsPathLen))
		}
		if err := e.EndContainer(); err != nil {
			t.Fatalf("basic-constraints end: %v", err)
		}
	}
	if o.hasKeyUsage {
		e.PutUint(tlv.ContextTag(2), uint64(o.keyUsage))
	}
	if o.hasSKI {
		e.PutOctets(tlv.ContextTag(4), o.ski)
	}
	if o.hasAKI {
		e.PutOctets(tlv.ContextTag(5), o.aki)
	}

	if err := e.EndContainer(); err != nil {
		t.Fatalf("extensions end: %v", err)
	}

	e.PutOctets(tlv.ContextTag(11), sig)

	if err := e.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return raw
}

// validRCACOpts returns a chip-valid RCAC option set.
func validRCACOpts(priv *ecdsa.PrivateKey) rcacBuildOpts {
	ski := make([]byte, 20)
	for i := range ski {
		ski[i] = byte(i + 1)
	}
	aki := make([]byte, 20)
	copy(aki, ski) // SKI == AKI on self-signed
	return rcacBuildOpts{
		rcacID:                     0x0001,
		hasBasicConstraints:        true,
		basicConstraintsIsCA:       true,
		basicConstraintsHasPathLen: true,
		basicConstraintsPathLen:    1,
		hasKeyUsage:                true,
		keyUsage:                   mattercert.KeyUsageKeyCertSign, // chip only requires keyCertSign
		hasSKI:                     true,
		ski:                        ski,
		hasAKI:                     true,
		aki:                        aki,
		foreignKey:                 nil, // signed by self
	}
}

// TestValidateRCAC_Valid verifies that a chip-conformant RCAC passes.
func TestValidateRCAC_Valid(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	raw := buildRCACTLV(t, priv, validRCACOpts(priv))
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if err := mattercert.ValidateRCAC(cert); err != nil {
		t.Fatalf("ValidateRCAC unexpectedly rejected valid RCAC: %v", err)
	}
}

// TestValidateRCAC_CRLSignAbsent verifies that an RCAC with keyCertSign
// but without cRLSign is accepted. chip does NOT require cRLSign;
// OpenCCU-Loom must not reject it.
// Mirrors chip CHIPCert.cpp:1141 — only kKeyCertSign is mandatory.
func TestValidateRCAC_CRLSignAbsent(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	opts := validRCACOpts(priv)
	opts.keyUsage = mattercert.KeyUsageKeyCertSign // no cRLSign
	raw := buildRCACTLV(t, priv, opts)
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if err := mattercert.ValidateRCAC(cert); err != nil {
		t.Errorf("ValidateRCAC rejected cRLSign-absent RCAC: chip only requires keyCertSign; got: %v", err)
	}
}

// TestValidateRCAC_SKIMismatch verifies that an RCAC where
// SubjectKeyId != AuthorityKeyId is rejected.
// Mirrors chip CHIPCert.cpp:1133 mSubjectKeyId.data_equal(mAuthKeyId).
func TestValidateRCAC_SKIMismatch(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	opts := validRCACOpts(priv)
	// Corrupt AKI so it differs from SKI.
	opts.aki = make([]byte, 20)
	for i := range opts.aki {
		opts.aki[i] = 0xFF
	}
	raw := buildRCACTLV(t, priv, opts)
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	err = mattercert.ValidateRCAC(cert)
	if !errors.Is(err, mattercert.ErrInvalidRCAC) {
		t.Errorf("expected ErrInvalidRCAC for SKI!=AKI, got %v", err)
	}
}

// TestValidateRCAC_PathLenAbsent verifies that an RCAC without a
// BasicConstraints PathLenConstraint is accepted. chip-tool and the
// default openssl-generated commissioner RCACs omit the constraint
// (an absent constraint means "no caller-imposed depth limit") and
// chip CHIPCert.cpp:1136 gates the upper-bound check on its presence.
// A stricter validation here rejected every chip-tool commissioning
// at SendTrustedRootCert; see the regression diagnosis on
// audit/full-parity.
func TestValidateRCAC_PathLenAbsent(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	opts := validRCACOpts(priv)
	opts.basicConstraintsHasPathLen = false
	opts.basicConstraintsPathLen = 0 // ignored when HasPathLen=false
	raw := buildRCACTLV(t, priv, opts)
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if err := mattercert.ValidateRCAC(cert); err != nil {
		t.Errorf("ValidateRCAC rejected RCAC with absent PathLenConstraint (chip accepts these): %v", err)
	}
}

// TestValidateRCAC_PathLenZero verifies that PathLenConstraint=0 is
// accepted. chip CHIPCert.cpp:1138 only checks `<= 1`, so PathLen=0
// is in range. Rare but legal — represents a root that has explicitly
// disallowed intermediate signing.
func TestValidateRCAC_PathLenZero(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	opts := validRCACOpts(priv)
	opts.basicConstraintsPathLen = 0
	raw := buildRCACTLV(t, priv, opts)
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if err := mattercert.ValidateRCAC(cert); err != nil {
		t.Errorf("ValidateRCAC rejected RCAC with PathLenConstraint=0 (chip only enforces `<= 1`): %v", err)
	}
}

// TestValidateRCAC_PathLenTooLarge verifies that PathLenConstraint=2
// is rejected (chip allows at most 1 ICAC).
// Mirrors chip CHIPCert.cpp:1139-1140 pathLenConstraint <= 1.
func TestValidateRCAC_PathLenTooLarge(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	opts := validRCACOpts(priv)
	opts.basicConstraintsPathLen = 2
	raw := buildRCACTLV(t, priv, opts)
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	err = mattercert.ValidateRCAC(cert)
	if !errors.Is(err, mattercert.ErrInvalidRCAC) {
		t.Errorf("expected ErrInvalidRCAC for PathLen=2, got %v", err)
	}
}

// TestValidateRCAC_SelfSigFail verifies that an RCAC signed by a
// foreign key (self-signature fails) is rejected.
// Mirrors chip CHIPCert.cpp:1144 VerifyCertSignature(certData, certData).
func TestValidateRCAC_SelfSigFail(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	foreign, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	opts := validRCACOpts(priv)
	opts.foreignKey = foreign // sign with a different key
	raw := buildRCACTLV(t, priv, opts)
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	err = mattercert.ValidateRCAC(cert)
	if !errors.Is(err, mattercert.ErrInvalidRCAC) {
		t.Errorf("expected ErrInvalidRCAC for foreign-signed RCAC, got %v", err)
	}
}

// TestValidateRCAC_RejectsWhenSignedNotBeforeDiffersFromCert pins the
// invariant that made TestValidateRCAC_PathLenAbsent flake ~0.04% of
// runs before the fixture was fixed: a cert must be verified against
// the exact bytes that were signed. It reproduces the failure
// deterministically by signing the TBS at one timestamp and then
// emitting the returned cert at a later one — the same skew that used
// to occur when a 1-second wall-clock boundary fell between the two
// separate nowEpoch() reads inside the old buildRCACFull.
//
// ValidateRCAC recomputes the TBS-DER from the certificate it was
// actually handed (mattercert.TBSToDER, called from verifySignature),
// so a NotBefore mismatch between the signed bytes and the returned
// cert must always surface as a signature failure — never an
// intermittent pass.
func TestValidateRCAC_RejectsWhenSignedNotBeforeDiffersFromCert(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := marshalPub(priv)
	opts := validRCACOpts(priv)

	const signedAt = uint64(1_000_000)
	const emittedAt = signedAt + 1 // simulates a one-second clock tick

	// Sign the TBS-DER for the cert as it looks at signedAt ...
	probeRaw := buildRCACProbe(t, pub, opts, signedAt)
	probeCert, err := mattercert.Decode(probeRaw)
	if err != nil {
		t.Fatalf("decode probe: %v", err)
	}
	tbsDER, err := mattercert.TBSToDER(probeCert)
	if err != nil {
		t.Fatalf("TBSToDER: %v", err)
	}
	hash := sha256.Sum256(tbsDER)
	r, s, err := ecdsa.Sign(rand.Reader, priv, hash[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	sig := make([]byte, 64)
	rb, sb := r.Bytes(), s.Bytes()
	copy(sig[32-len(rb):32], rb)
	copy(sig[64-len(sb):64], sb)

	// ... but emit the cert that ValidateRCAC actually receives at
	// emittedAt, so its NotBefore no longer matches what was signed.
	finalRaw := buildRCACFull(t, pub, opts, sig, emittedAt)
	cert, err := mattercert.Decode(finalRaw)
	if err != nil {
		t.Fatalf("decode final: %v", err)
	}

	err = mattercert.ValidateRCAC(cert)
	if !errors.Is(err, mattercert.ErrSignatureInvalid) {
		t.Fatalf("expected ErrSignatureInvalid for a cert whose NotBefore drifted between signing and emission, got %v", err)
	}
}

// fixedP256RCACKey returns a deterministic (non-random) P-256 key derived
// from a fixed seed reader, so the whole fixture — key, timestamp, and
// signature inputs — is reproducible across runs without crypto/rand. It
// uses ecdsa.GenerateKey rather than assigning the raw PublicKey
// coordinates, which is deprecated since Go 1.26.
func fixedP256RCACKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	seed := bytes.Repeat([]byte{
		0x6c, 0x7d, 0x78, 0x5b, 0x2a, 0x14, 0x9e, 0x3f,
		0x01, 0x88, 0x4c, 0x5b, 0x22, 0x77, 0x9d, 0x6e,
	}, 8)
	priv, err := ecdsa.GenerateKey(elliptic.P256(), bytes.NewReader(seed))
	if err != nil {
		t.Fatalf("fixedP256RCACKey: %v", err)
	}
	return priv
}

// TestValidateRCAC_FixedVectorSelfSignatureVerifies locks the "sign
// and verify the identical DER" invariant with a fully deterministic
// key and timestamp (no ecdsa.GenerateKey, no wall clock): the RCAC's
// self-signature must verify when the TBS-DER that gets hashed is the
// TBS-DER that was actually signed.
//
// Mirrors matter.js packages/.../Rcac.ts:140
// verifyEcdsa(PublicKey(...), this.asUnsignedDer(), this.signature),
// which checks the self-signature against the same in-memory
// certificate object that Certificate.ts:101 signEcdsa(key,
// this.asUnsignedDer()) signed — never a re-derived copy.
func TestValidateRCAC_FixedVectorSelfSignatureVerifies(t *testing.T) {
	t.Parallel()
	priv := fixedP256RCACKey(t)
	opts := validRCACOpts(priv)
	const fixedNow = uint64(500_000_000)

	raw := buildRCACTLVAt(t, priv, opts, fixedNow)
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if err := mattercert.ValidateRCAC(cert); err != nil {
		t.Fatalf("ValidateRCAC rejected fixed-vector self-signed RCAC: %v", err)
	}
}
