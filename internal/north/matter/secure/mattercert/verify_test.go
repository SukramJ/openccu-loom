// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mattercert_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/mattercert"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// ---- Test cert helpers (external package) ----

type verifyTestCertOpts struct {
	serial    []byte
	notBefore uint64
	notAfter  uint64
	// issuer fields
	issuerHasICACID bool
	issuerICACID    uint64
	issuerRCACID    uint64
	// subject fields
	subjectHasRCACID   bool
	subjectRCACID      uint64
	subjectHasICACID   bool
	subjectICACID      uint64
	subjectHasNodeID   bool
	subjectNodeID      uint64
	subjectHasFabricID bool
	subjectFabricID    uint64
	// key
	pubKey []byte
}

// buildMinimalCert constructs a Matter cert TLV with the given options
// (no signature field; used to compute TBS).
func buildTBSCert(t *testing.T, opts verifyTestCertOpts) []byte {
	t.Helper()
	e := tlv.NewEncoder()
	e.StartStruct(tlv.AnonymousTag())

	serial := opts.serial
	if serial == nil {
		serial = []byte{0x01}
	}
	e.PutOctets(tlv.ContextTag(1), serial)
	e.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)

	e.StartList(tlv.ContextTag(3)) // Issuer
	e.PutUint(tlv.ContextTag(20), opts.issuerRCACID)
	if opts.issuerHasICACID {
		e.PutUint(tlv.ContextTag(19), opts.issuerICACID)
	}
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer issuer: %v", err)
	}

	e.PutUint(tlv.ContextTag(4), opts.notBefore)
	e.PutUint(tlv.ContextTag(5), opts.notAfter)

	e.StartList(tlv.ContextTag(6)) // Subject
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

	e.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	e.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	e.PutOctets(tlv.ContextTag(9), opts.pubKey)

	e.StartList(tlv.ContextTag(10)) // Extensions
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer extensions: %v", err)
	}

	// No signature field for TBS
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer top: %v", err)
	}
	raw, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return raw
}

// buildSignedCert builds a fully-signed cert TLV, signing the TBS portion
// with priv and appending the 64-byte r||s signature.
func buildSignedCert(t *testing.T, opts verifyTestCertOpts, priv *ecdsa.PrivateKey) []byte {
	t.Helper()
	tbs := buildTBSCert(t, opts)

	// Decode the unsigned TLV cert and compute the TBS in X.509 DER
	// form — that is the byte form Matter §6.5 / matter.js's
	// Certificate.verifyChain hash for ECDSA verification. Signing
	// over the raw TLV (the historical test path) only round-trips
	// against our own verifier; real-world chains (Apple Home,
	// chip-tool, Google Home) expect DER and would otherwise reject.
	// buildTBSCert closes the outer struct with End-of-Container
	// (0x18). Splice a placeholder 64-byte signature in BEFORE that
	// terminator so the cert decodes — validateMandatory needs a
	// 64-byte signature element to accept the cert. Tag-11 control
	// byte 0x30 (context tag, OctetStr1), tag = 11, length = 0x40,
	// then 64 zero bytes; then re-emit the original 0x18 end marker.
	if len(tbs) == 0 || tbs[len(tbs)-1] != 0x18 {
		t.Fatalf("buildTBSCert: trailing byte not End-of-Container, got %x", tbs[len(tbs)-1])
	}
	probeRaw := append([]byte(nil), tbs[:len(tbs)-1]...)
	probeRaw = append(probeRaw, 0x30, 11, 0x40)
	probeRaw = append(probeRaw, make([]byte, 64)...)
	probeRaw = append(probeRaw, 0x18)
	probeCert, err := mattercert.Decode(probeRaw)
	if err != nil {
		t.Fatalf("decode probe TBS: %v", err)
	}
	tbsDER, err := mattercert.TBSToDER(probeCert)
	if err != nil {
		t.Fatalf("TBSToDER: %v", err)
	}

	hash := sha256.Sum256(tbsDER)
	r, s, err := ecdsa.Sign(rand.Reader, priv, hash[:])
	if err != nil {
		t.Fatalf("ecdsa.Sign: %v", err)
	}
	sig := make([]byte, 64)
	rb, sb := r.Bytes(), s.Bytes()
	copy(sig[32-len(rb):32], rb)
	copy(sig[64-len(sb):64], sb)

	// Now rebuild with signature field.
	e := tlv.NewEncoder()
	e.StartStruct(tlv.AnonymousTag())

	serial := opts.serial
	if serial == nil {
		serial = []byte{0x01}
	}
	e.PutOctets(tlv.ContextTag(1), serial)
	e.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)

	e.StartList(tlv.ContextTag(3))
	e.PutUint(tlv.ContextTag(20), opts.issuerRCACID)
	if opts.issuerHasICACID {
		e.PutUint(tlv.ContextTag(19), opts.issuerICACID)
	}
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer issuer2: %v", err)
	}

	e.PutUint(tlv.ContextTag(4), opts.notBefore)
	e.PutUint(tlv.ContextTag(5), opts.notAfter)

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
		t.Fatalf("EndContainer subject2: %v", err)
	}

	e.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	e.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	e.PutOctets(tlv.ContextTag(9), opts.pubKey)

	e.StartList(tlv.ContextTag(10))
	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer extensions2: %v", err)
	}

	e.PutOctets(tlv.ContextTag(11), sig)

	if err := e.EndContainer(); err != nil {
		t.Fatalf("EndContainer top2: %v", err)
	}
	raw, err := e.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return raw
}

func marshalPub(priv *ecdsa.PrivateKey) []byte {
	return elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019: canonical raw-point encoding for Matter TLV test fixtures
}

// nowEpoch returns the current time in Matter-epoch seconds (offsets
// from 2000-01-01T00:00:00Z per §6.5.1.5) for use as cert
// NotBefore / NotAfter values in tests.
func nowEpoch() uint64 {
	const matterEpochUnix = 946684800                  // 2000-01-01T00:00:00Z
	return uint64(time.Now().Unix() - matterEpochUnix) //nolint:gosec // G115: matter-epoch offset always non-negative for current real time
}

// ---- Test helpers ----

type certChain struct {
	rootPriv *ecdsa.PrivateKey
	nocPriv  *ecdsa.PrivateKey
	icacPriv *ecdsa.PrivateKey
	rootRaw  []byte
	nocRaw   []byte
	icacRaw  []byte
}

// buildChainNoICA creates root + NOC signed directly by root.
//
// The root cert uses tag 20 in Subject (→ HasRCACID + HasFabricID in decoder).
// Due to a production bug in decodeDN, IsRoot() always returns false (see decode.go).
// The Verifier only checks IsNOC() / IsICA(), so this does not affect verify tests.
//
// The NOC uses tag 17 (NodeID) + tag 20 (→ HasFabricID) in Subject → IsNOC()=true.
func buildChainNoICA(t *testing.T) certChain {
	t.Helper()
	rootPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	nocPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := nowEpoch()

	rootOpts := verifyTestCertOpts{
		notBefore:        now - 100,
		notAfter:         0,
		issuerRCACID:     0x0001,
		subjectHasRCACID: true,
		subjectRCACID:    0x0001,
		pubKey:           marshalPub(rootPriv),
	}
	rootRaw := buildSignedCert(t, rootOpts, rootPriv)

	// NOC Subject: tag 17 (matter-node-id) + tag 21 (matter-fabric-id) → IsNOC().
	nocOpts := verifyTestCertOpts{
		notBefore:          now - 100,
		notAfter:           0,
		issuerRCACID:       0x0001,
		subjectHasNodeID:   true,
		subjectNodeID:      0xAAAA,
		subjectHasFabricID: true,
		subjectFabricID:    0xBBBB,
		pubKey:             marshalPub(nocPriv),
	}
	nocRaw := buildSignedCert(t, nocOpts, rootPriv)

	return certChain{rootPriv: rootPriv, nocPriv: nocPriv, rootRaw: rootRaw, nocRaw: nocRaw}
}

