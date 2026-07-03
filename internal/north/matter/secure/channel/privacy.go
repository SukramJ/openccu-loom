// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package channel

import (
	"crypto/aes"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Privacy-mode constants per Matter Core Spec §4.9.
const (
	// PrivacyKeySize is the byte length of the AES key that masks the
	// privacy-protected header portion. Equal to AES-128 key size.
	PrivacyKeySize = 16

	// PrivacyMICSuffixSize is the minimum trailing-MIC byte count the
	// framing layer requires before attempting a privacy operation. The
	// privacy nonce itself consumes the last [privacyNonceMICBytes] of
	// the full [privacyMICLength]-byte MIC; this looser gate is kept for
	// the framing-layer length check.
	PrivacyMICSuffixSize = 14

	// PrivacyNonceSize is the length of the AES-CTR privacy nonce:
	// SessionID (2 bytes) followed by the last privacyNonceMICBytes of
	// the MIC. Mirrors matter.js CRYPTO_PRIVACY_NONCE_LENGTH_BYTES (13).
	PrivacyNonceSize = 13

	// privacyMICLength is the full AES-CCM MIC (tag) length. matter.js
	// buildNonce requires exactly this many MIC bytes.
	privacyMICLength = 16

	// privacyNonceMICBytes is how many trailing MIC bytes feed the
	// privacy nonce: mic[privacyMICLength-privacyNonceMICBytes:] — i.e.
	// mic[5:16], the last 11 bytes. Mirrors matter.js NONCE_MIC_LENGTH.
	privacyNonceMICBytes = PrivacyNonceSize - 2 // 11

	// privacyCTRFlags is the AES-CCM counter-block flags byte for a
	// 13-byte nonce (L = 15-13 = 2, so flags = L-1 = 1).
	privacyCTRFlags = 0x01

	// privacyHKDFInfo is the HKDF info string used to derive the
	// privacy key from the session encryption key. Matter §4.9.1
	// fixes the literal value.
	privacyHKDFInfo = "PrivacyKey"
)

// Privacy-mode errors.
var (
	// ErrPrivacyKeySource is returned by [DerivePrivacyKey] when the
	// session key has the wrong length.
	ErrPrivacyKeySource = errors.New("channel: privacy key source must be 16 bytes")

	// ErrPrivacyMICShort is returned when the MIC slice supplied to a
	// privacy operation is shorter than the 16-byte AES-CCM tag the
	// privacy nonce is derived from (Matter §4.9; matter.js buildNonce).
	ErrPrivacyMICShort = errors.New("channel: privacy MIC needs ≥16 bytes")
)

// DerivePrivacyKey produces the 16-byte privacy key from a session
// encryption key per Matter Core Spec §4.4.3.1:
//
//	PrivacyKey = HKDF-SHA256(IKM = sessionKey, salt = "", info = "PrivacyKey", L = 16)
//
// The returned key is independent of the session encryption key — it
// is safe to derive once at session establishment and cache.
func DerivePrivacyKey(sessionKey []byte) ([]byte, error) {
	if len(sessionKey) != PrivacyKeySize {
		return nil, fmt.Errorf("%w: got %d", ErrPrivacyKeySource, len(sessionKey))
	}
	out, err := hkdf.Key(sha256.New, sessionKey, nil, privacyHKDFInfo, PrivacyKeySize)
	if err != nil {
		return nil, fmt.Errorf("channel: privacy hkdf: %w", err)
	}
	return out, nil
}

// PrivacyMask produces the 16-byte AES-CTR keystream block the
// privacy-protected header portion is XORed with per Matter §4.9.
//
//	nonce   = SessionID (BE 2B) || MIC[5:16]      (13 bytes)
//	mask    = AES-Encrypt(PrivacyKey, 0x01 || nonce || 0x0001)
//
// Header privacy is AES-CTR (expressed as AES-CCM with L=2 and empty
// AAD). The protected header region never exceeds one AES block, so a
// single keystream block suffices: the AES-CCM counter block for the
// first payload block is 0x01 || nonce || 0x0001 (flags = L-1 = 1,
// counter = 1). XORing that keystream over the region both masks and
// unmasks. Mirrors matter.js
// packages/protocol/src/codec/MessagePrivacy.ts:38-56 (buildNonce +
// obfuscate) and chip src/transport/CryptoContext.cpp:168-176.
//
// privacyKey must be exactly 16 bytes (the output of
// [DerivePrivacyKey]); mic must carry the full 16-byte AES-CCM tag —
// the nonce reads its last [privacyNonceMICBytes] bytes (mic[5:16]).
//
// SessionID is encoded big-endian in the nonce — the one place in the
// Matter wire protocol where a multi-byte integer is not little-endian.
func PrivacyMask(privacyKey []byte, sessionID uint16, mic []byte) ([]byte, error) {
	if len(privacyKey) != PrivacyKeySize {
		return nil, fmt.Errorf("%w: privacy key got %d", ErrPrivacyKeySource, len(privacyKey))
	}
	if len(mic) < privacyMICLength {
		return nil, fmt.Errorf("%w: got %d", ErrPrivacyMICShort, len(mic))
	}
	tag := mic[len(mic)-privacyMICLength:]
	cipher, err := aes.NewCipher(privacyKey)
	if err != nil {
		return nil, fmt.Errorf("channel: privacy cipher: %w", err)
	}
	// counter block: 0x01 || SessionID(BE) || MIC[5:16] || 0x0001.
	block := make([]byte, PrivacyKeySize)
	block[0] = privacyCTRFlags
	binary.BigEndian.PutUint16(block[1:3], sessionID)
	copy(block[3:1+PrivacyNonceSize], tag[privacyMICLength-privacyNonceMICBytes:])
	block[PrivacyKeySize-1] = 0x01 // low byte of the big-endian CCM counter (value 1)
	mask := make([]byte, PrivacyKeySize)
	cipher.Encrypt(mask, block)
	return mask, nil
}

// ApplyPrivacyMask XORs mask byte-by-byte over headerSlice in place.
// headerSlice must not exceed [PrivacyKeySize] bytes — Matter spec
// §4.4.3.1 sizes the privacy-protected header portion to fit within
// one AES block. Returns an error when the slice is over-sized.
//
// The caller is responsible for selecting the correct slice: per
// Spec §4.4.1.1 + §4.4.3.1, the privacy-protected portion is the
// portion of the message header AFTER the Message Flags byte, up to
// (but not including) the Security Flags byte — i.e., the Session
// ID + Message Counter + optional Source/Destination Node IDs.
//
// Privacy mode is XOR-symmetric: the same call recovers the
// plaintext when applied to a previously-masked slice.
func ApplyPrivacyMask(mask, headerSlice []byte) error {
	if len(mask) != PrivacyKeySize {
		return fmt.Errorf("channel: privacy mask must be %d bytes, got %d",
			PrivacyKeySize, len(mask))
	}
	if len(headerSlice) > PrivacyKeySize {
		return fmt.Errorf("channel: privacy slice exceeds %d bytes (got %d) — Spec §4.4.3.1 limits the protected portion to one AES block",
			PrivacyKeySize, len(headerSlice))
	}
	for i := range headerSlice {
		headerSlice[i] ^= mask[i]
	}
	return nil
}

// PrivacyKey returns the lazily-derived privacy key for outbound
// (encrypt) traffic. The key is cached on first use so subsequent
// calls return the same slice. Returns an error when the session has
// been [Session.Close]d.
//
// Used by the message-framing layer when a frame's Security Flags
// carry the Privacy bit (P, bit 7). MRP / Secure Channel control
// frames do NOT use privacy mode; only application-layer encrypted
// unicast does, and only when the session was negotiated with the
// privacy capability.
func (s *Session) PrivacyKey() ([]byte, error) {
	s.privacyMu.Lock()
	defer s.privacyMu.Unlock()
	if s.closed {
		return nil, ErrSessionInactive
	}
	if s.privacyKey == nil {
		k, err := DerivePrivacyKey(s.encKey)
		if err != nil {
			return nil, err
		}
		s.privacyKey = k
	}
	return s.privacyKey, nil
}

// PeerPrivacyKey returns the lazily-derived privacy key for inbound
// (decrypt) traffic. Unidirectional sessions where EncryptKey ==
// DecryptKey share a single privacy key with [PrivacyKey]; PASE /
// CASE bidirectional sessions split them.
func (s *Session) PeerPrivacyKey() ([]byte, error) {
	s.privacyMu.Lock()
	defer s.privacyMu.Unlock()
	if s.closed {
		return nil, ErrSessionInactive
	}
	if s.peerPrivacyKey == nil {
		k, err := DerivePrivacyKey(s.decKey)
		if err != nil {
			return nil, err
		}
		s.peerPrivacyKey = k
	}
	return s.peerPrivacyKey, nil
}
