// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package codes

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (notes/concepts/alarm-concept.md §11 / slice-6 design):
// time=1, memory=64 MiB, 4 threads, a 16-byte salt and a 32-byte
// derived key. VerifyPIN re-derives with the parameters embedded in
// the stored hash rather than these constants, so a future parameter
// bump never invalidates already-issued codes.
const (
	argon2Time    = 1
	argon2Memory  = 64 * 1024 // KiB
	argon2Threads = 4
	argon2SaltLen = 16
	argon2KeyLen  = 32
)

// HashPIN derives an argon2id hash of pin using a fresh random salt and
// returns it in the standard PHC encoded form:
//
//	$argon2id$v=19$m=65536,t=1,p=4$<salt>$<hash>
//
// The encoded string carries everything VerifyPIN needs; nothing else
// is stored alongside a pin-kind alarm_codes row.
func HashPIN(pin string) (string, error) {
	salt := make([]byte, argon2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("codes: generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(pin), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argon2Memory, argon2Time, argon2Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPIN reports whether pin matches encoded, an argon2id hash
// produced by HashPIN. Comparison of the derived key against the
// stored key is constant-time; a malformed encoded value never
// matches. An empty encoded hash (non-pin code kinds) never matches.
func VerifyPIN(encoded, pin string) bool {
	if encoded == "" {
		return false
	}
	h, ok := decodePINHash(encoded)
	if !ok {
		return false
	}
	got := argon2.IDKey([]byte(pin), h.salt, h.time, h.memory, h.threads, uint32(len(h.key))) //nolint:gosec // G115: key length bound to a small hash size
	return subtle.ConstantTimeCompare(got, h.key) == 1
}

// decodedPINHash is the parsed form of a PHC-style argon2id encoding:
// the salt, the derived key, and the parameters used to derive it.
type decodedPINHash struct {
	salt, key    []byte
	time, memory uint32
	threads      uint8
}

// decodePINHash parses the PHC-style argon2id encoding HashPIN
// produces.
func decodePINHash(encoded string) (decodedPINHash, bool) {
	parts := strings.Split(encoded, "$")
	// parts[0] is the empty string before the leading '$'.
	if len(parts) != 6 || parts[1] != "argon2id" {
		return decodedPINHash{}, false
	}
	if !strings.HasPrefix(parts[2], "v=") {
		return decodedPINHash{}, false
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return decodedPINHash{}, false
	}
	m, mOK := parseParam(params[0], "m=")
	t, tOK := parseParam(params[1], "t=")
	p, pOK := parseParam(params[2], "p=")
	if !mOK || !tOK || !pOK || p == 0 || p > 255 {
		return decodedPINHash{}, false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return decodedPINHash{}, false
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return decodedPINHash{}, false
	}
	return decodedPINHash{salt: salt, key: key, time: t, memory: m, threads: uint8(p)}, true
}

// parseParam extracts the unsigned integer value of a "<prefix><n>"
// PHC parameter fragment (e.g. "m=65536").
func parseParam(field, prefix string) (uint32, bool) {
	if !strings.HasPrefix(field, prefix) {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimPrefix(field, prefix), 10, 32)
	if err != nil {
		return 0, false
	}
	return uint32(n), true
}
