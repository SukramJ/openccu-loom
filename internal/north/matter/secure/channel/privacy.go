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

// Privacy-mode constants per Matter Core Spec §4.4.3.1.
const (
	// PrivacyKeySize is the byte length of the AES key that masks the
	// privacy-protected header portion. Equal to AES-128 key size.
	PrivacyKeySize = 16

	// PrivacyMICSuffixSize is the number of trailing MIC bytes that
	// participate in the privacy IV (Spec §4.4.3.1).
	PrivacyMICSuffixSize = 14

	// privacyHKDFInfo is the HKDF info string used to derive the
	// privacy key from the session encryption key. Matter §4.4.3.1
	// fixes the literal value.
	privacyHKDFInfo = "PrivacyKey"
)

// Privacy-mode errors.
var (
	// ErrPrivacyKeySource is returned by [DerivePrivacyKey] when the
	// session key has the wrong length.
	ErrPrivacyKeySource = errors.New("channel: privacy key source must be 16 bytes")

	// ErrPrivacyMICShort is returned when the MIC slice supplied to a
	// privacy operation is shorter than the spec-mandated 14-byte
	// suffix.
	ErrPrivacyMICShort = errors.New("channel: privacy MIC suffix needs ≥14 bytes")
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

// PrivacyMask produces the 16-byte AES-ECB block that the privacy-
// protected header portion is XORed with per Matter §4.4.3.1:
//
//	IV     = SessionID (BE 2B) || MIC[len-14:]    (16 bytes total)
//	Mask   = AES-ECB-Encrypt(PrivacyKey, IV)
//
// privacyKey must be exactly 16 bytes (i.e., the output of
// [DerivePrivacyKey]); mic must be at least 14 bytes long. The
// function reads only the last [PrivacyMICSuffixSize] bytes of mic.
//
// Per spec, SessionID is encoded big-endian in the privacy IV (this
// is the one place in the Matter wire protocol where a multi-byte
// integer is *not* little-endian).
func PrivacyMask(privacyKey []byte, sessionID uint16, mic []byte) ([]byte, error) {
	if len(privacyKey) != PrivacyKeySize {
		return nil, fmt.Errorf("%w: privacy key got %d", ErrPrivacyKeySource, len(privacyKey))
	}
	if len(mic) < PrivacyMICSuffixSize {
		return nil, fmt.Errorf("%w: got %d", ErrPrivacyMICShort, len(mic))
	}
	cipher, err := aes.NewCipher(privacyKey)
	if err != nil {
		return nil, fmt.Errorf("channel: privacy cipher: %w", err)
	}
	iv := make([]byte, PrivacyKeySize)
	binary.BigEndian.PutUint16(iv[0:2], sessionID)
	copy(iv[2:], mic[len(mic)-PrivacyMICSuffixSize:])
	mask := make([]byte, PrivacyKeySize)
	cipher.Encrypt(mask, iv)
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
