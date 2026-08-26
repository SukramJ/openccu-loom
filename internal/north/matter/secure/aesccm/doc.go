// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package aesccm implements AES-CCM-128 with a 13-byte nonce and a
// 16-byte tag — the AEAD primitive Matter uses for Secure Channel
// frame encryption per Matter Core Specification §4.4.3.
//
// Stdlib `crypto/cipher` ships GCM but not CCM; this package fills
// the gap. The implementation follows RFC 3610 verbatim with the
// parameter set fixed by Matter (M = 16, L = 2, N = 13).
//
// API:
//
//	c, _ := aesccm.New(key) // key must be 16 bytes
//	sealed, _ := c.Seal(dst, nonce, plaintext, aad)
//	plain,  _ := c.Open(dst, nonce, sealed, aad)
//
// The package is dependency-free Go stdlib (uses crypto/aes for the
// block primitive plus crypto/subtle for constant-time tag compare).
package aesccm