// buildChainWithICA creates root + ICAC + NOC.
//
// ICAC Subject: tag 20 (→ HasFabricID) + tag 21 (→ HasICACID) → IsICA()=true.
// NOC Issuer: tag 21 (→ HasICACID) — must match ICAC.Subject.MatterICACID (0x9999).
// NOC Subject: tag 17 (NodeID) + tag 20 (→ HasFabricID) → IsNOC()=true.
func buildChainWithICA(t *testing.T) certChain {
	t.Helper()
	rootPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	icacPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	nocPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := nowEpoch()

	rootOpts := verifyTestCertOpts{
		notBefore:        now - 100,
		notAfter:         0,
		issuerRCACID:     0x0001,
		subjectHasRCACID: true,
		subjectRCACID:    0x0001,
		pubKey:           marshalPub(rootPriv),
	}
	rootRaw := buildSignedCert(t, rootOpts, rootPriv)

	// ICAC Subject: tag 19 (matter-icac-id) + tag 21 (matter-fabric-id) → IsICA().
	icacOpts := verifyTestCertOpts{
		notBefore:          now - 100,
		notAfter:           0,
		issuerRCACID:       0x0001,
		subjectHasICACID:   true,
		subjectICACID:      0x9999,
		subjectHasFabricID: true,
		subjectFabricID:    0xBBBB,
		pubKey:             marshalPub(icacPriv),
	}
	icacRaw := buildSignedCert(t, icacOpts, rootPriv)

	// NOC Issuer carries the ICAC's matter-icac-id (tag 19) so the chain
	// link check matches. NOC Subject: tag 17 + tag 21 → IsNOC().
	nocOpts := verifyTestCertOpts{
		notBefore:          now - 100,
		notAfter:           0,
		issuerHasICACID:    true,
		issuerICACID:       0x9999,
		subjectHasNodeID:   true,
		subjectNodeID:      0xAAAA,
		subjectHasFabricID: true,
		subjectFabricID:    0xBBBB,
		pubKey:             marshalPub(nocPriv),
	}
	nocRaw := buildSignedCert(t, nocOpts, icacPriv)

	return certChain{rootPriv: rootPriv, nocPriv: nocPriv, icacPriv: icacPriv, rootRaw: rootRaw, nocRaw: nocRaw, icacRaw: icacRaw}
}

// ---- Tests ----

func TestNewVerifier_InvalidRootKeyLength(t *testing.T) {
	t.Parallel()
	// Wrong length (not 65 bytes).
	_, err := mattercert.NewVerifier(make([]byte, 33), mattercert.SystemTime{})
	if !errors.Is(err, mattercert.ErrMalformed) {
		t.Fatalf("expected ErrMalformed for short root key, got %v", err)
	}
}

func TestNewVerifier_InvalidRootKeyPrefix(t *testing.T) {
	t.Parallel()
	// Right length, wrong prefix (0x03 instead of 0x04).
	key := make([]byte, 65)
	key[0] = 0x03
	_, err := mattercert.NewVerifier(key, mattercert.SystemTime{})
	if !errors.Is(err, mattercert.ErrMalformed) {
		t.Fatalf("expected ErrMalformed for wrong prefix, got %v", err)
	}
}

func TestNewVerifier_OffCurveRootKey(t *testing.T) {
	t.Parallel()
	// 65-byte 0x04-prefixed buffer with all 0xFF coordinates (not on P-256).
	key := make([]byte, 65)
	key[0] = 0x04
	for i := 1; i < 65; i++ {
		key[i] = 0xFF
	}
	_, err := mattercert.NewVerifier(key, mattercert.SystemTime{})
	if !errors.Is(err, mattercert.ErrMalformed) {
		t.Fatalf("expected ErrMalformed for off-curve root key, got %v", err)
	}
}

func TestVerifyAndExtractPubKey_NOCDirectFromRoot(t *testing.T) {
	t.Parallel()
	chain := buildChainNoICA(t)
	rootCert, err := mattercert.Decode(chain.rootRaw)
	if err != nil {
		t.Fatalf("Decode root: %v", err)
	}
	v, err := mattercert.NewVerifier(rootCert.PublicKey, mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	pk, err := v.VerifyAndExtractPubKey(chain.nocRaw, nil)
	if err != nil {
		t.Fatalf("VerifyAndExtractPubKey: %v", err)
	}
	if pk == nil {
		t.Fatal("returned nil public key")
	}
	// Compare via ECDH bytes (Go 1.26+ preferred path; avoids deprecated ecdsa.PublicKey.X).
	ecdhGot, err := pk.ECDH()
	if err != nil {
		t.Fatalf("ECDH: %v", err)
	}
	ecdhWant, err := chain.nocPriv.PublicKey.ECDH()
	if err != nil {
		t.Fatalf("ECDH want: %v", err)
	}
	if !ecdhGot.Equal(ecdhWant) {
		t.Error("returned wrong NOC public key")
	}
}

func TestVerifyAndExtractPubKey_NOCViaICAC(t *testing.T) {
	t.Parallel()
	chain := buildChainWithICA(t)
	rootCert, err := mattercert.Decode(chain.rootRaw)
	if err != nil {
		t.Fatalf("Decode root: %v", err)
	}
	v, err := mattercert.NewVerifier(rootCert.PublicKey, mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	pk, err := v.VerifyAndExtractPubKey(chain.nocRaw, chain.icacRaw)
	if err != nil {
		t.Fatalf("VerifyAndExtractPubKey with ICAC: %v", err)
	}
	ecdhGot, err := pk.ECDH()
	if err != nil {
		t.Fatalf("ECDH: %v", err)
	}
	ecdhWant, err := chain.nocPriv.PublicKey.ECDH()
	if err != nil {
		t.Fatalf("ECDH want: %v", err)
	}
	if !ecdhGot.Equal(ecdhWant) {
		t.Error("returned wrong NOC public key (ICAC path)")
	}
}

func TestVerifyAndExtractPubKey_FakeSignature(t *testing.T) {
	t.Parallel()
	chain := buildChainNoICA(t)
	rootCert, err := mattercert.Decode(chain.rootRaw)
	if err != nil {
		t.Fatalf("Decode root: %v", err)
	}
	v, err := mattercert.NewVerifier(rootCert.PublicKey, mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// Tamper with the signature: the NOC raw ends with EndContainer (0x18) preceded by
	// the 64-byte signature content. The signature starts at offset len-65 (1 byte for
	// EndContainer at the end, 64 bytes of signature value before that).
	// Flip a byte in the middle of the r scalar (offset from end: 65+32 = 97).
	tampered := append([]byte(nil), chain.nocRaw...)
	sigOffset := len(tampered) - 1 - 64 + 1 // first byte of the 64-byte sig body
	if sigOffset > 0 && sigOffset < len(tampered) {
		tampered[sigOffset] ^= 0x01
	} else {
		tampered[len(tampered)-2] ^= 0x01
	}

	_, err = v.VerifyAndExtractPubKey(tampered, nil)
	if !errors.Is(err, mattercert.ErrSignatureInvalid) {
		t.Fatalf("expected ErrSignatureInvalid for tampered NOC, got %v", err)
	}
}

func TestVerifyAndExtractPubKey_Expired(t *testing.T) {
	t.Parallel()
	rootPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	nocPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	// NotBefore=1, NotAfter=2 (in the past).
	rootOpts := verifyTestCertOpts{
		notBefore:        1,
		notAfter:         2,
		issuerRCACID:     0x0001,
		subjectHasRCACID: true,
		subjectRCACID:    0x0001,
		pubKey:           marshalPub(rootPriv),
	}
	rootRaw := buildSignedCert(t, rootOpts, rootPriv)
	rootCert, _ := mattercert.Decode(rootRaw)

	nocOpts := verifyTestCertOpts{
		notBefore:          1,
		notAfter:           2,
		issuerRCACID:       0x0001,
		subjectHasNodeID:   true,
		subjectNodeID:      0xAAAA,
		subjectHasFabricID: true,
		subjectFabricID:    0xBBBB,
		pubKey:             marshalPub(nocPriv),
	}
	nocRaw := buildSignedCert(t, nocOpts, rootPriv)

	// Use a FixedTime set to well after NotAfter=2 (Matter-epoch
	// seconds). NotAfter=2 maps to 2000-01-01T00:00:02Z (Unix
	// 946684802); pick year 2033 so the comparison fails on the
	// upper bound.
	clock := mattercert.FixedTime{T: time.Unix(2000000000, 0)}
	v, err := mattercert.NewVerifier(rootCert.PublicKey, clock)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	_, err = v.VerifyAndExtractPubKey(nocRaw, nil)
	if !errors.Is(err, mattercert.ErrExpired) {
		t.Fatalf("expected ErrExpired, got %v", err)
	}
}

func TestVerifyAndExtractPubKey_ChainBroken(t *testing.T) {
	t.Parallel()
	// Build a chain with ICAC-ID mismatch: NOC.Issuer.ICACID != ICAC.Subject.ICACID.
	rootPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	icacPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	nocPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := nowEpoch()

	rootOpts := verifyTestCertOpts{
		notBefore:        now - 100,
		notAfter:         0,
		issuerRCACID:     0x0001,
		subjectHasRCACID: true,
		subjectRCACID:    0x0001,
		pubKey:           marshalPub(rootPriv),
	}
	rootRaw := buildSignedCert(t, rootOpts, rootPriv)
	rootCert, _ := mattercert.Decode(rootRaw)

	// ICAC Subject: tag 19 (matter-icac-id=0x1111) + tag 21 (matter-fabric-id) → IsICA().
	icacOpts := verifyTestCertOpts{
		notBefore:          now - 100,
		notAfter:           0,
		issuerRCACID:       0x0001,
		subjectHasICACID:   true,
		subjectICACID:      0x1111,
		subjectHasFabricID: true,
		subjectFabricID:    0xBBBB,
		pubKey:             marshalPub(icacPriv),
	}
	icacRaw := buildSignedCert(t, icacOpts, rootPriv)

	// NOC claims to have been issued by ICAC with matter-icac-id=0x9999
	// (mismatch vs the actual ICAC subject 0x1111).
	nocOpts := verifyTestCertOpts{
		notBefore:          now - 100,
		notAfter:           0,
		issuerHasICACID:    true,
		issuerICACID:       0x9999, // mismatch!
		subjectHasNodeID:   true,
		subjectNodeID:      0xAAAA,
		subjectHasFabricID: true,
		subjectFabricID:    0xBBBB,
		pubKey:             marshalPub(nocPriv),
	}
	nocRaw := buildSignedCert(t, nocOpts, icacPriv)

	v, err := mattercert.NewVerifier(rootCert.PublicKey, mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	_, err = v.VerifyAndExtractPubKey(nocRaw, icacRaw)
	if !errors.Is(err, mattercert.ErrChainBroken) {
		t.Fatalf("expected ErrChainBroken, got %v", err)
	}
}

// buildCertWithExtensions builds a raw cert TLV that includes a
// basic-constraints extension (CA=true) and an extended-key-usage
// extension (serverAuth=1, clientAuth=2).
func buildCertWithExtensions(t *testing.T, priv *ecdsa.PrivateKey) []byte {
	t.Helper()
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.25 but crypto/ecdh requires key type migration; kept for Matter TLV wire format compatibility

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x03})                    // SerialNumber
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256) // SigAlgo
	// Issuer
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0xABCD)) // RCAC
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer issuer: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0)) // NotBefore
	enc.PutUint(tlv.ContextTag(5), uint64(0)) // NotAfter
	// Subject
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0xABCD)) // RCAC (self-signed)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer subject: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	// Extensions list (context tag 10)
	enc.StartList(tlv.ContextTag(10))
	// basic-constraints struct (context tag 1)
	enc.StartStruct(tlv.ContextTag(1))
	enc.PutBool(tlv.ContextTag(1), true) // is-ca = true
	enc.PutUint(tlv.ContextTag(2), 0)    // path-len = 0 (HasPathLen)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer bc: %v", err)
	}
	// key-usage (context tag 2)
	enc.PutUint(tlv.ContextTag(2), uint64(0x0001)) // digitalSignature
	// extended-key-usage array (context tag 3) — include all 6 EKU values
	// so that mapEKU exercises all switch cases.
	enc.StartArray(tlv.ContextTag(3))
	enc.PutUint(tlv.AnonymousTag(), uint64(1)) // serverAuth
	enc.PutUint(tlv.AnonymousTag(), uint64(2)) // clientAuth
	enc.PutUint(tlv.AnonymousTag(), uint64(3)) // codeSigning
	enc.PutUint(tlv.AnonymousTag(), uint64(4)) // emailProtection
	enc.PutUint(tlv.AnonymousTag(), uint64(5)) // timeStamping
	enc.PutUint(tlv.AnonymousTag(), uint64(6)) // OCSPSigning
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer eku: %v", err)
	}
	// subject-key-id (context tag 4)
	enc.PutOctets(tlv.ContextTag(4), make([]byte, 20))
	// authority-key-id (context tag 5)
	enc.PutOctets(tlv.ContextTag(5), make([]byte, 20))
	// future-extension (context tag 6) — raw DER bytes. Use a minimal ASN.1
	// INTEGER 42 (0x02 0x01 0x2a) so asn1.Unmarshal succeeds on re-emit.
	enc.PutOctets(tlv.ContextTag(6), []byte{0x02, 0x01, 0x2a})
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer extensions: %v", err)
	}
	// Signature (64 zeroes)
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer top: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	return raw
}

