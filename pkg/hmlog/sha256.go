// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog

import "crypto/sha256"

// sha256Sum is a tiny indirection so the anonymisation helper does
// not pull crypto/sha256 into every consumer's import graph when the
// feature is unused. Returns the first six bytes of the digest —
// the caller knows the surface they need.
func sha256Sum(b []byte) [6]byte {
	full := sha256.Sum256(b)
	var out [6]byte
	copy(out[:], full[:6])
	return out
}
