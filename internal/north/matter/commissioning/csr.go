// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package commissioning

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// NOCSR Elements field tags per Matter §11.18.7.6.
const (
	nocsrElCSR        uint8 = 0x01
	nocsrElCSRNonce   uint8 = 0x02
	nocsrElVendorRsv1 uint8 = 0x03
	nocsrElVendorRsv2 uint8 = 0x04
	nocsrElVendorRsv3 uint8 = 0x05
)

// Errors.
var (
	// ErrCSRNonce is returned when the supplied nonce is not 32 bytes.
	ErrCSRNonce = errors.New("commissioning: CSR nonce must be 32 bytes")
)

// CSRInput drives [BuildCSR].
type CSRInput struct {
	// CSRNonce is the 32-byte nonce from the commissioner's
	// CSRRequest.
	CSRNonce []byte
	// AttestationChallenge is the 16-byte challenge for the response
	// signature.
	AttestationChallenge []byte
	// DACPrivateKey signs the response (Matter §11.18.7.6).
	DACPrivateKey *ecdsa.PrivateKey
}

// CSRResult bundles the CSR private key, the TLV-encoded NOCSRElements,
// and the ECDSA signature over (elements ‖ challenge).
type CSRResult struct {
	// PrivateKey is the freshly generated P-256 private key. The
	// caller persists it via the OperationalCredentials cluster's
	// AddNOC handler.
	PrivateKey *ecdsa.PrivateKey
	// Elements is the TLV-encoded NOCSRElements blob.
	Elements []byte
	// Signature is the 64-byte ECDSA-P256 r‖s over SHA256(elements ‖ challenge).
	Signature []byte
	// CSR is the raw DER-encoded PKCS#10 CertificationRequest, also
	// embedded inside Elements. Exposed for diagnostic logging.
	CSR []byte
}

// BuildCSR generates a new P-256 keypair and produces a Matter
// NOCSRElements payload + signature. The commissioner extracts the
// embedded PKCS#10 CSR, signs it with the fabric ICAC, and returns
// the resulting NOC via AddNOC.
func BuildCSR(in CSRInput) (*CSRResult, error) {
	if len(in.CSRNonce) != 32 {
		return nil, fmt.Errorf("%w: got %d", ErrCSRNonce, len(in.CSRNonce))
	}
	if len(in.AttestationChallenge) != 16 {
		return nil, fmt.Errorf("%w: got %d", ErrAttestationChallenge, len(in.AttestationChallenge))
	}
	if in.DACPrivateKey == nil {
		return nil, errors.New("commissioning: DAC private key required")
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("commissioning: generate CSR key: %w", err)
	}

	csrTemplate := &x509.CertificateRequest{
		SignatureAlgorithm: x509.ECDSAWithSHA256,
		Subject:            pkix.Name{},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, priv)
	if err != nil {
		return nil, fmt.Errorf("commissioning: build CSR: %w", err)
	}

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(nocsrElCSR), csrDER)
	enc.PutOctets(tlv.ContextTag(nocsrElCSRNonce), in.CSRNonce)
	if err := enc.EndContainer(); err != nil {
		return nil, fmt.Errorf("commissioning: NOCSR TLV: %w", err)
	}
	elements, err := enc.Bytes()
	if err != nil {
		return nil, fmt.Errorf("commissioning: NOCSR TLV bytes: %w", err)
	}

	digest := sha256.New()
	digest.Write(elements)
	digest.Write(in.AttestationChallenge)
	hash := digest.Sum(nil)

	r, s, err := ecdsa.Sign(rand.Reader, in.DACPrivateKey, hash)
	if err != nil {
		return nil, fmt.Errorf("commissioning: NOCSR sign: %w", err)
	}
	sig := encodeRS(r.Bytes(), s.Bytes())
	return &CSRResult{
		PrivateKey: priv,
		Elements:   elements,
		Signature:  sig,
		CSR:        csrDER,
	}, nil
}
