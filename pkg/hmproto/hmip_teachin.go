// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproto

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// hmipKeyBase32Alphabet is the 32-character alphabet printed on HmIP
// device labels. It omits D, I, O and V to avoid misreadings. Mirrors
// convertHmIPKeyBase32ToBase16 in the CCU WebUI bundle (webui.js).
const hmipKeyBase32Alphabet = "0123456789ABCEFGHJKLMNPQRSTUWXYZ"

// stripKeySeparators removes the dashes and spaces operators copy from a
// device label and uppercases the rest, mirroring the CCU WebUI's input
// normalisation before submission.
func stripKeySeparators(s string) string {
	return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(s))
}

// NormalizeSGTIN canonicalises an HmIP SGTIN as entered by an operator:
// dashes and spaces are stripped, the rest is uppercased and must be
// exactly 24 hex characters — the shape `Interface.setInstallModeHMIP`
// expects in its `address` parameter.
func NormalizeSGTIN(s string) (string, error) {
	v := stripKeySeparators(s)
	if len(v) != 24 {
		return "", fmt.Errorf("sgtin must be 24 hex characters, got %d", len(v))
	}
	if !isUpperHex(v) {
		return "", fmt.Errorf("sgtin contains non-hex characters")
	}
	return v, nil
}

// NormalizeHmIPKey canonicalises an HmIP device key: a 32-hex-character
// key passes through (uppercased, separators stripped); the shorter
// Base32 form printed on device labels is decoded with the CCU WebUI's
// exact algorithm — right-to-left 5-bit accumulation, flushed into a
// 16-byte buffer from the rightmost byte while more than 8 bits are
// pending, without a final flush — so the resulting key matches the CCU
// byte-for-byte, including the 0x00 lead-in bytes short labels produce.
func NormalizeHmIPKey(s string) (string, error) {
	v := stripKeySeparators(s)
	if v == "" {
		return "", fmt.Errorf("key is empty")
	}
	if len(v) >= 32 {
		if len(v) == 32 && isUpperHex(v) {
			return v, nil
		}
		return "", fmt.Errorf("key must be 32 hex characters or the shorter label form, got %d characters", len(v))
	}
	var buf [16]byte
	pos := len(buf) - 1
	value := 0
	bits := 0
	for i := len(v) - 1; i >= 0; i-- {
		idx := strings.IndexByte(hmipKeyBase32Alphabet, v[i])
		if idx < 0 {
			return "", fmt.Errorf("key contains invalid character %q", string(v[i]))
		}
		value |= idx << bits
		bits += 5
		for bits > 8 {
			if pos < 0 {
				return "", fmt.Errorf("key label form too long")
			}
			buf[pos] = byte(value)
			pos--
			value >>= 8
			bits -= 8
		}
	}
	return strings.ToUpper(hex.EncodeToString(buf[:])), nil
}

// isUpperHex reports whether v consists solely of 0-9 / A-F.
func isUpperHex(v string) bool {
	for i := 0; i < len(v); i++ {
		c := v[i]
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}
