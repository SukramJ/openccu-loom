// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package attestation

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // SubjectKeyIdentifier derivation per RFC 5280; not security-relevant.
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"fmt"
	"math/big"
	"time"
)

// Matter Attestation Certificate Subject DN attributes
// (Matter §6.5.6.1).
//
// Both attributes are UTF8String holding the upper-case 4-hex-character
// representation of the unsigned 16-bit ID. Commissioners parse them
// out of the Subject DN to bind the certificate to a specific
// (vendor, product) pair — Apple Home rejects bridges whose VID/PID
// does not appear in the DAC.
var (
	oidMatterVendorID  = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 37244, 2, 1}
	oidMatterProductID = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 37244, 2, 2}
)

// matterAttCertName composes the Subject DN for a Matter attestation
// certificate: a CommonName plus the matter-oid-vid attribute and
// optionally matter-oid-pid for DAC subjects.
//
// crypto/x509 only consults `ExtraNames` (not `Names`) when emitting
// a fresh DN — `Names` is the parsed surface. ExtraNames is therefore
// the single source of truth for the encoded order: CN, VID, [PID].
func matterAttCertName(cn string, vid, pid uint16, includePID bool) pkix.Name {
	extra := []pkix.AttributeTypeAndValue{
		{Type: oidCommonName, Value: cn},
		{Type: oidMatterVendorID, Value: fmt.Sprintf("%04X", vid)},
	}
	if includePID {
		extra = append(extra, pkix.AttributeTypeAndValue{
			Type: oidMatterProductID, Value: fmt.Sprintf("%04X", pid),
		})
	}
	return pkix.Name{ExtraNames: extra}
}

// oidCommonName is RFC 5280 id-at-commonName (2.5.4.3). Spelled out
// here so [matterAttCertName] can place it in the same ExtraNames
// slice as the Matter-specific OIDs and control the encoding order.
var oidCommonName = asn1.ObjectIdentifier{2, 5, 4, 3}

// computeSKID derives the 20-byte SubjectKeyIdentifier from an EC
// public key per the SHA-1(uncompressed point) convention used by
// chip-tool / matter.js. Apple's commissioner verifies the AKI on the
// child certificate matches this SKID byte-for-byte.
func computeSKID(pub *ecdsa.PublicKey) []byte {
	raw := elliptic.Marshal(pub.Curve, pub.X, pub.Y) //nolint:staticcheck // matter.js / chip-tool compatibility
	sum := sha1.Sum(raw)                             //nolint:gosec // SKID derivation per RFC 5280
	return sum[:]
}

// Chain bundles the materials the OperationalCredentials
// cluster surfaces during PASE: the DAC private key (signs
// AttestationResponse) plus the DAC and PAI certificates the cluster
// emits via CertificateChainResponse.
type Chain struct {
	DACKey *ecdsa.PrivateKey
	DAC    []byte
	PAI    []byte
}

// BuildTestChain returns a fresh DAC + PAI rooted at the embedded
// CSA Test PAA (VID 0xFFF1). The PAI is generated once per call with
// a random key and the requested vendor ID; the DAC is generated
// from a freshly-minted P-256 key with the given (vendor, product)
// pair. Apple Home, Google Home, and chip-tool all accept this chain
// without operator-supplied attestation material.
//
// The returned PAI key is discarded — only the certificate ships;
// signing capability stays with the bridge via the DAC key.
func BuildTestChain(vid, pid uint16) (*Chain, error) {
	paiKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("pai keygen: %w", err)
	}
	dacKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("dac keygen: %w", err)
	}

	paaCert, err := x509.ParseCertificate(TestPAAFFF1Cert)
	if err != nil {
		return nil, fmt.Errorf("parse PAA: %w", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	notBefore := now.Add(-time.Hour)
	notAfter := now.Add(20 * 365 * 24 * time.Hour) // 20 years.

	paiSerial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, fmt.Errorf("pai serial: %w", err)
	}
	paiSubject := matterAttCertName(fmt.Sprintf("Matter Dev PAI 0x%04X", vid), vid, 0, false)
	paiSKID := computeSKID(&paiKey.PublicKey)
	paiTmpl := &x509.Certificate{
		SerialNumber:          paiSerial,
		Subject:               paiSubject,
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SubjectKeyId:          paiSKID,
		AuthorityKeyId:        TestPAAFFF1SKID,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
	}
	paiDER, err := x509.CreateCertificate(rand.Reader, paiTmpl, paaCert, &paiKey.PublicKey, TestPAAFFF1PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("pai sign: %w", err)
	}
	// Re-parse to obtain an Issuer DN structure for the DAC template.
	paiCert, err := x509.ParseCertificate(paiDER)
	if err != nil {
		return nil, fmt.Errorf("re-parse PAI: %w", err)
	}

	dacSerial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, fmt.Errorf("dac serial: %w", err)
	}
	dacSubject := matterAttCertName(fmt.Sprintf("Matter Dev DAC 0x%04X/0x%04X", vid, pid), vid, pid, true)
	dacSKID := computeSKID(&dacKey.PublicKey)
	dacTmpl := &x509.Certificate{
		SerialNumber: dacSerial,
		Subject:      dacSubject,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		// Matter §6.2.2.1 mandates Extended Key Usage = clientAuth on
		// the DAC. chip's DefaultDeviceAttestationVerifier verifies it
		// strictly in 2026; Apple's verifier currently tolerates the
		// absence but logs the gap. Mirror the spec to stay future-
		// proof against strict-mode controllers.
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		SubjectKeyId:          dacSKID,
		AuthorityKeyId:        paiSKID,
		SignatureAlgorithm:    x509.ECDSAWithSHA256,
	}
	dacDER, err := x509.CreateCertificate(rand.Reader, dacTmpl, paiCert, &dacKey.PublicKey, paiKey)
	if err != nil {
		return nil, fmt.Errorf("dac sign: %w", err)
	}
	return &Chain{
		DACKey: dacKey,
		DAC:    dacDER,
		PAI:    paiDER,
	}, nil
}
