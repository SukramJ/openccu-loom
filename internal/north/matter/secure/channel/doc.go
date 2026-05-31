// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package channel implements the Matter Secure Channel session
// wrapper per Matter Core Specification §4.4.3.
//
// A Session bundles:
//
//   - Encrypt key (16 bytes) for outbound traffic.
//   - Decrypt key (16 bytes) for inbound traffic.
//   - Local source-node ID (used in nonce construction).
//   - Outbound message counter ([..]/transport/mrp Counter).
//   - Inbound duplicate-detection window ([..]/transport/mrp Window).
//
// Encryption uses AES-CCM-128 ([..]/secure/aesccm) with the
// Matter-mandated nonce layout:
//
//	nonce[0]    = Security Flags (from Message Header)
//	nonce[1:5]  = Message Counter (4B little-endian)
//	nonce[5:13] = Source Node ID (8B little-endian)
//
// AAD is the marshalled unencrypted Message Header (everything before
// the encrypted body — Flags, Session ID, Security Flags, Counter,
// Source / Destination Node IDs).
//
// The session is the building block above MRP and below the
// Interaction Model. Key derivation (Ke from Spake2+, I2R/R2I from
// Sigma) lives in [..]/secure/spake2 and the future
// [..]/secure/sigma.
package channel