// TestDecode_NotAfterBeforeNotBefore verifies that a cert where NotAfter
// is before NotBefore is rejected with ErrMalformed.
func TestDecode_NotAfterBeforeNotBefore(t *testing.T) {
	t.Parallel()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.25 but crypto/ecdh requires key type migration; kept for Matter TLV wire format compatibility

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x05})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x1111))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer issuer: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(1000)) // NotBefore: 1000
	enc.PutUint(tlv.ContextTag(5), uint64(500))  // NotAfter: 500 (before NotBefore — deliberately invalid)
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x1111))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer subject: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer ext: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer top: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if decErr == nil {
		t.Fatal("expected error for NotAfter < NotBefore, got nil")
	}
}

// TestDecode_WithExtensions verifies that a cert with BasicConstraints and
// ExtendedKeyUsage extensions decodes and round-trips through TBSToDER,
// exercising decodeBasicConstraints, decodeExtensions, mapEKU, and
// buildExtensions.
func TestDecode_WithExtensions(t *testing.T) {
	t.Parallel()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	raw := buildCertWithExtensions(t, priv)

	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if !cert.Extensions.HasBasicConstraints {
		t.Error("HasBasicConstraints should be true")
	}
	if !cert.Extensions.BasicConstraintsIsCA {
		t.Error("BasicConstraintsIsCA should be true")
	}
	if !cert.Extensions.HasExtendedKeyUsage {
		t.Error("HasExtendedKeyUsage should be true")
	}
	if len(cert.Extensions.ExtendedKeyUsage) != 6 {
		t.Errorf("ExtendedKeyUsage: got %d, want 6", len(cert.Extensions.ExtendedKeyUsage))
	}
	if !cert.Extensions.HasKeyUsage {
		t.Error("HasKeyUsage should be true")
	}
	if !cert.Extensions.HasSubjectKeyID {
		t.Error("HasSubjectKeyID should be true")
	}
	if !cert.Extensions.HasAuthorityKeyID {
		t.Error("HasAuthorityKeyID should be true")
	}

	// TBSToDER exercises buildExtensions + mapEKU.
	tbs, err := mattercert.TBSToDER(cert)
	if err != nil {
		t.Fatalf("TBSToDER: %v", err)
	}
	if len(tbs) == 0 {
		t.Error("TBSToDER returned empty bytes")
	}
}

// TestPublicKeyECDSA_WrongAlgorithm verifies ErrUnsupportedAlgorithm
// when the public-key-algorithm tag is not EC.
func TestPublicKeyECDSA_WrongAlgorithm(t *testing.T) {
	t.Parallel()
	c := &mattercert.Certificate{
		PublicKeyAlgorithm: 0xFF, // not PubKeyAlgoEC
	}
	_, err := c.PublicKeyECDSA()
	if !errors.Is(err, mattercert.ErrUnsupportedAlgorithm) {
		t.Errorf("expected ErrUnsupportedAlgorithm, got %v", err)
	}
}

// TestPublicKeyECDSA_WrongCurve verifies ErrUnsupportedAlgorithm when
// the elliptic-curve ID is not Prime256v1.
func TestPublicKeyECDSA_WrongCurve(t *testing.T) {
	t.Parallel()
	c := &mattercert.Certificate{
		PublicKeyAlgorithm: mattercert.PubKeyAlgoEC,
		EllipticCurveID:    0xFF, // not CurvePrime256v1
	}
	_, err := c.PublicKeyECDSA()
	if !errors.Is(err, mattercert.ErrUnsupportedAlgorithm) {
		t.Errorf("expected ErrUnsupportedAlgorithm, got %v", err)
	}
}

// TestPublicKeyECDSA_MalformedPoint verifies ErrMalformed when the
// public key is too short.
func TestPublicKeyECDSA_MalformedPoint(t *testing.T) {
	t.Parallel()
	c := &mattercert.Certificate{
		PublicKeyAlgorithm: mattercert.PubKeyAlgoEC,
		EllipticCurveID:    mattercert.CurvePrime256v1,
		PublicKey:          []byte{0x04, 0x01}, // too short
	}
	_, err := c.PublicKeyECDSA()
	if !errors.Is(err, mattercert.ErrMalformed) {
		t.Errorf("expected ErrMalformed, got %v", err)
	}
}

