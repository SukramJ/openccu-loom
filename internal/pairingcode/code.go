// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

// Package pairingcode holds the six-digit code derivation of the pairing
// protocol (docs/adr/0076-client-pairing.md). It is a stdlib-only leaf so
// every side that speaks the protocol shares one formula: the daemon's own
// pairing server (internal/pairing), its client towards an openccu-lite box
// (internal/client/transport/occulited) and the remote add-on's proxy
// (internal/remoteproxy), which must stay free of the daemon's auth stack.
package pairingcode

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
