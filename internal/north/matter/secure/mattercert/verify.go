// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mattercert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Verifier validates a NOC chain against a fabric root.
//
// Two roles:
//
//   - Sigma: the responder side of CASE looks up the local root
//     for the fabric the initiator claims, then asks the verifier
//     to validate the initiator's NOC + optional ICAC chain.
//
//   - Commissioning (Stufe 6): after AddNOC the bridge inspects
//     its own newly-issued NOC + ICAC against the trusted root
//     supplied by AddTrustedRootCertificate.
//
// The verifier carries a clock so tests can pin "now" via a
// [TimeSource] implementation; production code uses [SystemTime].
type Verifier struct {
	root *ecdsa.PublicKey
	now  TimeSource
}

// TimeSource returns the current time for NotBefore/NotAfter checks.
type TimeSource interface {
	Now() time.Time
}

// SystemTime delegates to time.Now.
type SystemTime struct{}

// Now implements [TimeSource].
func (SystemTime) Now() time.Time { return time.Now() }

// FixedTime returns a [TimeSource] that always reports t — for tests.
type FixedTime struct{ T time.Time }

// Now implements [TimeSource].
func (f FixedTime) Now() time.Time { return f.T }

// Verification errors.
var (
	// ErrSignatureInvalid is returned when an ECDSA verify fails.
	ErrSignatureInvalid = errors.New("mattercert: signature invalid")
	// ErrChainBroken is returned when the chain does not link up
	// (e.g. NOC issuer != ICAC subject, or ICAC issuer != Root subject).
	ErrChainBroken = errors.New("mattercert: certificate chain broken")
	// ErrExpired is returned when the certificate's validity window
	// excludes the current time.
	ErrExpired = errors.New("mattercert: certificate expired or not yet valid")
	// ErrInvalidRCAC is returned when a Root CA certificate fails the
	// structural checks that go beyond signature verification.
	ErrInvalidRCAC = errors.New("mattercert: RCAC structural validation failed")
)

// KeyUsage bit masks per Matter §6.5.1.4 (mirrors RFC 5280 §4.2.1.3).
const (
	// KeyUsageKeyCertSign must be set on every CA certificate.
	// Mirrors chip src/credentials/CHIPCert.cpp ValidateChipRCAC keyCertSign check.
	KeyUsageKeyCertSign uint16 = 1 << 5
	// KeyUsageCRLSign is defined for completeness; chip does NOT require it
	// on an RCAC — only keyCertSign is mandatory per CHIPCert.cpp:1141.
	// Kept as a named constant so callers can inspect the bit without
	// hard-coding the mask, but ValidateRCAC intentionally omits it.
	KeyUsageCRLSign uint16 = 1 << 6
)