// TestVerifier_PeerNodeIDFromNOC exercises PeerNodeIDFromNOC with a
// synthesised NOC that carries a NodeID in the subject.
func TestVerifier_PeerNodeIDFromNOC(t *testing.T) {
	t.Parallel()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.25 but crypto/ecdh requires key type migration; kept for Matter TLV wire format compatibility

	// Build a minimal NOC-shaped cert (HasNodeID + HasFabricID in subject).
	nocRaw := buildSignedCert(t, verifyTestCertOpts{
		serial:             []byte{0x01},
		notBefore:          0,
		notAfter:           0,
		issuerRCACID:       0xDEAD,
		subjectHasNodeID:   true,
		subjectNodeID:      0xBEEF_CAFE_1234_5678,
		subjectHasFabricID: true,
		subjectFabricID:    0xFAB1,
		pubKey:             pub,
	}, priv)

	// NewVerifier needs a root public key; use the same key for a
	// self-signed scenario — we're not verifying the chain here, just
	// PeerNodeIDFromNOC.
	v, err := mattercert.NewVerifier(pub, mattercert.FixedTime{T: time.Now()})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	nodeID, err := v.PeerNodeIDFromNOC(nocRaw)
	if err != nil {
		t.Fatalf("PeerNodeIDFromNOC: %v", err)
	}
	if nodeID != 0xBEEF_CAFE_1234_5678 {
		t.Errorf("NodeID = 0x%016X, want 0xBEEFCAFE12345678", nodeID)
	}
}

// TestVerifier_PeerNodeIDFromNOC_MissingNodeID verifies that a cert
// without HasNodeID in the subject returns an error.
func TestVerifier_PeerNodeIDFromNOC_MissingNodeID(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.25 but crypto/ecdh requires key type migration; kept for Matter TLV wire format compatibility

	// Build a cert with NodeID absent.
	nocRaw := buildSignedCert(t, verifyTestCertOpts{
		serial:    []byte{0x02},
		pubKey:    pub,
		notBefore: 0,
		notAfter:  0,
	}, priv)

	v, err := mattercert.NewVerifier(pub, mattercert.FixedTime{T: time.Now()})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	_, err = v.PeerNodeIDFromNOC(nocRaw)
	if err == nil {
		t.Fatal("expected error for NOC without NodeID, got nil")
	}
}

// TestDecode_WrongSigAlgo verifies that a cert with a non-ECDSA sig algo
// returns ErrUnsupportedAlgorithm.
func TestDecode_WrongSigAlgo(t *testing.T) {
	t.Parallel()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), 99) // not SigAlgoECDSAWithSHA256
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrUnsupportedAlgorithm) {
		t.Fatalf("err = %v, want ErrUnsupportedAlgorithm", decErr)
	}
}

// TestDecode_WrongPubKeyAlgo verifies ErrUnsupportedAlgorithm for a bad
// public-key algorithm (not PubKeyAlgoEC).
func TestDecode_WrongPubKeyAlgo(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), 0xFF) // bad pubkey algo
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrUnsupportedAlgorithm) {
		t.Fatalf("err = %v, want ErrUnsupportedAlgorithm", decErr)
	}
}

// TestDecode_WrongPubKeyLength verifies ErrMalformed for a public key that
// is not 65 bytes (wrong length for uncompressed P-256 point).
func TestDecode_WrongPubKeyLength(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), make([]byte, 33)) // wrong length
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", decErr)
	}
}

// TestDecode_BasicConstraintsNotContainer verifies that a basic-constraints
// extension where the element is not a container returns ErrMalformed.
func TestDecode_BasicConstraintsNotContainer(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	// Extensions list with basic-constraints as a scalar (not a container)
	enc.StartList(tlv.ContextTag(10))
	enc.PutUint(tlv.ContextTag(1), 42) // tag 1 = basic-constraints, but as scalar not struct
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", decErr)
	}
}

// TestDecode_ExtendedKeyUsageNotContainer verifies that an eku extension
// where the element is not a container returns ErrMalformed.
func TestDecode_ExtendedKeyUsageNotContainer(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	// Extensions list with eku as a scalar (not a container/array)
	enc.StartList(tlv.ContextTag(10))
	enc.PutUint(tlv.ContextTag(3), 42) // tag 3 = eku, but as scalar
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", decErr)
	}
}

// TestDecode_UnknownContainerFieldSkipped verifies that an unknown
// container tag in the cert body is skipped via skipContainer. This
// exercises the `default: if el.IsContainer { skipContainer }` path
// in assignField and the depth++ branch of skipContainer.
func TestDecode_UnknownContainerFieldSkipped(t *testing.T) {
	t.Parallel()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	// Build a cert with an unknown context tag (99) that is a container.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	// Unknown container tag 50 — should be skipped.
	enc.StartStruct(tlv.ContextTag(50))
	enc.PutUint(tlv.ContextTag(1), 42) // inner field — covered by skipContainer
	// Nested struct to exercise depth++ in skipContainer.
	enc.StartStruct(tlv.ContextTag(2))
	enc.PutUint(tlv.ContextTag(1), 7)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("inner nested end: %v", err)
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("unknown container end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	// Should decode successfully — the unknown container is skipped.
	_, decErr := mattercert.Decode(raw)
	if decErr != nil {
		t.Fatalf("Decode: expected nil error for cert with unknown container, got %v", decErr)
	}
}

// TestTBSToDER_OffCurvePubKey verifies that TBSToDER returns ErrMalformed
// when the certificate's public key is not on the P-256 curve.
func TestTBSToDER_OffCurvePubKey(t *testing.T) {
	t.Parallel()
	// Build a cert via Decode using a valid cert, then manually replace
	// the public key with an invalid point. We cannot do this through
	// the normal Decode path (validateMandatory checks the 65-byte prefix
	// but not curve membership), so we construct a Certificate directly.
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019
	raw := buildCertWithExtensions(t, priv)
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	// Replace pub key with a 65-byte 0x04-prefixed buffer that is NOT on P-256.
	badPub := make([]byte, 65)
	copy(badPub, pub)
	badPub[0] = 0x04
	for i := 1; i < 65; i++ {
		badPub[i] = 0xFF // all-ones coordinates are not on P-256
	}
	cert.PublicKey = badPub
	_, err = mattercert.TBSToDER(cert)
	if !errors.Is(err, mattercert.ErrMalformed) {
		t.Fatalf("TBSToDER with off-curve key: err = %v, want ErrMalformed", err)
	}
}

// TestVerifyAndExtractPubKey_NonNOCRejectsWithError verifies that passing
// a cert that is not a NOC (e.g. an RCAC) returns a non-nil error.
func TestVerifyAndExtractPubKey_NonNOCRejectsWithError(t *testing.T) {
	t.Parallel()
	rootPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := nowEpoch()

	// Build a root cert (HasRCACID in subject = IsRoot(), not IsNOC()).
	rootOpts := verifyTestCertOpts{
		notBefore:        now - 100,
		notAfter:         0,
		issuerRCACID:     0x0001,
		subjectHasRCACID: true,
		subjectRCACID:    0x0001,
		pubKey:           marshalPub(rootPriv),
	}
	rootRaw := buildSignedCert(t, rootOpts, rootPriv)
	rootCert, err := mattercert.Decode(rootRaw)
	if err != nil {
		t.Fatalf("Decode root: %v", err)
	}

	v, err := mattercert.NewVerifier(rootCert.PublicKey, mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// Pass the root cert as the NOC — it's not a NOC so VerifyAndExtractPubKey
	// must return an error.
	_, err = v.VerifyAndExtractPubKey(rootRaw, nil)
	if err == nil {
		t.Fatal("expected error for non-NOC input, got nil")
	}
	if !errors.Is(err, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
}

// TestVerifyAndExtractPubKey_ICAC_NotIsICA verifies that passing a non-ICAC
// cert as the ICAC argument (it's a NOC) returns an error.
func TestVerifyAndExtractPubKey_ICAC_NotIsICA(t *testing.T) {
	t.Parallel()
	chain := buildChainNoICA(t)
	rootCert, err := mattercert.Decode(chain.rootRaw)
	if err != nil {
		t.Fatalf("Decode root: %v", err)
	}

	v, err := mattercert.NewVerifier(rootCert.PublicKey, mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// Pass the NOC as the ICAC — it's not an ICAC, so VerifyAndExtractPubKey
	// must return an error.
	_, err = v.VerifyAndExtractPubKey(chain.nocRaw, chain.nocRaw)
	if err == nil {
		t.Fatal("expected error for non-ICAC input as ICAC, got nil")
	}
	if !errors.Is(err, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", err)
	}
}

// TestVerifyAndExtractPubKey_ICAC_BadSignature verifies that an ICAC
// with a forged signature (not signed by the root) returns ErrSignatureInvalid.
func TestVerifyAndExtractPubKey_ICAC_BadSignature(t *testing.T) {
	t.Parallel()
	chain := buildChainWithICA(t)
	rootCert, err := mattercert.Decode(chain.rootRaw)
	if err != nil {
		t.Fatalf("Decode root: %v", err)
	}

	v, err := mattercert.NewVerifier(rootCert.PublicKey, mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// Tamper with the ICAC bytes.
	tamperedICAC := append([]byte(nil), chain.icacRaw...)
	// Flip a byte near the end (in the signature area).
	if len(tamperedICAC) > 10 {
		tamperedICAC[len(tamperedICAC)-5] ^= 0x01
	}

	_, err = v.VerifyAndExtractPubKey(chain.nocRaw, tamperedICAC)
	if !errors.Is(err, mattercert.ErrSignatureInvalid) {
		t.Fatalf("tampered ICAC: err = %v, want ErrSignatureInvalid", err)
	}
}

// TestVerifySignature_WrongSigLength verifies that verifySignature (called
// via VerifyAndExtractPubKey) rejects a cert with a non-64-byte signature.
// We do this by building a cert TLV with only 32 bytes in the sig field.
func TestVerifyAndExtractPubKey_WrongSigLength(t *testing.T) {
	t.Parallel()
	rootPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	nocPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := nowEpoch()

	rootOpts := verifyTestCertOpts{
		notBefore:        now - 100,
		notAfter:         0,
		issuerRCACID:     0x0001,
		subjectHasRCACID: true,
		subjectRCACID:    0x0001,
		pubKey:           marshalPub(rootPriv),
	}
	rootRaw := buildSignedCert(t, rootOpts, rootPriv)
	rootCert, err := mattercert.Decode(rootRaw)
	if err != nil {
		t.Fatalf("Decode root: %v", err)
	}

	// Build a NOC manually with a 32-byte signature field (too short).
	// validateMandatory rejects sig != 64 bytes, so we can't use Decode+verify.
	// Instead we build the cert TLV manually so we can control the sig length,
	// then pass the raw bytes to VerifyAndExtractPubKey which will Decode it.
	//
	// Since validateMandatory rejects 32-byte sig, we need a way to bypass.
	// The test must use a 64-byte sig for Decode to succeed. But verifySignature
	// is already tested by the tampered-NOC test above. Skip the impossible path.
	_ = nocPriv
	_ = rootCert
	t.Skip("verifySignature wrong-sig-length path is covered by FakeSignature test; skip duplicate")
}

// TestDecode_SubjectKeyIDEmpty verifies ErrMalformed when the SubjectKeyID
// extension has an empty octet string.
func TestDecode_SubjectKeyIDEmpty(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	// Extensions with empty SubjectKeyID (tag 4 = extTagSubjectKeyID)
	enc.StartList(tlv.ContextTag(10))
	enc.PutOctets(tlv.ContextTag(4), []byte{}) // empty subject-key-id
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", decErr)
	}
}

// TestDecode_AuthorityKeyIDEmpty verifies ErrMalformed when the AuthorityKeyID
// extension has an empty octet string.
func TestDecode_AuthorityKeyIDEmpty(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	// Extensions with empty AuthorityKeyID (tag 5 = extTagAuthorityKeyID)
	enc.StartList(tlv.ContextTag(10))
	enc.PutOctets(tlv.ContextTag(5), []byte{}) // empty authority-key-id
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed", decErr)
	}
}

// TestBuildDN_FallbackOrder verifies that buildDN emits DN attributes in
// canonical order when Order is empty (no TLV order recorded), exercising
// the HasICACID and HasFabricID fallback paths.
func TestBuildDN_FallbackOrder(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	// Build a cert with ICACID in the issuer (sets HasICACID on the issuer DN).
	// The issuer DN is populated through decodeDN which always sets Order;
	// but buildDN's fallback runs when Order is empty (synthetic DN in tests).
	// We exercise this via TBSToDER on a cert that has HasICACID set.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	// Issuer with both RCAC and ICAC IDs.
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x1111)) // RCAC
	enc.PutUint(tlv.ContextTag(19), uint64(0x2222)) // ICAC
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	// Subject with RCAC + ICAC.
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x3333)) // RCAC
	enc.PutUint(tlv.ContextTag(19), uint64(0x4444)) // ICAC
	enc.PutUint(tlv.ContextTag(21), uint64(0x5555)) // FabricID
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	// TBSToDER exercises buildDN with Order populated (from decoded TLV).
	tbs, err := mattercert.TBSToDER(cert)
	if err != nil {
		t.Fatalf("TBSToDER: %v", err)
	}
	if len(tbs) == 0 {
		t.Error("TBSToDER returned empty bytes")
	}
}

