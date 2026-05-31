// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package fabric

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Sizes per Matter Core Spec §4.13 / §11.18.
const (
	// PublicKeySize is the uncompressed P-256 public key length
	// (1-byte 0x04 prefix + 32-byte X + 32-byte Y).
	PublicKeySize = 65
	// CompressedFabricIDSize is the public-facing fabric identifier
	// length (8 bytes).
	CompressedFabricIDSize = 8
	// FabricIDSize is the on-the-wire Fabric ID size (64-bit).
	FabricIDSize = 8
)

// Errors.
var (
	// ErrInvalidPublicKey is returned for malformed root-CA public-
	// key inputs (wrong length, non-uncompressed prefix, off-curve).
	ErrInvalidPublicKey = errors.New("fabric: invalid root public key")
)

// Fabric is the in-memory representation of a Matter operational
// fabric. Persistence (to SQLite) happens at the daemon's store
// layer; this struct is the canonical shape Sigma sees.
type Fabric struct {
	// ID is the 64-bit Fabric ID assigned by the commissioner.
	ID uint64
	// NodeID is the local node's 64-bit identifier inside this fabric.
	NodeID uint64
	// RootPubKey is the trust anchor — the issuing root CA's
	// public key. Uncompressed P-256 encoding.
	RootPubKey []byte
	// VendorID is the 16-bit IANA-assigned vendor identifier baked
	// into the NOC.
	VendorID uint16

	// CompressedID is the cached output of [Fabric.Compressed]; when
	// non-zero the caller skips re-derivation.
	CompressedID [CompressedFabricIDSize]byte
}

// New constructs a Fabric and pre-computes its CompressedID. Returns
// ErrInvalidPublicKey when rootPubKey is malformed.
func New(id, nodeID uint64, rootPubKey []byte, vendorID uint16) (*Fabric, error) {
	if err := validateRootPubKey(rootPubKey); err != nil {
		return nil, err
	}
	f := &Fabric{
		ID:         id,
		NodeID:     nodeID,
		RootPubKey: append([]byte(nil), rootPubKey...),
		VendorID:   vendorID,
	}
	cid, err := computeCompressedFabricID(rootPubKey, id)
	if err != nil {
		return nil, err
	}
	copy(f.CompressedID[:], cid)
	return f, nil
}

// validateRootPubKey checks the length / prefix / on-curve invariants.
func validateRootPubKey(b []byte) error {
	if len(b) != PublicKeySize {
		return fmt.Errorf("%w: length=%d, want %d", ErrInvalidPublicKey, len(b), PublicKeySize)
	}
	if b[0] != 0x04 {
		return fmt.Errorf("%w: prefix=0x%02X, want 0x04 (uncompressed)", ErrInvalidPublicKey, b[0])
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), b) //nolint:staticcheck // SA1019: required for raw point decode
	if x == nil || y == nil {
		return fmt.Errorf("%w: point not on curve", ErrInvalidPublicKey)
	}
	return nil
}

// computeCompressedFabricID applies the HKDF derivation per Matter
// Core Spec §4.13.2.4.
//
//	salt = fabricID big-endian (8 bytes)
//	IKM  = rootPubKey[1:65] (X || Y; the 0x04 prefix is stripped)
//	info = "CompressedFabric"
//	L    = 8
func computeCompressedFabricID(rootPubKey []byte, fabricID uint64) ([]byte, error) {
	salt := make([]byte, FabricIDSize)
	binary.BigEndian.PutUint64(salt, fabricID)
	ikm := rootPubKey[1:] // drop the 0x04 prefix per spec.
	out, err := hkdf.Key(sha256.New, ikm, salt, "CompressedFabric", CompressedFabricIDSize)
	if err != nil {
		return nil, fmt.Errorf("fabric: hkdf: %w", err)
	}
	return out, nil
}

// PublicKey returns RootPubKey decoded as a [*ecdsa.PublicKey] for
// signature verification.
func (f *Fabric) PublicKey() (*ecdsa.PublicKey, error) {
	x, y := elliptic.Unmarshal(elliptic.P256(), f.RootPubKey) //nolint:staticcheck // SA1019: see validateRootPubKey
	if x == nil {
		return nil, ErrInvalidPublicKey
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
}
