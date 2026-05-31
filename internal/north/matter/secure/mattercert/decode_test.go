// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mattercert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// buildTestCert constructs a minimal valid Matter cert TLV using the supplied
// parameters. Pass sig == nil to omit the Signature field, sigLen to control
// signature length.  Set pubKey to override the 65-byte public key slot.
// Set sigAlgo / curveID / pubAlgo to override algorithm fields.
func buildTestCert(t *testing.T, opts certOptions) []byte {
	t.Helper()
	e := tlv.NewEncoder()
	e.StartStruct(tlv.AnonymousTag())

	// tag 1: SerialNumber
	serial := opts.serial
	if serial == nil {
		serial = []byte{0x01}
	}
	e.PutOctets(tlv.ContextTag(1), serial)

	// tag 2: SignatureAlgorithm
	sigAlgo := opts.sigAlgo
	if sigAlgo == 0 {
		sigAlgo = SigAlgoECDSAWithSHA256
	}
	e.PutUint(tlv.ContextTag(2), sigAlgo)

	// tag 3: Issuer (List container) — DN tag 17/19/20/21/22 per
	// Matter Core Spec §6.5.6.1 Table 60.
	e.StartList(tlv.ContextTag(3))
	e.PutUint(tlv.ContextTag(20), opts.issuerRCACID)
	if opts.issuerHasICACID {
		e.PutUint(tlv.ContextTag(19), opts.issuerICACID)
	}
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer issuer: %v", err)
	}

	// tag 4: NotBefore
	e.PutUint(tlv.ContextTag(4), opts.notBefore)
	// tag 5: NotAfter
	e.PutUint(tlv.ContextTag(5), opts.notAfter)

	// tag 6: Subject (List container)
	e.StartList(tlv.ContextTag(6))
	if opts.subjectHasRCACID {
		e.PutUint(tlv.ContextTag(20), opts.subjectRCACID)
	}
	if opts.subjectHasICACID {
		e.PutUint(tlv.ContextTag(19), opts.subjectICACID)
	}
	if opts.subjectHasNodeID {
		e.PutUint(tlv.ContextTag(17), opts.subjectNodeID)
	}
	if opts.subjectHasFabricID {
		e.PutUint(tlv.ContextTag(21), opts.subjectFabricID)
	}
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer subject: %v", err)
	}

	// tag 7: PublicKeyAlgorithm
	pubAlgo := opts.pubAlgo
	if pubAlgo == 0 {
		pubAlgo = PubKeyAlgoEC
	}
	e.PutUint(tlv.ContextTag(7), pubAlgo)

	// tag 8: EllipticCurveID
	curveID := opts.curveID
	if curveID == 0 {
		curveID = CurvePrime256v1
	}
	e.PutUint(tlv.ContextTag(8), curveID)

	// tag 9: PublicKey (65-byte uncompressed P-256)
	pubKey := opts.pubKey
	if pubKey == nil {
		pubKey = makeValidPubKey(t)
	}
	e.PutOctets(tlv.ContextTag(9), pubKey)

	// tag 10: Extensions (empty list)
	e.StartList(tlv.ContextTag(10))
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer extensions: %v", err)
	}

	// tag 11: Signature
	if !opts.omitSignature {
		sig := opts.signature
		if sig == nil {
			sig = make([]byte, 64)
		}
		e.PutOctets(tlv.ContextTag(11), sig)
	}

	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer top: %v", err)
	}
	raw, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return raw
}

type certOptions struct {
	serial             []byte
	sigAlgo            uint64
	issuerRCACID       uint64
	issuerHasICACID    bool
	issuerICACID       uint64
	notBefore          uint64
	notAfter           uint64
	subjectHasRCACID   bool
	subjectRCACID      uint64
	subjectHasICACID   bool
	subjectICACID      uint64
	subjectHasNodeID   bool
	subjectNodeID      uint64
	subjectHasFabricID bool
	subjectFabricID    uint64
	pubAlgo            uint64
	curveID            uint64
	pubKey             []byte
	signature          []byte
	omitSignature      bool
}