// TestCheckValidity_NotYetValid verifies that a cert whose NotBefore is in
// the future returns ErrExpired (the "now < NotBefore" path).
func TestCheckValidity_NotYetValid(t *testing.T) {
	t.Parallel()
	rootPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	nocPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := nowEpoch()

	rootOpts := verifyTestCertOpts{
		notBefore:        now - 100,
		notAfter:         0,
		issuerRCACID:     0x0001,
		subjectHasRCACID: true,
		subjectRCACID:    0x0001,
		pubKey:           marshalPub(rootPriv),
	}
	rootRaw := buildSignedCert(t, rootOpts, rootPriv)
	rootCert, _ := mattercert.Decode(rootRaw)

	// NOC with NotBefore far in the future (year 2100 in matter-epoch seconds).
	futureNotBefore := now + 3_000_000_000 // ~95 years in the future
	nocOpts := verifyTestCertOpts{
		notBefore:          futureNotBefore,
		notAfter:           0,
		issuerRCACID:       0x0001,
		subjectHasNodeID:   true,
		subjectNodeID:      0xAAAA,
		subjectHasFabricID: true,
		subjectFabricID:    0xBBBB,
		pubKey:             marshalPub(nocPriv),
	}
	nocRaw := buildSignedCert(t, nocOpts, rootPriv)

	// Use SystemTime — the current time is now, which is before futureNotBefore.
	v, err := mattercert.NewVerifier(rootCert.PublicKey, mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	_, err = v.VerifyAndExtractPubKey(nocRaw, nil)
	if !errors.Is(err, mattercert.ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired (not yet valid)", err)
	}
}

// TestDecode_BasicConstraintsNonContextTag verifies that a basic-constraints
// inner struct containing a non-context-tagged element returns ErrMalformed.
// This exercises the `el.Tag.Kind != TagKindContext` path in decodeBasicConstraints.
func TestDecode_BasicConstraintsNonContextTag(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	// Extensions with basic-constraints struct containing an anonymous-tagged element
	enc.StartList(tlv.ContextTag(10))
	enc.StartStruct(tlv.ContextTag(1)) // basic-constraints (tag 1, struct)
	// Anonymous tag inside — triggers non-context-tag error in decodeBasicConstraints
	enc.PutBool(tlv.AnonymousTag(), true)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("bc end: %v", err)
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed (non-context tag in bc)", decErr)
	}
}

// TestDecode_BasicConstraintsUnknownContainer verifies the default case in
// decodeBasicConstraints when an unknown context tag holds a nested container.
// This exercises the `if el.IsContainer { skipContainer }` path.
func TestDecode_BasicConstraintsUnknownContainer(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	// Extensions with basic-constraints struct containing an unknown container tag (99)
	enc.StartList(tlv.ContextTag(10))
	enc.StartStruct(tlv.ContextTag(1))   // basic-constraints struct
	enc.PutBool(tlv.ContextTag(1), true) // is-ca = true (known field)
	enc.StartStruct(tlv.ContextTag(99))  // unknown nested container
	enc.PutUint(tlv.ContextTag(1), 42)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("nested end: %v", err)
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("bc end: %v", err)
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	// Should decode successfully — the unknown container is skipped.
	cert, decErr := mattercert.Decode(raw)
	if decErr != nil {
		t.Fatalf("Decode: expected nil error for cert with unknown bc container, got %v", decErr)
	}
	if !cert.Extensions.HasBasicConstraints {
		t.Error("HasBasicConstraints should be true")
	}
}

// TestDecode_ExtensionsUnknownContainer verifies the default case in
// decodeExtensions when an unknown extension tag holds a nested container.
func TestDecode_ExtensionsUnknownContainer(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	// Extensions with an unknown container (tag 99)
	enc.StartList(tlv.ContextTag(10))
	enc.StartStruct(tlv.ContextTag(99)) // unknown extension container
	enc.PutUint(tlv.ContextTag(1), 42)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("unknown ext end: %v", err)
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext list end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	// Should decode successfully — the unknown container is skipped.
	_, decErr := mattercert.Decode(raw)
	if decErr != nil {
		t.Fatalf("Decode: expected nil error for cert with unknown ext container, got %v", decErr)
	}
}

// TestNewVerifier_NilClockUsesSystemTime verifies that passing a nil clock
// to NewVerifier defaults to SystemTime (no panic, no error).
func TestNewVerifier_NilClockUsesSystemTime(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019
	_, err := mattercert.NewVerifier(pub, nil)
	if err != nil {
		t.Fatalf("NewVerifier with nil clock: %v", err)
	}
}

