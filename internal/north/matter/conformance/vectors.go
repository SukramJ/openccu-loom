// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package conformance

import (
	"bytes"
	"fmt"
	"testing"
)

// Vector is one golden round-trip pinned to fixed bytes. The Decode
// function consumes Wire and produces a typed value; Encode rebuilds
// the wire form from that value. The framework asserts byte-equality
// in both directions, so a drifting codec fails deterministically
// regardless of which side broke.
type Vector struct {
	// Name is the test sub-name for go test output.
	Name string
	// Wire is the canonical byte sequence.
	Wire []byte
	// Decode parses Wire into a typed value. May return an error;
	// the framework fails the test on any non-nil return.
	Decode func(data []byte) (any, error)
	// Encode rebuilds Wire from the typed value. May return an
	// error; the framework fails on any non-nil return.
	Encode func(value any) ([]byte, error)
}

// RunVectorSet executes every vector in vs as a t.Run sub-test.
// Round-trip semantics:
//
//  1. value, err := v.Decode(v.Wire)
//  2. emitted, err := v.Encode(value)
//  3. require bytes.Equal(emitted, v.Wire)
//
// Codecs that produce non-canonical output (e.g. encoders that emit
// fields in input order rather than spec order) need to be made
// canonical before they enter the vector set.
func RunVectorSet(t *testing.T, vs []Vector) {
	t.Helper()
	for _, v := range vs {
		t.Run(v.Name, func(t *testing.T) {
			t.Parallel()
			value, err := v.Decode(v.Wire)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			emitted, err := v.Encode(value)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if !bytes.Equal(emitted, v.Wire) {
				t.Fatalf("round-trip mismatch:\n  want %s\n  got  %s",
					hexDump(v.Wire), hexDump(emitted))
			}
		})
	}
}

// hexDump returns a compact hex representation for diff-friendly
// error messages.
func hexDump(b []byte) string {
	if len(b) == 0 {
		return "<empty>"
	}
	out := make([]byte, 0, len(b)*3)
	for i, by := range b {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, hexNibble(by>>4), hexNibble(by&0x0F))
	}
	return string(out)
}

func hexNibble(n byte) byte {
	if n < 10 {
		return '0' + n
	}
	return 'A' + (n - 10)
}

// MustHex parses a hex literal at test-construction time and panics
// on error. Used for static vector tables — failure means the
// developer typed something unparseable.
func MustHex(s string) []byte {
	out := make([]byte, 0, len(s)/2)
	var nibble byte
	high := true
	for i := range len(s) {
		c := s[i]
		if c == ' ' || c == '\n' || c == '\t' {
			continue
		}
		v, ok := hexParse(c)
		if !ok {
			panic(fmt.Sprintf("conformance: MustHex: invalid char %q", c))
		}
		if high {
			nibble = v << 4
			high = false
		} else {
			out = append(out, nibble|v)
			high = true
		}
	}
	if !high {
		panic("conformance: MustHex: odd nibble count")
	}
	return out
}

func hexParse(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
}