func makeValidPubKey(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019: elliptic.Marshal is the canonical raw-point encoding for Matter TLV test fixtures
}

// makeRootCertOpts returns options for a Root CA cert (Subject carries
// matter-rcac-id only).
func makeRootCertOpts() certOptions {
	return certOptions{
		issuerRCACID:     0x0001,
		notBefore:        1000,
		notAfter:         0,
		subjectHasRCACID: true,
		subjectRCACID:    0x0001,
	}
}

// makeICACOpts returns options for an ICAC cert (Subject:
// matter-icac-id + matter-fabric-id).
func makeICACOpts() certOptions {
	return certOptions{
		issuerRCACID:       0x0001,
		notBefore:          1000,
		notAfter:           0,
		subjectHasICACID:   true,
		subjectICACID:      0x1234,
		subjectHasFabricID: true,
		subjectFabricID:    0xBEEF,
	}
}

// makeNOCOpts returns options for a NOC cert (Subject: matter-node-id
// + matter-fabric-id; Issuer references the ICAC).
func makeNOCOpts() certOptions {
	return certOptions{
		issuerHasICACID:    true,
		issuerICACID:       0x1234,
		notBefore:          1000,
		notAfter:           0,
		subjectHasNodeID:   true,
		subjectNodeID:      0xDEAD,
		subjectHasFabricID: true,
		subjectFabricID:    0xBEEF,
	}
}

// ---- Tests ----

func TestDecode_TruncatedInput(t *testing.T) {
	t.Parallel()
	_, err := Decode([]byte{})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("expected ErrTruncated, got %v", err)
	}
}

func TestDecode_NonStructureTop(t *testing.T) {
	t.Parallel()
	// Just a uint8 element with anonymous tag.
	e := tlv.NewEncoder()
	e.PutUint(tlv.AnonymousTag(), 42)
	raw, _ := e.Bytes()
	_, err := Decode(raw)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for non-Structure top, got %v", err)
	}
}

func TestDecode_ValidNOCSubjectFields(t *testing.T) {
	t.Parallel()
	// NOC Subject: tag 17 (matter-node-id=0xDEAD) + tag 21 (matter-fabric-id=0xBEEF).
	opts := makeNOCOpts()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	opts.pubKey = elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019: elliptic.Marshal is canonical for Matter TLV raw-point fixtures
	raw := buildTestCert(t, opts)
	cert, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode valid NOC: %v", err)
	}
	if cert.Subject.MatterNodeID != 0xDEAD {
		t.Errorf("MatterNodeID = %X, want DEAD", cert.Subject.MatterNodeID)
	}
	if !cert.Subject.HasFabricID {
		t.Error("HasFabricID should be true for NOC")
	}
	if cert.Subject.MatterFabricID != 0xBEEF {
		t.Errorf("MatterFabricID = %X, want BEEF", cert.Subject.MatterFabricID)
	}
	if !cert.IsNOC() {
		t.Error("IsNOC should be true")
	}
}

func TestDecode_MissingSignature(t *testing.T) {
	t.Parallel()
	opts := makeRootCertOpts()
	opts.omitSignature = true
	raw := buildTestCert(t, opts)
	_, err := Decode(raw)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for missing signature, got %v", err)
	}
}

func TestDecode_SignatureWrongLength(t *testing.T) {
	t.Parallel()
	opts := makeRootCertOpts()
	opts.signature = make([]byte, 63) // not 64
	raw := buildTestCert(t, opts)
	_, err := Decode(raw)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for wrong sig length, got %v", err)
	}
}

func TestDecode_UnsupportedSigAlgo(t *testing.T) {
	t.Parallel()
	opts := makeRootCertOpts()
	opts.sigAlgo = 99 // unsupported
	raw := buildTestCert(t, opts)
	_, err := Decode(raw)
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("expected ErrUnsupportedAlgorithm, got %v", err)
	}
}