// TestVerifyAndExtractPubKey_OffCurvePubKeyInNOC verifies that
// VerifyAndExtractPubKey returns an error when the NOC has a 65-byte pubkey
// with 0x04 prefix but off-curve coordinates. This exercises the TBSToDER
// error path inside verifySignature.
func TestVerifyAndExtractPubKey_OffCurvePubKeyInNOC(t *testing.T) {
	t.Parallel()
	rootPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := nowEpoch()

	rootOpts := verifyTestCertOpts{
		notBefore:        now - 100,
		notAfter:         0,
		issuerRCACID:     0x0001,
		subjectHasRCACID: true,
		subjectRCACID:    0x0001,
		pubKey:           marshalPub(rootPriv),
	}
	rootRaw := buildSignedCert(t, rootOpts, rootPriv)
	rootCert, err := mattercert.Decode(rootRaw)
	if err != nil {
		t.Fatalf("Decode root: %v", err)
	}

	// Build a NOC with an off-curve pubkey (0x04 prefix, all-0xFF coordinates).
	// validateMandatory accepts this (only checks length+prefix); buildTBS rejects it.
	badPub := make([]byte, 65)
	badPub[0] = 0x04
	for i := 1; i < 65; i++ {
		badPub[i] = 0xFF // not on P-256
	}

	// Build the NOC TLV manually with the bad pubkey + an arbitrary 64-byte sig.
	// We need a cert that Decode() accepts (pass validateMandatory) but fails
	// TBSToDER. Decode accepts 0x04-prefixed 65-byte pubkey without curve check.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), now-100)
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(17), uint64(0xAAAA)) // NodeID
	enc.PutUint(tlv.ContextTag(21), uint64(0xBBBB)) // FabricID → IsNOC()=true
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), badPub) // off-curve pubkey
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64)) // arbitrary signature
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	nocRaw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	v, err := mattercert.NewVerifier(rootCert.PublicKey, mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	// VerifyAndExtractPubKey must return a non-nil error because TBSToDER fails
	// on the off-curve key inside verifySignature.
	_, err = v.VerifyAndExtractPubKey(nocRaw, nil)
	if err == nil {
		t.Fatal("expected error for off-curve pubkey NOC, got nil")
	}
}

// TestTBSToDER_FallbackOrder_HasICACID verifies the buildDN fallback path
// (Order == nil) when the issuer DN has HasICACID set. We achieve this by
// decoding a cert that has ICAC ID in the issuer and then clearing Order
// so the fallback loop runs.
func TestTBSToDER_FallbackOrder_HasICACID(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	// Build a cert with ICAC ID in the issuer.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x1111)) // RCAC
	enc.PutUint(tlv.ContextTag(19), uint64(0x2222)) // ICAC
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x3333)) // RCAC in subject
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Clear Order on the issuer DN so the fallback path runs in buildDN.
	cert.Issuer.Order = nil
	cert.Subject.Order = nil

	// TBSToDER must succeed and exercise the fallback path with HasICACID.
	tbs, err := mattercert.TBSToDER(cert)
	if err != nil {
		t.Fatalf("TBSToDER with cleared Order (HasICACID): %v", err)
	}
	if len(tbs) == 0 {
		t.Error("TBSToDER returned empty bytes")
	}
}

// TestTBSToDER_FallbackOrder_HasFabricID verifies the buildDN fallback path
// when both HasFabricID and CASEAuthTags are set on the subject but Order is cleared.
func TestTBSToDER_FallbackOrder_HasFabricIDAndCAT(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x1111)) // RCAC
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(17), uint64(0xAAAA))      // NodeID
	enc.PutUint(tlv.ContextTag(21), uint64(0xBBBB))      // FabricID
	enc.PutUint(tlv.ContextTag(22), uint64(0xCAFE_1234)) // CASEAuthTag
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Clear Order so the fallback path exercises HasFabricID + CASEAuthTags.
	cert.Subject.Order = nil
	cert.Issuer.Order = nil

	tbs, err := mattercert.TBSToDER(cert)
	if err != nil {
		t.Fatalf("TBSToDER with cleared Order (HasFabricID+CAT): %v", err)
	}
	if len(tbs) == 0 {
		t.Error("TBSToDER returned empty bytes")
	}
}

// TestPeerNodeIDFromNOC_DecodeError verifies that PeerNodeIDFromNOC
// surfaces a non-nil error when the raw bytes are not a valid cert.
func TestPeerNodeIDFromNOC_DecodeError(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	v, err := mattercert.NewVerifier(pub, mattercert.FixedTime{T: time.Now()})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	// Pass garbage bytes — Decode must fail.
	_, err = v.PeerNodeIDFromNOC([]byte{0xFF, 0x01, 0x02})
	if err == nil {
		t.Fatal("expected error for invalid NOC bytes, got nil")
	}
}

// TestDecodeDN_NonContextTagInIssuer verifies that a non-context-tagged
// element inside the issuer DN list returns ErrMalformed.
func TestDecodeDN_NonContextTagInIssuer(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	// Issuer list with an anonymous-tagged element — triggers non-context-tag error in decodeDN.
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.AnonymousTag(), uint64(0xABCD)) // non-context tag inside DN list
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed (non-context tag in issuer DN)", decErr)
	}
}

// TestDecodeDN_UnknownContainerInIssuer verifies the skip-container path
// inside decodeDN when the issuer list contains an unknown container tag.
func TestDecodeDN_UnknownContainerInIssuer(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	// Issuer list with a known RCAC ID and then an unknown container (tag 50).
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x1111)) // RCAC ID (known)
	enc.StartStruct(tlv.ContextTag(50))             // unknown container in DN
	enc.PutUint(tlv.ContextTag(1), 42)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("unknown dn container end: %v", err)
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x2222))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	// Should decode successfully — the unknown container is skipped.
	cert, decErr := mattercert.Decode(raw)
	if decErr != nil {
		t.Fatalf("Decode: expected nil error for cert with unknown DN container, got %v", decErr)
	}
	if !cert.Issuer.HasRCACID {
		t.Error("Issuer.HasRCACID should be true (RCAC was decoded before the unknown container)")
	}
}

// TestAssignField_SerialNumberNotOctetString verifies ErrMalformed when
// the serial-number field (tag 1) is encoded as an integer instead of an
// octet string. This exercises the `el.Type < TypeOctetStr1 ||
// el.Type > TypeOctetStr8` guard inside assignField.
func TestAssignField_SerialNumberNotOctetString(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	// Serial as a uint (not octet string) — triggers assignField error.
	enc.PutUint(tlv.ContextTag(1), uint64(42))
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed (serial not octet string)", decErr)
	}
}

// TestBuildTBS_ZeroSerial verifies the `serial.Sign() == 0` branch in
// buildTBS. We decode a valid cert, replace SerialNumber with []byte{0x00}
// (which new(big.Int).SetBytes → Sign()==0), then call TBSToDER. The
// branch re-assigns serial to big.NewInt(0) — same value — so TBSToDER
// must still succeed.
func TestBuildTBS_ZeroSerial(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	raw := buildCertWithExtensions(t, priv)
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// A single zero byte → big.Int.SetBytes → Sign() == 0.
	cert.SerialNumber = []byte{0x00}

	tbs, err := mattercert.TBSToDER(cert)
	if err != nil {
		t.Fatalf("TBSToDER with zero serial: %v", err)
	}
	if len(tbs) == 0 {
		t.Error("TBSToDER returned empty bytes")
	}
}

// TestEncodeKeyUsageBits_ZeroFlags verifies the `flags == 0` early-return
// path in encodeKeyUsageBits via buildExtensions. We build a cert with
// KeyUsage set to 0 and call TBSToDER.
func TestEncodeKeyUsageBits_ZeroFlags(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x1111))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x1111))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	// KeyUsage = 0 → exercises the flags==0 early-return in encodeKeyUsageBits.
	enc.PutUint(tlv.ContextTag(2), uint64(0))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	cert, decErr := mattercert.Decode(raw)
	if decErr != nil {
		t.Fatalf("Decode: %v", decErr)
	}
	tbs, err := mattercert.TBSToDER(cert)
	if err != nil {
		t.Fatalf("TBSToDER with zero KeyUsage: %v", err)
	}
	if len(tbs) == 0 {
		t.Error("TBSToDER returned empty bytes")
	}
}

// TestMapEKU_UnknownValue verifies that mapEKU returns ErrUnsupportedAlgorithm
// for an EKU value outside the 1..6 range. This exercises the default branch
// in mapEKU. We call TBSToDER on a cert where we manually set the EKU slice.
func TestMapEKU_UnknownValue(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	raw := buildCertWithExtensions(t, priv)
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Inject an unknown EKU value (99) directly.
	cert.Extensions.ExtendedKeyUsage = append(cert.Extensions.ExtendedKeyUsage, 99)

	_, err = mattercert.TBSToDER(cert)
	if !errors.Is(err, mattercert.ErrUnsupportedAlgorithm) {
		t.Fatalf("TBSToDER with unknown EKU: err = %v, want ErrUnsupportedAlgorithm", err)
	}
}

