// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package commissioning

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// AttestationElements field tags per Matter §11.18.5.
const (
	attElCertificationDeclaration uint8 = 0x01
	attElAttestationNonce         uint8 = 0x02
	attElTimestamp                uint8 = 0x03
	attElFirmwareInformation      uint8 = 0x04
)

// Errors.
var (
	// ErrAttestationNonce is returned when [BuildAttestation] receives
	// a nonce that is not exactly 32 bytes.
	ErrAttestationNonce = errors.New("commissioning: attestation nonce must be 32 bytes")
	// ErrAttestationChallenge is returned when AttestationChallenge
	// is not exactly 16 bytes.
	ErrAttestationChallenge = errors.New("commissioning: attestation challenge must be 16 bytes")
)

// AttestationInput drives [BuildAttestation].
type AttestationInput struct {
	// CertificationDeclaration is the CMS-signed CD bytes (Matter
	// §6.3). Pre-flashed at the factory; the bridge stores it as
	// opaque bytes.
	CertificationDeclaration []byte
	// AttestationNonce is the 32-byte nonce supplied by the
	// commissioner in AttestationRequest.
	AttestationNonce []byte
	// FirmwareInformation is optional vendor-specific firmware blob
	// (Matter §11.18.7.1.4). Empty is permitted.
	FirmwareInformation []byte
	// AttestationChallenge is the 16-byte challenge derived from the
	// PASE / CASE session (see [PASEResponder.AttestationChallenge]).
	AttestationChallenge []byte
	// DACPrivateKey is the bridge's Device Attestation private key.
	// Used to ECDSA-sign the (elements ‖ challenge) digest.
	DACPrivateKey *ecdsa.PrivateKey
	// Now is the timestamp embedded in the attestation. Zero falls
	// back to time.Now.
	Now time.Time
}

// AttestationResult bundles the elements TLV blob and its signature.
// Caller wires both into the AttestationResponse fields.
type AttestationResult struct {
	Elements  []byte // TLV-encoded (cert-declaration, nonce, timestamp, firmware)
	Signature []byte // 64-byte r||s ECDSA-P256 over SHA256(elements || challenge)
}

// BuildAttestation produces the AttestationResponse payload. The
// elements TLV is deterministic so the commissioner can re-derive
// the signature input and verify it.
func BuildAttestation(in AttestationInput) (*AttestationResult, error) {
	if len(in.AttestationNonce) != 32 {
		return nil, fmt.Errorf("%w: got %d", ErrAttestationNonce, len(in.AttestationNonce))
	}
	if len(in.AttestationChallenge) != 16 {
		return nil, fmt.Errorf("%w: got %d", ErrAttestationChallenge, len(in.AttestationChallenge))
	}
	if in.DACPrivateKey == nil {
		return nil, errors.New("commissioning: DAC private key required")
	}

	now := in.Now
	if now.IsZero() {
		now = time.Now()
	}

	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutOctets(tlv.ContextTag(attElCertificationDeclaration), in.CertificationDeclaration)
	enc.PutOctets(tlv.ContextTag(attElAttestationNonce), in.AttestationNonce)
	//nolint:gosec // unix-epoch-seconds fits in uint64 for centuries; see #20
	enc.PutUint(tlv.ContextTag(attElTimestamp), uint64(now.Unix()))
	if len(in.FirmwareInformation) > 0 {
		enc.PutOctets(tlv.ContextTag(attElFirmwareInformation), in.FirmwareInformation)
	}
	if err := enc.EndContainer(); err != nil {
		return nil, fmt.Errorf("commissioning: attestation TLV: %w", err)
	}
	elements, err := enc.Bytes()
	if err != nil {
		return nil, fmt.Errorf("commissioning: attestation TLV bytes: %w", err)
	}

	digest := sha256.New()
	digest.Write(elements)
	digest.Write(in.AttestationChallenge)
	hash := digest.Sum(nil)

	r, s, err := ecdsa.Sign(rand.Reader, in.DACPrivateKey, hash)
	if err != nil {
		return nil, fmt.Errorf("commissioning: attestation sign: %w", err)
	}
	sig := encodeRS(r.Bytes(), s.Bytes())
	return &AttestationResult{Elements: elements, Signature: sig}, nil
}

// encodeRS packs r ‖ s into a 64-byte slice with leading zeros padded.
func encodeRS(rb, sb []byte) []byte {
	out := make([]byte, 64)
	copy(out[32-len(rb):32], rb)
	copy(out[64-len(sb):64], sb)
	return out
}