// ValidateRCAC checks the structural invariants that chip enforces on Root
// CA certificates beyond what the chain verifier already covers:
//
//   - Subject == Issuer (self-signed: the RCAC-ID in both must match).
//   - SubjectKeyIdentifier == AuthorityKeyIdentifier (self-signed key binding).
//   - KeyUsage extension present with keyCertSign set (cRLSign is NOT required).
//   - BasicConstraints PathLenConstraint, when PRESENT, must be ≤ 1 (Matter
//     restricts depth to one ICAC). chip CHIPCert.cpp:1136-1139 wraps the
//     bound in `if (mCertFlags.Has(kPathLenConstraintPresent))` — an RCAC
//     without an explicit PathLenConstraint passes (a missing constraint
//     means "no caller-imposed depth limit"), and PathLen=0 is also
//     accepted because chip imposes no lower bound.
//   - ECDSA self-signature verification: the RCAC must be signed by its own key.
//
// Mirrors connectedhomeip/src/credentials/CHIPCert.cpp:1116-1144
// ValidateChipRCAC verbatim — the previous "PathLen must be present AND > 0"
// guards were stricter than chip and rejected the chip-tool / openssl
// commissioner default RCAC (Path­LenConstraint absent), breaking
// commissioning at SendTrustedRootCert with `BasicConstraints
// PathLenConstraint absent on RCAC`.
// Returns [ErrInvalidRCAC] (wrapping a descriptive message) when any check fails.
func ValidateRCAC(c *Certificate) error {
	if !c.IsRoot() {
		return fmt.Errorf("%w: cert is not a Root CA", ErrInvalidRCAC)
	}
	// Self-signed: Subject RCAC-ID must equal Issuer RCAC-ID.
	// Mirrors chip CHIPCert.cpp:1131 mSubjectDN.IsEqual(mIssuerDN).
	if c.Subject.MatterRCACID != c.Issuer.MatterRCACID {
		return fmt.Errorf("%w: Subject RCAC-ID (0x%016X) != Issuer RCAC-ID (0x%016X); root must be self-signed",
			ErrInvalidRCAC, c.Subject.MatterRCACID, c.Issuer.MatterRCACID)
	}
	// SubjectKeyId must equal AuthorityKeyId on a self-signed certificate.
	// Mirrors chip CHIPCert.cpp:1133 mSubjectKeyId.data_equal(mAuthKeyId).
	if c.Extensions.HasSubjectKeyID && c.Extensions.HasAuthorityKeyID {
		if !bytesEqual(c.Extensions.SubjectKeyID, c.Extensions.AuthorityKeyID) {
			return fmt.Errorf("%w: SubjectKeyId != AuthorityKeyId on self-signed RCAC", ErrInvalidRCAC)
		}
	}
	// KeyUsage must include keyCertSign. chip checks ONLY kKeyCertSign;
	// cRLSign is NOT required by the Matter spec or chip.
	// Mirrors chip CHIPCert.cpp:1141 kKeyUsage_KeyCertSign.
	if !c.Extensions.HasKeyUsage {
		return fmt.Errorf("%w: KeyUsage extension absent", ErrInvalidRCAC)
	}
	if c.Extensions.KeyUsage&KeyUsageKeyCertSign == 0 {
		return fmt.Errorf("%w: KeyUsage=0x%04X missing keyCertSign (bit 5)",
			ErrInvalidRCAC, c.Extensions.KeyUsage)
	}
	// BasicConstraints PathLenConstraint, when present, must be ≤ 1
	// (Matter restricts depth to at most one ICAC per §6.5.2.3).
	// chip CHIPCert.cpp:1136-1139 ONLY checks the upper bound and ONLY
	// when the constraint is present — an RCAC without an explicit
	// PathLenConstraint (chip-tool / openssl default) is accepted.
	if c.Extensions.BasicConstraintsHasPathLen && c.Extensions.BasicConstraintsPathLen > 1 {
		return fmt.Errorf("%w: BasicConstraints PathLenConstraint=%d exceeds 1 (Matter allows at most one ICAC)",
			ErrInvalidRCAC, c.Extensions.BasicConstraintsPathLen)
	}
	// ECDSA self-signature: the RCAC must be signed by its own public key.
	// A commissioner that submits a foreign or unsigned root could later
	// break the CASE chain if the bridge accepts it silently.
	// Mirrors chip CHIPCert.cpp:1144 VerifyCertSignature(certData, certData).
	selfKey, err := c.PublicKeyECDSA()
	if err != nil {
		return fmt.Errorf("%w: cannot decode RCAC public key: %w", ErrInvalidRCAC, err)
	}
	if err := verifySignature(c, selfKey); err != nil {
		return fmt.Errorf("%w: RCAC self-signature invalid: %w", ErrInvalidRCAC, err)
	}
	return nil
}

// bytesEqual returns true when a and b have identical length and contents.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// NewVerifier returns a verifier rooted at rootPubKey. The key must be
// a P-256 public key in uncompressed (0x04 prefix, 65 bytes) form —
// the canonical Matter root-CA public-key shape.
func NewVerifier(rootPubKey []byte, clock TimeSource) (*Verifier, error) {
	if len(rootPubKey) != 65 || rootPubKey[0] != 0x04 {
		return nil, fmt.Errorf("%w: root pub key length=%d prefix=%#x", ErrMalformed, len(rootPubKey), rootPubKey[0])
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), rootPubKey) //nolint:staticcheck // SA1019: required for raw point decode
	if x == nil {
		return nil, fmt.Errorf("%w: root pub key off-curve", ErrMalformed)
	}
	if clock == nil {
		clock = SystemTime{}
	}
	return &Verifier{
		root: &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y},
		now:  clock,
	}, nil
}

// PeerNodeIDFromNOC implements the [sigma.PeerNodeIDExtractor]
// optional surface. It decodes noc and returns the operational
// NodeID from the certificate subject. The Sigma responder pipes
// this into [channel.Config.PeerNodeID] so the AES-CCM nonce on
// the inbound secure channel (which uses the peer's own NodeID)
// matches.
func (v *Verifier) PeerNodeIDFromNOC(noc []byte) (uint64, error) {
	c, err := Decode(noc)
	if err != nil {
		return 0, fmt.Errorf("noc decode: %w", err)
	}
	if !c.Subject.HasNodeID {
		return 0, fmt.Errorf("%w: NOC subject missing NodeID", ErrMalformed)
	}
	return c.Subject.MatterNodeID, nil
}

// PeerCATsFromNOC implements the [sigma.PeerCATsExtractor] optional
// surface. It decodes noc and returns the list of CASE Authenticated
// Tags from the certificate subject. The Sigma responder pipes this
// into [channel.Config.PeerCATs] so the IM dispatcher's ACL gate can
// evaluate per-subject ACEs that target administrator groups (Matter
// §9.10.5.6 + chip src/access/AccessControl.cpp:481).
//
// Returns nil + no error when the NOC subject carries no CATs — that
// is the common case (single-admin fabrics). chip
// src/lib/core/CASEAuthTag.h enforces (identifier, version) packing
// inside a 32-bit CAT; the values stored here match that layout
// verbatim so [matchesCATSubject] decoding in the dispatcher round-trips.
func (v *Verifier) PeerCATsFromNOC(noc []byte) ([]uint32, error) {
	c, err := Decode(noc)
	if err != nil {
		return nil, fmt.Errorf("noc decode: %w", err)
	}
	if len(c.Subject.CASEAuthTags) == 0 {
		return nil, nil
	}
	out := make([]uint32, len(c.Subject.CASEAuthTags))
	copy(out, c.Subject.CASEAuthTags)
	return out, nil
}