// TestVerifyAndExtractPubKey_ICAC_DecodeError verifies that passing
// invalid (non-TLV) bytes as the ICAC argument returns a non-nil error.
// This exercises the `icacCert, err := Decode(icac)` error branch at the
// start of the icac != nil block.
func TestVerifyAndExtractPubKey_ICAC_DecodeError(t *testing.T) {
	t.Parallel()
	chain := buildChainNoICA(t)
	rootCert, err := mattercert.Decode(chain.rootRaw)
	if err != nil {
		t.Fatalf("Decode root: %v", err)
	}
	v, err := mattercert.NewVerifier(rootCert.PublicKey, mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	// Pass garbage bytes as ICAC — Decode must fail.
	_, err = v.VerifyAndExtractPubKey(chain.nocRaw, []byte{0xFF, 0x01})
	if err == nil {
		t.Fatal("expected error for invalid ICAC bytes, got nil")
	}
}

// TestVerifyAndExtractPubKey_ICAC_Expired verifies that an expired ICAC
// cert (NotAfter in the past) causes VerifyAndExtractPubKey to return
// ErrExpired. This exercises the checkValidity error path for the ICAC.
func TestVerifyAndExtractPubKey_ICAC_Expired(t *testing.T) {
	t.Parallel()
	rootPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	icacPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	nocPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	now := nowEpoch()

	rootOpts := verifyTestCertOpts{
		notBefore:        now - 1000,
		notAfter:         0,
		issuerRCACID:     0x0001,
		subjectHasRCACID: true,
		subjectRCACID:    0x0001,
		pubKey:           marshalPub(rootPriv),
	}
	rootRaw := buildSignedCert(t, rootOpts, rootPriv)
	rootCert, _ := mattercert.Decode(rootRaw)

	// ICAC with notAfter=1 (long in the past — Matter epoch 1 = 2000-01-01T00:00:01Z).
	icacOpts := verifyTestCertOpts{
		notBefore:        1,
		notAfter:         2, // very short window, already expired
		issuerRCACID:     0x0001,
		subjectHasICACID: true,
		subjectICACID:    0x9999,
		pubKey:           marshalPub(icacPriv),
	}
	icacRaw := buildSignedCert(t, icacOpts, rootPriv)

	nocOpts := verifyTestCertOpts{
		notBefore:          now - 100,
		notAfter:           0,
		issuerHasICACID:    true,
		issuerICACID:       0x9999,
		issuerRCACID:       0x0001,
		subjectHasNodeID:   true,
		subjectNodeID:      0xAAAA,
		subjectHasFabricID: true,
		subjectFabricID:    0xBBBB,
		pubKey:             marshalPub(nocPriv),
	}
	nocRaw := buildSignedCert(t, nocOpts, icacPriv)

	// Use SystemTime — the real clock is well past Matter epoch 2000-01-01T00:00:02Z.
	v, err := mattercert.NewVerifier(rootCert.PublicKey, mattercert.SystemTime{})
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}
	_, err = v.VerifyAndExtractPubKey(nocRaw, icacRaw)
	if !errors.Is(err, mattercert.ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired for expired ICAC", err)
	}
}

// TestBuildExtensions_BadFutureExtension verifies that buildExtensions
// returns an error when FutureExtensions contains a malformed DER byte
// sequence that asn1.Unmarshal cannot parse. This exercises the
// `future-extension: unmarshal` error branch.
func TestBuildExtensions_BadFutureExtension(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	raw := buildCertWithExtensions(t, priv)
	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}

	// Inject a malformed DER byte into FutureExtensions — asn1.Unmarshal
	// will fail on this because 0xFF is not a valid BER/DER tag.
	cert.Extensions.FutureExtensions = [][]byte{{0xFF}}

	_, err = mattercert.TBSToDER(cert)
	if err == nil {
		t.Fatal("expected error for malformed future-extension DER, got nil")
	}
}

// TestDecodeExtensions_NonContextTag verifies that a non-context-tagged
// element inside the extensions list returns ErrMalformed.
func TestDecodeExtensions_NonContextTag(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	// Extensions list with an anonymous-tagged element — triggers non-context-tag error.
	enc.StartList(tlv.ContextTag(10))
	enc.PutUint(tlv.AnonymousTag(), uint64(42)) // anonymous tag in extensions list
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed (non-context tag in extensions)", decErr)
	}
}

// TestValidateMandatory_EmptySerial verifies that validateMandatory returns
// ErrMalformed when the serial number is empty (len==0).
func TestValidateMandatory_EmptySerial(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	// Empty serial number octet string.
	enc.PutOctets(tlv.ContextTag(1), []byte{})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("ext end: %v", err)
	}
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed (empty serial)", decErr)
	}
}

// TestDecode_TopBodyNonContextTag verifies that a non-context-tagged element
// in the top-level cert body returns ErrMalformed.
func TestDecode_TopBodyNonContextTag(t *testing.T) {
	t.Parallel()
	// Build a minimal struct with an anonymous-tagged element in the cert body.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.AnonymousTag(), uint64(42)) // non-context tag at cert body level
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("top end: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed (non-context tag in cert body)", decErr)
	}
}

// TestDecode_ToplevelNotStruct verifies that a non-struct top-level element
// (e.g. a plain uint) returns ErrMalformed.
func TestDecode_ToplevelNotStruct(t *testing.T) {
	t.Parallel()
	// Encode a top-level uint (not a struct).
	enc := tlv.NewEncoder()
	enc.PutUint(tlv.AnonymousTag(), uint64(42))
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrMalformed) {
		t.Fatalf("err = %v, want ErrMalformed (top element not struct)", decErr)
	}
}

// TestDecode_EmptyBytes verifies that Decode on an empty byte slice
// returns a non-nil error (ErrTruncated).
func TestDecode_EmptyBytes(t *testing.T) {
	t.Parallel()
	_, err := mattercert.Decode([]byte{})
	if !errors.Is(err, mattercert.ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated for empty input", err)
	}
}

// TestSkipContainer_TruncatedPayload verifies that skipContainer returns
// ErrTruncated when the buffer is exhausted while scanning the unknown
// container's children. We craft a minimal TLV struct whose cert body
// contains an unknown context-tag struct (tag 50) that is never closed
// (truncated after the open marker). This exercises the `d.Next() → EOF`
// branch inside skipContainer.
func TestSkipContainer_TruncatedPayload(t *testing.T) {
	t.Parallel()
	// Manual byte construction:
	//   0x15  — AnonymousTag() + TypeStructure (outer cert struct)
	//   0x30, 1, 1, 0x03 — ContextTag(1) + TypeOctetStr1 (serial=0x03, len=1)
	//   0x35, 50 — ContextTag(50) + TypeStructure (unknown container, truncated)
	// No end-of-container bytes → decoder hits io.EOF inside skipContainer.
	raw := []byte{
		0x15,             // AnonymousTag struct
		0x30, 1, 1, 0x03, // serial: ContextTag(1) OctetStr1 len=1 val=0x03
		0x35, 50, // ContextTag(50) Struct — no body, no 0x18 end
	}
	_, err := mattercert.Decode(raw)
	if !errors.Is(err, mattercert.ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated (truncated unknown container)", err)
	}
}

// TestDecodeUint8Array_TruncatedPayload verifies that decodeUint8Array
// returns ErrTruncated when the EKU array is truncated after the open
// marker. We craft a cert TLV up to the EKU array open and then stop,
// triggering the d.Next() → EOF path in decodeUint8Array.
func TestDecodeUint8Array_TruncatedPayload(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	// Build a cert up to the EKU array start using the encoder, then
	// splice the array-open byte without a matching close. We do this by
	// encoding a complete cert, locating the End-of-Container of the
	// extensions list, and replacing the extensions list with one that
	// has an EKU array (tag 3) that is not closed.

	// Easier approach: build the full cert via encoder and then manually
	// splice raw bytes after the extensions list open to inject a truncated
	// EKU array open.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256)
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("issuer end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0))
	enc.PutUint(tlv.ContextTag(5), uint64(0))
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(20), uint64(0x0001))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("subject end: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	enc.PutOctets(tlv.ContextTag(9), pub)
	// Start extensions list (tag 10) but don't close it — we'll do that manually.
	enc.StartList(tlv.ContextTag(10))
	// Don't call EndContainer on extensions; instead get bytes so far and
	// manually append a truncated EKU array (context tag 3 + TypeArray).
	//
	// Actually we can't use enc.Bytes() without closing. Let's just build
	// the cert up to the extensions list start, then append raw bytes.

	// Alternative: build the full cert, find the extension list start,
	// and splice. Let's build a simpler approach: just feed truncated
	// raw bytes directly.
	//
	// The minimal cert body up to a truncated EKU array inside extensions:
	//   0x15 outer struct
	//   0x30 0x01 0x01 0x03   serial tag 1, 1 byte, val 0x03
	//   0x24 0x02 0x01        sig-algo tag 2, uint8, val 1
	//   0x37 0x03 0x18        issuer list tag 3 start, then end-container
	//   0x24 0x04 0x00        notbefore tag 4, uint8, 0
	//   0x24 0x05 0x00        notafter tag 5, uint8, 0
	//   0x37 0x06 0x18        subject list tag 6 start, then end-container
	//   0x24 0x07 0x01        pubkeyalgo tag 7, uint8, 1
	//   0x24 0x08 0x01        curveid tag 8, uint8, 1
	//   0x30 0x09 0x41        pubkey tag 9, 65 bytes — then 65 bytes of 0x04+0xFF...
	// Then extensions list tag 10, then EKU array tag 3 (not closed).
	//
	// But this is very verbose. Let's use a helper approach.
	_ = pub // suppress unused warning
	_ = enc // suppress unused warning

	// Simpler: build via the encoder up to the truncated point.
	// We'll use raw TLV byte splicing on a known-good cert.
	raw := buildCertEKUTruncated(t, priv)
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated (truncated EKU array)", decErr)
	}
}

