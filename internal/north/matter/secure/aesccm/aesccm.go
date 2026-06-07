// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package aesccm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// KeySize is the AES key length (128-bit Matter mandated).
	KeySize = 16
	// NonceSize is the Matter-mandated nonce length (Core Spec §4.4.3).
	NonceSize = 13
	// TagSize is the full 16-byte authentication tag length.
	TagSize = 16
	// blockSize is the AES block size.
	blockSize = 16
	// l is the length-field byte width per RFC 3610 (15 - NonceSize).
	l = 15 - NonceSize // = 2
)

// Errors.
var (
	// ErrKeySize is returned when [New] is called with a non-16-byte
	// key.
	ErrKeySize = errors.New("aesccm: key must be 16 bytes")
	// ErrNonceSize is returned for nonces of an unexpected length.
	ErrNonceSize = errors.New("aesccm: nonce must be 13 bytes")
	// ErrAuthFailed is returned by [CCM.Open] when the recomputed tag
	// does not match the sealed tag — the caller MUST treat this as a
	// possible tampering event and discard the message.
	ErrAuthFailed = errors.New("aesccm: authentication failed")
	// ErrSealedTooShort is returned by [CCM.Open] when the sealed
	// payload is smaller than [TagSize].
	ErrSealedTooShort = errors.New("aesccm: sealed payload shorter than tag")
	// ErrPlaintextTooLong is returned when the plaintext exceeds the
	// 2^16 - 1 limit imposed by L = 2.
	ErrPlaintextTooLong = errors.New("aesccm: plaintext exceeds 2^16-1 bytes")
)

// CCM is a stateful AES-CCM-128 cipher. Construct with [New] and
// reuse for multiple Seal / Open calls — the underlying cipher.Block
// is concurrency-safe.
type CCM struct {
	block cipher.Block
}

// New returns a CCM bound to key. Key length must be [KeySize] (16).
func New(key []byte) (*CCM, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("%w: got %d", ErrKeySize, len(key))
	}
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aesccm: aes.NewCipher: %w", err)
	}
	return &CCM{block: b}, nil
}

// Seal encrypts and authenticates plaintext under (nonce, aad) and
// appends the resulting ciphertext + tag to dst. Pass nil dst to get
// a freshly-allocated result. Returns ErrNonceSize / ErrPlaintextTooLong
// for invalid inputs.
func (c *CCM) Seal(dst, nonce, plaintext, aad []byte) ([]byte, error) {
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("%w: got %d", ErrNonceSize, len(nonce))
	}
	if len(plaintext) > 0xFFFF {
		return nil, fmt.Errorf("%w: %d bytes", ErrPlaintextTooLong, len(plaintext))
	}

	tag := c.cbcMAC(nonce, plaintext, aad)
	out := append(dst, plaintext...) //nolint:gocritic // building the result; appendAssign would discard tag
	c.ctrCrypt(nonce, out[len(dst):])
	encryptedTag := c.encryptTag(nonce, tag)
	out = append(out, encryptedTag...)
	return out, nil
}

// Open verifies + decrypts sealed (= ciphertext || tag). On success
// the plaintext is appended to dst and returned. On failure
// [ErrAuthFailed] is returned and dst is unmodified.
func (c *CCM) Open(dst, nonce, sealed, aad []byte) ([]byte, error) {
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("%w: got %d", ErrNonceSize, len(nonce))
	}
	if len(sealed) < TagSize {
		return nil, fmt.Errorf("%w: %d bytes", ErrSealedTooShort, len(sealed))
	}

	cipherTextLen := len(sealed) - TagSize
	if cipherTextLen > 0xFFFF {
		return nil, fmt.Errorf("%w: %d bytes", ErrPlaintextTooLong, cipherTextLen)
	}

	// Recover the plaintext into a scratch buffer that we can hand
	// the MAC routine without disturbing dst.
	plaintext := make([]byte, cipherTextLen)
	copy(plaintext, sealed[:cipherTextLen])
	c.ctrCrypt(nonce, plaintext)

	// Recover the original tag.
	wireTag := make([]byte, TagSize)
	copy(wireTag, sealed[cipherTextLen:])
	encZero := c.encryptTag(nonce, make([]byte, TagSize))
	for i := range wireTag {
		wireTag[i] ^= encZero[i]
	}

	expected := c.cbcMAC(nonce, plaintext, aad)
	if subtle.ConstantTimeCompare(wireTag, expected) != 1 {
		return nil, ErrAuthFailed
	}
	return append(dst, plaintext...), nil
}

