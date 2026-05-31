// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package spake2 implements SPAKE2+ over P-256 per Matter Core
// Specification §3.10 (which itself profiles RFC 9383). SPAKE2+ is
// Matter's Password-Authenticated Key Exchange (PAKE) primitive used
// to establish a Passcode-Authenticated Session (PASE) during
// commissioning.
//
// The implementation supports both roles:
//
//   - Verifier — the role openccu-loom plays as the bridged Matter
//     device being commissioned. The verifier holds (w0, L = w1·G)
//     and never sees the passcode.
//   - Prover — the role a Matter commissioner plays. The prover
//     holds the passcode and derives (w0, w1) on every session.
//
// Both roles are exposed so the package is testable end-to-end
// without an external Matter controller.
//
// Cryptographic primitives:
//
//   - P-256 (NIST secp256r1) for points and scalars.
//   - PBKDF2-HMAC-SHA256 for passcode → (w0, w1).
//   - HKDF-SHA256 for the SPAKE2+ key schedule.
//   - HMAC-SHA256 for confirmation tags.
//
// The Matter-mandated points M and N are baked in as compressed
// constants from Matter Core Spec §3.10.1 (which references the
// SPAKE2+ test-vector points published with RFC 9383).
//
// Limitations of this v1.1 implementation:
//
//   - The high-level flow uses [crypto/elliptic.P256] arithmetic
//     (deprecated in newer Go releases for ECDSA, but still the
//     supported path for raw scalar multiplication needed here).
//   - Constant-time comparisons for confirmation tags use
//     [crypto/subtle].
//   - The package is dependency-free Go stdlib.
package spake2
