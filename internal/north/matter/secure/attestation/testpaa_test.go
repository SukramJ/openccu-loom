// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package attestation

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"testing"
)

func TestTestPAAFFF1Cert_Parses(t *testing.T) {
	t.Parallel()
	cert, err := x509.ParseCertificate(TestPAAFFF1Cert)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cert.Subject.CommonName != "Matter Test PAA" {
		t.Errorf("subject CN: got %q, want %q", cert.Subject.CommonName, "Matter Test PAA")
	}
	if !cert.IsCA {
		t.Error("PAA must be marked CA=true")
	}
	if cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("PAA must have KeyCertSign usage")
	}
	if !bytes.Equal(cert.SubjectKeyId, TestPAAFFF1SKID) {
		t.Errorf("SKID mismatch: got %s, want %s",
			hex.EncodeToString(cert.SubjectKeyId),
			hex.EncodeToString(TestPAAFFF1SKID))
	}
	// Self-issued: AuthorityKeyId equals SubjectKeyId.
	if !bytes.Equal(cert.AuthorityKeyId, cert.SubjectKeyId) {
		t.Errorf("AKID/SKID drift: aki=%s, skid=%s",
			hex.EncodeToString(cert.AuthorityKeyId),
			hex.EncodeToString(cert.SubjectKeyId))
	}
}

func TestTestPAAFFF1PrivateKey_MatchesCert(t *testing.T) {
	t.Parallel()
	cert, err := x509.ParseCertificate(TestPAAFFF1Cert)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("cert public key is %T, want *ecdsa.PublicKey", cert.PublicKey)
	}
	//nolint:staticcheck // SA1019: direct X/Y comparison kept — the test verifies the embedded private key projects to the cert's subjectPublicKey, which is naturally expressed as big.Int comparison.
	if pub.X.Cmp(TestPAAFFF1PrivateKey.X) != 0 ||
		pub.Y.Cmp(TestPAAFFF1PrivateKey.Y) != 0 {
		t.Error("private key public component does not match cert subjectPublicKey")
	}
}

func TestTestPAAFFF1SKID_DerivedFromPublicKey(t *testing.T) {
	t.Parallel()
	// Per RFC 5280 §4.2.1.2, a common SKID derivation is SHA-1 over the
	// uncompressed EC point. The CSA Test PAA follows that convention,
	// so we double-check the embedded SKID stays in lock-step with the
	// embedded public key.
	pub := append([]byte{0x04}, TestPAAFFF1PrivateKey.X.Bytes()...)
	pub = append(pub, TestPAAFFF1PrivateKey.Y.Bytes()...)
	sum := sha1.Sum(pub) //nolint:gosec // SKID derivation only, not security
	if !bytes.Equal(sum[:], TestPAAFFF1SKID) {
		t.Errorf("embedded SKID does not match SHA1(pubkey): got %s, want %s",
			hex.EncodeToString(TestPAAFFF1SKID), hex.EncodeToString(sum[:]))
	}
}

func TestTestPAANoVIDCert_Parses(t *testing.T) {
	t.Parallel()
	cert, err := x509.ParseCertificate(TestPAANoVIDCert)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cert.Subject.CommonName != "Matter Test PAA" {
		t.Errorf("subject CN: got %q", cert.Subject.CommonName)
	}
	if !bytes.Equal(cert.SubjectKeyId, TestPAANoVIDSKID) {
		t.Errorf("SKID mismatch: got %s",
			hex.EncodeToString(cert.SubjectKeyId))
	}
}

func TestTestCMSSigner_LooksLikeAppendixF(t *testing.T) {
	t.Parallel()
	// Smoke-check the embedded scalar is non-zero and on-curve.
	if TestCMSSignerPrivateKey == nil || TestCMSSignerPrivateKey.D == nil { //nolint:staticcheck // SA1019: presence-check on the raw scalar; crypto/ecdh has no equivalent "is the private bytes set" predicate.
		t.Fatal("CMS signer private key not initialised")
	}
	if !TestCMSSignerPrivateKey.IsOnCurve(
		TestCMSSignerPrivateKey.X,
		TestCMSSignerPrivateKey.Y,
	) {
		t.Fatal("CMS signer public key is not on the P-256 curve")
	}
	if len(TestCMSSignerSKID) != 20 {
		t.Errorf("SKID length: got %d, want 20", len(TestCMSSignerSKID))
	}
}