// VerifyAndExtractPubKey is the [sigma.PeerVerifier] surface. It
// decodes the supplied NOC bytes, optionally walks an ICAC, and
// verifies the chain back to the verifier's root. Returns the
// operational public key embedded in the NOC.
//
// Errors translate into [sigma.ErrSignatureInvalid] at the Sigma
// layer; this method preserves the more specific error inside the
// returned wrapper for diagnostics.
func (v *Verifier) VerifyAndExtractPubKey(noc, icac []byte) (*ecdsa.PublicKey, error) {
	nocCert, err := Decode(noc)
	if err != nil {
		return nil, fmt.Errorf("noc: %w", err)
	}
	if !nocCert.IsNOC() {
		return nil, fmt.Errorf("%w: cert is not a NOC", ErrMalformed)
	}
	if err := v.checkValidity(nocCert); err != nil {
		return nil, fmt.Errorf("noc: %w", err)
	}

	var issuerKey *ecdsa.PublicKey
	if len(icac) > 0 {
		icacCert, err := Decode(icac)
		if err != nil {
			return nil, fmt.Errorf("icac: %w", err)
		}
		if !icacCert.IsICA() {
			return nil, fmt.Errorf("%w: cert is not an ICAC", ErrMalformed)
		}
		if err := v.checkValidity(icacCert); err != nil {
			return nil, fmt.Errorf("icac: %w", err)
		}
		// ICAC must be signed by the root.
		if err := verifySignature(icacCert, v.root); err != nil {
			return nil, fmt.Errorf("icac: %w", err)
		}
		// NOC must be signed by ICAC.
		icacPub, err := icacCert.PublicKeyECDSA()
		if err != nil {
			return nil, fmt.Errorf("icac: %w", err)
		}
		issuerKey = icacPub
		// Chain link check: NOC.Issuer.MatterICACID == ICAC.Subject.MatterICACID.
		if nocCert.Issuer.MatterICACID != icacCert.Subject.MatterICACID || !icacCert.Subject.HasICACID {
			return nil, fmt.Errorf("%w: NOC issuer ICAC-ID does not match ICAC subject", ErrChainBroken)
		}
	} else {
		// NOC is signed directly by the root.
		issuerKey = v.root
	}

	if err := verifySignature(nocCert, issuerKey); err != nil {
		return nil, fmt.Errorf("noc: %w", err)
	}
	return nocCert.PublicKeyECDSA()
}

// checkValidity confirms the certificate's NotBefore/NotAfter window
// covers v.now. NotBefore / NotAfter are Matter-epoch seconds (offsets
// from 2000-01-01T00:00:00Z per §6.5.1.5); convert to Unix seconds
// before comparing against the wall clock.
func (v *Verifier) checkValidity(c *Certificate) error {
	now := uint64(v.now.Now().Unix()) //nolint:gosec // unix-epoch fits in uint64 for centuries; see #20
	notBeforeUnix := c.NotBefore + uint64(matterEpochUTCSeconds)
	if now < notBeforeUnix {
		return fmt.Errorf("%w: now=%d < NotBefore=%d", ErrExpired, now, notBeforeUnix)
	}
	// NotAfter == 0 means "no expiry" (Matter convention for very
	// long-lived RCACs); honor it as never-expiring.
	if c.NotAfter != 0 {
		notAfterUnix := c.NotAfter + uint64(matterEpochUTCSeconds)
		if now > notAfterUnix {
			return fmt.Errorf("%w: now=%d > NotAfter=%d", ErrExpired, now, notAfterUnix)
		}
	}
	return nil
}

// verifySignature ECDSA-verifies the certificate's Signature field
// against issuerKey. Per Matter Core §6.5 / matter.js
// Certificate.verifyChain the signature covers the certificate's
// **X.509 DER form** with the signatureAlgorithm + signature
// stripped — i.e. the TBSCertificate. Hashing the raw Matter TLV
// (our previous approach) only validates self-issued test certs and
// rejects every real-world commissioner's NOC, including Apple Home.
func verifySignature(c *Certificate, issuerKey *ecdsa.PublicKey) error {
	if len(c.Signature) != 64 {
		return fmt.Errorf("%w: signature length=%d", ErrMalformed, len(c.Signature))
	}
	r := new(big.Int).SetBytes(c.Signature[:32])
	s := new(big.Int).SetBytes(c.Signature[32:])

	tbs, err := TBSToDER(c)
	if err != nil {
		return fmt.Errorf("rebuild tbs der: %w", err)
	}
	hash := sha256.Sum256(tbs)
	if !ecdsa.Verify(issuerKey, hash[:], r, s) {
		return ErrSignatureInvalid
	}
	return nil
}