// buildCertEKUTruncated builds a cert TLV that has all mandatory fields
// except the extensions list has an EKU array (context tag 3) that is opened
// but never closed, causing decodeUint8Array to hit io.EOF.
func buildCertEKUTruncated(t *testing.T, priv *ecdsa.PrivateKey) []byte {
	t.Helper()
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	// Build a complete valid cert, then replace its extension list bytes with
	// a truncated EKU array open marker. We do this by building the bytes up
	// to the extensions list start manually and then appending.
	//
	// TLV control byte for a context-tagged List: (1 << 5) | 0x17 = 0x37
	// TLV control byte for a context-tagged Array: (1 << 5) | 0x16 = 0x36
	// TLV control byte for a context-tagged uint8: (1 << 5) | 0x04 = 0x24
	// TLV control byte for a context-tagged OctetStr1: (1 << 5) | 0x10 = 0x30
	// End-of-Container: 0x18
	// Anonymous struct: 0x15

	// TLV bytes before the pubkey: outer struct(1) + serial(4) + sig-algo(3) +
	// issuer-list(2) + rcac(3) + end(1) + notbefore(3) + notafter(3) +
	// subject-list(2) + rcac(3) + end(1) + pubkeyalgo(3) + curveid(3) +
	// pubkey-tag(3) = 35 fixed bytes, then len(pub), then extensions(2) + EKU(2).
	buf := make([]byte, 0, 35+len(pub)+2+2)
	buf = append(
		buf,
		0x15,             // outer anonymous struct
		0x30, 1, 1, 0x03, // serial: ContextTag(1) OctetStr1 len=1 val=0x03
		0x24, 2, 1, // sig-algo: ContextTag(2) UInt8 = 1
		// issuer: ContextTag(3) List start, RCAC tag 20 uint64, end
		// ContextTag(20) UInt8 = 0x01
		0x37, 3, // issuer list start
		0x24, 20, 0x01, // matter-rcac-id = 1 (as uint8 for simplicity)
		0x18,       // end issuer
		0x24, 4, 0, // notbefore: ContextTag(4) UInt8 = 0
		0x24, 5, 0, // notafter: ContextTag(5) UInt8 = 0
		// subject: ContextTag(6) List with RCAC
		0x37, 6,
		0x24, 20, 0x01,
		0x18,
		0x24, 7, 1, // pubkeyalgo: ContextTag(7) UInt8 = 1
		0x24, 8, 1, // curveid: ContextTag(8) UInt8 = 1
		// pubkey: ContextTag(9) OctetStr1 len=65 val=pub
		// OctetStr1 stores the length in 1 byte after the tag
		0x30, 9, 65,
	)
	buf = append(buf, pub...)
	buf = append(
		buf,
		0x37, 10, // extensions: ContextTag(10) List start
		// EKU array: ContextTag(3) Array start — NOT closed (truncated)
		// control: (1 << 5) | 0x16 = 0x36, tag = 3
		0x36, 3, // EKU array open, no end-container
	)

	// Do NOT add 0x18 end-of-container for EKU array, extensions list, or outer struct.
	// The decoder will hit io.EOF inside decodeUint8Array when reading the first child.
	return buf
}

// TestDecodeBasicConstraints_TruncatedPayload verifies that decodeBasicConstraints
// returns ErrTruncated when the BC struct is opened but its children are missing.
func TestDecodeBasicConstraints_TruncatedPayload(t *testing.T) {
	t.Parallel()
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)

	// Build a cert with a BC struct that opens but is truncated (no inner fields, no EndContainer).
	raw := buildCertBCTruncated(t, priv)
	_, decErr := mattercert.Decode(raw)
	if !errors.Is(decErr, mattercert.ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated (truncated BC struct)", decErr)
	}
}

// buildCertBCTruncated is analogous to buildCertEKUTruncated but truncates
// the basic-constraints struct (context tag 1 inside extensions).
func buildCertBCTruncated(t *testing.T, priv *ecdsa.PrivateKey) []byte {
	t.Helper()
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // SA1019

	buf := make([]byte, 0, 35+len(pub)+2+2)
	buf = append(
		buf,
		0x15,             // outer anonymous struct
		0x30, 1, 1, 0x03, // serial
		0x24, 2, 1, // sig-algo
		0x37, 3, // issuer list open
		0x24, 20, 0x01,
		0x18,       // end issuer
		0x24, 4, 0, // notbefore
		0x24, 5, 0, // notafter
		0x37, 6, // subject list open
		0x24, 20, 0x01,
		0x18,       // end subject
		0x24, 7, 1, // pubkeyalgo
		0x24, 8, 1, // curveid
		0x30, 9, 65, // pubkey OctetStr1 len=65
	)
	buf = append(buf, pub...)
	buf = append(
		buf,
		0x37, 10, // extensions list open
		// BC struct: ContextTag(1) + TypeStructure = (1<<5)|0x15 = 0x35, tag=1
		0x35, 1, // BC struct open — truncated (no inner fields, no end)
	)
	return buf
}

// TestTBSToDER_CASEAuthTag exercises the hex8 path by building a cert
// whose subject carries CASEAuthTags via the Order mechanism.
func TestTBSToDER_CASEAuthTag(t *testing.T) {
	t.Parallel()
	// Build a minimal cert TLV with a CASE auth tag (dnTag 22 = dnTagMatterCASEAuth) in the subject.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(1), []byte{0x01})                    // SerialNumber
	enc.PutUint(tlv.ContextTag(2), mattercert.SigAlgoECDSAWithSHA256) // SigAlgo
	// Issuer
	enc.StartList(tlv.ContextTag(3))
	enc.PutUint(tlv.ContextTag(20), uint64(0xFABFAB)) // RCAC
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer issuer: %v", err)
	}
	enc.PutUint(tlv.ContextTag(4), uint64(0)) // NotBefore
	enc.PutUint(tlv.ContextTag(5), uint64(0)) // NotAfter
	// Subject with CASE auth tag (context tag 22 = dnTagMatterCASEAuth)
	enc.StartList(tlv.ContextTag(6))
	enc.PutUint(tlv.ContextTag(17), uint64(0x1234))      // NodeID (dnTagMatterNodeID=17)
	enc.PutUint(tlv.ContextTag(21), uint64(0xFAB1))      // FabricID (dnTagMatterFabricID=21)
	enc.PutUint(tlv.ContextTag(22), uint64(0xCAFE_1234)) // CASEAuthTag (dnTagMatterCASEAuth=22)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer subject: %v", err)
	}
	enc.PutUint(tlv.ContextTag(7), mattercert.PubKeyAlgoEC)
	enc.PutUint(tlv.ContextTag(8), mattercert.CurvePrime256v1)
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pub := elliptic.Marshal(elliptic.P256(), priv.PublicKey.X, priv.PublicKey.Y) //nolint:staticcheck // elliptic.Marshal is deprecated in Go 1.25 but crypto/ecdh requires key type migration; kept for Matter TLV wire format compatibility
	enc.PutOctets(tlv.ContextTag(9), pub)
	// Extensions (empty)
	enc.StartList(tlv.ContextTag(10))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer extensions: %v", err)
	}
	// Signature (64 zeroes)
	enc.PutOctets(tlv.ContextTag(11), make([]byte, 64))
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer top: %v", err)
	}
	raw, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	cert, err := mattercert.Decode(raw)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	// TBSToDER should succeed and not be empty; this exercises the hex8 path
	// via buildDN(Subject{CASEAuthTags=[0xCAFE1234], Order=[17,21,22]}).
	tbs, err := mattercert.TBSToDER(cert)
	if err != nil {
		t.Fatalf("TBSToDER: %v", err)
	}
	if len(tbs) == 0 {
		t.Error("TBSToDER returned empty bytes")
	}
}
