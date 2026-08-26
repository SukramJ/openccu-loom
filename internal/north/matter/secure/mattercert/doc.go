// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package mattercert decodes and verifies Matter Operational
// Certificates (NOC, ICAC, RCAC) per Matter Core Specification §6.5.
//
// Matter operational certificates are TLV-encoded — *not* ASN.1
// X.509 — though they carry the same conceptual fields (serial,
// issuer, validity, subject, public key, signature). The TLV layout
// follows §6.5 with context-tagged fields under a top-level
// structure.
//
// The package serves two consumers:
//
//   - The [Verifier] type implements
//     [github.com/SukramJ/openccu-loom/internal/north/matter/secure/sigma.PeerVerifier]
//     so the CASE handshake can validate peer NOCs against the local
//     fabric root.
//
//   - Stufe 6 (commissioning driver) uses the decoder to inspect the
//     bridge's own NOC after AddNOC, and to validate the DAC chain
//     (DAC → PAI → PAA) supplied by the commissioner during PASE.
//
// The package depends on [internal/north/matter/tlv] for the wire
// codec and on [crypto/ecdsa] + [crypto/sha256] for signature
// verification. No CGo, no external X.509 library.
package mattercert