// cbcMAC computes the 16-byte CBC-MAC over the formatted (B0 || AAD ||
// plaintext) buffer per RFC 3610 §2.2.
func (c *CCM) cbcMAC(nonce, plaintext, aad []byte) []byte {
	hasAAD := len(aad) > 0

	// B0: flags || nonce || msg-length (l bytes, big-endian).
	flags := byte((TagSize-2)/2)<<3 | (l - 1)
	if hasAAD {
		flags |= 0x40
	}
	b0 := make([]byte, blockSize)
	b0[0] = flags
	copy(b0[1:1+NonceSize], nonce)
	binary.BigEndian.PutUint16(b0[1+NonceSize:], uint16(len(plaintext))) //nolint:gosec // G115: plaintext-length capped to 2^16-1 by Seal/Open; see #20

	state := make([]byte, blockSize)
	c.block.Encrypt(state, b0)

	if hasAAD {
		// AAD prefix: 2-byte big-endian length (we never exceed
		// 0xFEFF in Matter framing — larger values use the 6-byte
		// extended encoding which we don't implement because Matter
		// never uses it).
		aadBlock := make([]byte, 0, len(aad)+2)
		aadBlock = binary.BigEndian.AppendUint16(aadBlock, uint16(len(aad))) //nolint:gosec // G115: Matter AAD never exceeds 0xFEFF; see #20
		aadBlock = append(aadBlock, aad...)
		state = cbcMacBlocks(c.block, state, aadBlock)
	}

	state = cbcMacBlocks(c.block, state, plaintext)
	return state
}

// cbcMacBlocks consumes data in 16-byte blocks (zero-padding the
// final partial block) and folds each block into the running CBC-MAC
// state.
func cbcMacBlocks(b cipher.Block, state, data []byte) []byte {
	block := make([]byte, blockSize)
	for i := 0; i < len(data); i += blockSize {
		end := min(i+blockSize, len(data))
		// Reset the padding buffer.
		for j := range block {
			block[j] = 0
		}
		copy(block, data[i:end])
		for j := range block {
			state[j] ^= block[j]
		}
		b.Encrypt(state, state)
	}
	return state
}

// ctrCrypt XORs data with the AES-CTR keystream derived from nonce.
// Counter starts at 1 (counter 0 is reserved for the encrypted tag,
// produced by [encryptTag]).
func (c *CCM) ctrCrypt(nonce, data []byte) {
	a := make([]byte, blockSize)
	a[0] = byte(l - 1)
	copy(a[1:1+NonceSize], nonce)

	keystream := make([]byte, blockSize)
	counter := uint16(1)
	for i := 0; i < len(data); i += blockSize {
		binary.BigEndian.PutUint16(a[1+NonceSize:], counter)
		c.block.Encrypt(keystream, a)
		end := min(i+blockSize, len(data))
		for j := i; j < end; j++ {
			data[j] ^= keystream[j-i]
		}
		counter++
	}
}

// encryptTag XORs the CBC-MAC tag with AES_K(A_0) per RFC 3610 §2.5.
func (c *CCM) encryptTag(nonce, tag []byte) []byte {
	a0 := make([]byte, blockSize)
	a0[0] = byte(l - 1)
	copy(a0[1:1+NonceSize], nonce)
	// Counter bytes are already zero.

	out := make([]byte, blockSize)
	c.block.Encrypt(out, a0)
	for i := range tag {
		out[i] ^= tag[i]
	}
	return out[:TagSize]
}
