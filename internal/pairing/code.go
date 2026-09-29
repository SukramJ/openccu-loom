// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package pairing lets an external client obtain an API token without
// anybody copying a secret: the client asks unauthenticated, both sides
// independently derive the same six-digit code, an administrator compares
// and TYPES that code in the Config UI, and the waiting client receives
// its token exactly once. The protocol mirrors the occulited pairing this
// daemon already speaks as a client (internal/client/transport/occulited),
// with loom's role model in place of scope areas; the wire contract is
// documented in docs/adr/0076-client-pairing.md.
package pairing

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// Code is the six-digit code both sides compute: the first four bytes of
// SHA-256(nonce ‖ clientNonce ‖ fingerprint), read big-endian, modulo one
// million, zero-padded. fingerprint is the SHA-256 of the certificate DER
// the server serves (and the client therefore saw), empty over plain
// HTTP. The client commits to clientNonce (as its SHA-256) before it
// learns nonce and reveals it with its first poll, so an interceptor
// cannot search for a certificate whose code matches.
func Code(nonce, clientNonce, fingerprint []byte) string {
	h := sha256.New()
	h.Write(nonce)
	h.Write(clientNonce)
	h.Write(fingerprint)
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(h.Sum(nil)[:4])%1_000_000)
}