func TestDecode_IsRoot(t *testing.T) {
	t.Parallel()
	opts := makeRootCertOpts()
	raw := buildTestCert(t, opts)
	cert, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode root: %v", err)
	}
	if !cert.IsRoot() {
		t.Error("IsRoot should be true for root cert")
	}
	if cert.IsICA() {
		t.Error("IsICA should be false for root cert")
	}
	if cert.IsNOC() {
		t.Error("IsNOC should be false for root cert")
	}
	if !cert.Subject.HasRCACID {
		t.Error("HasRCACID should be true")
	}
}

func TestDecode_IsICA(t *testing.T) {
	t.Parallel()
	opts := makeICACOpts()
	raw := buildTestCert(t, opts)
	cert, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode ICAC: %v", err)
	}
	if cert.IsRoot() {
		t.Error("IsRoot should be false for ICAC cert")
	}
	if !cert.IsICA() {
		t.Error("IsICA should be true for ICAC cert")
	}
	if cert.IsNOC() {
		t.Error("IsNOC should be false for ICAC cert")
	}
}

func TestDecode_IsNOC(t *testing.T) {
	t.Parallel()
	opts := makeNOCOpts()
	raw := buildTestCert(t, opts)
	cert, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode NOC: %v", err)
	}
	if cert.IsRoot() {
		t.Error("IsRoot should be false for NOC")
	}
	if cert.IsICA() {
		t.Error("IsICA should be false for NOC")
	}
	if !cert.IsNOC() {
		t.Error("IsNOC should be true for NOC")
	}
}

func TestDecode_PublicKeyECDSA_NonP256Curve(t *testing.T) {
	t.Parallel()
	opts := makeRootCertOpts()
	opts.curveID = 99 // not P-256
	raw := buildTestCert(t, opts)
	_, err := Decode(raw)
	if !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("expected ErrUnsupportedAlgorithm for non-P256 curveID, got %v", err)
	}
}

func TestDecode_PublicKeyECDSA_OffCurvePoint(t *testing.T) {
	t.Parallel()
	opts := makeRootCertOpts()
	// Craft a 65-byte key that starts with 0x04 but is not on the curve.
	badKey := make([]byte, 65)
	badKey[0] = 0x04
	for i := 1; i < 65; i++ {
		badKey[i] = 0xFF
	}
	opts.pubKey = badKey
	raw := buildTestCert(t, opts)
	cert, err := Decode(raw)
	if err != nil {
		// Decode may succeed (validateMandatory only checks prefix/length),
		// but PublicKeyECDSA must fail.
		if errors.Is(err, ErrMalformed) {
			return // acceptable if decode itself rejects
		}
		t.Fatalf("Decode: unexpected error %v", err)
	}
	_, err = cert.PublicKeyECDSA()
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("PublicKeyECDSA off-curve: expected ErrMalformed, got %v", err)
	}
}

func TestDecode_PublicKeyECDSA_ValidKey(t *testing.T) {
	t.Parallel()
	opts := makeRootCertOpts()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	opts.pubKey = elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019: elliptic.Marshal is canonical for Matter TLV raw-point fixtures
	raw := buildTestCert(t, opts)
	cert, err := Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	pk, err := cert.PublicKeyECDSA()
	if err != nil {
		t.Fatalf("PublicKeyECDSA: %v", err)
	}
	// Compare via ECDH bytes to avoid deprecated ecdsa.PublicKey.X direct access.
	// crypto/ecdh is the recommended comparison path for Go 1.26+.
	ecdhPriv, err := priv.ECDH()
	if err != nil {
		t.Fatalf("ECDH: %v", err)
	}
	ecdhPub, err := pk.ECDH()
	if err != nil {
		t.Fatalf("ECDH pub: %v", err)
	}
	if ecdhPriv.PublicKey().Equal(ecdhPub) == false {
		t.Error("PublicKeyECDSA returned wrong key")
	}
}
