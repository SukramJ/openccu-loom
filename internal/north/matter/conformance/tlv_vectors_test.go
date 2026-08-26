// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package conformance_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/conformance"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestTLVCoreVectors pins the Matter TLV codec to a small set of
// fixed byte sequences derived from Matter Core Spec §A.7 (Appendix
// A.7 Encoding Examples). Each vector exercises a different element
// shape. Failure indicates a wire-codec drift the Sonnet round-trip
// tests in tlv_test.go cannot catch by themselves.
func TestTLVCoreVectors(t *testing.T) {
	t.Parallel()
	conformance.RunVectorSet(t, []conformance.Vector{
		{
			// Boolean false (no tag, control byte 0x08).
			Name: "BoolFalse_anonymous",
			Wire: conformance.MustHex("08"),
			Decode: func(data []byte) (any, error) {
				d := tlv.NewDecoder(data)
				return d.Next()
			},
			Encode: func(value any) ([]byte, error) {
				e := tlv.NewEncoder()
				e.PutBool(tlv.AnonymousTag(), false)
				return e.Bytes()
			},
		},
		{
			// Boolean true (no tag, control byte 0x09).
			Name: "BoolTrue_anonymous",
			Wire: conformance.MustHex("09"),
			Decode: func(data []byte) (any, error) {
				d := tlv.NewDecoder(data)
				return d.Next()
			},
			Encode: func(value any) ([]byte, error) {
				e := tlv.NewEncoder()
				e.PutBool(tlv.AnonymousTag(), true)
				return e.Bytes()
			},
		},
		{
			// Unsigned int 1-byte = 42 with anonymous tag (0x04 0x2A).
			Name: "Uint1_anonymous_42",
			Wire: conformance.MustHex("04 2A"),
			Decode: func(data []byte) (any, error) {
				d := tlv.NewDecoder(data)
				el, err := d.Next()
				if err != nil {
					return nil, err
				}
				if el.Type != tlv.TypeUnsignedInt1 {
					return nil, fmt.Errorf("expected uint1, got 0x%02X", el.Type)
				}
				return el.Uint, nil
			},
			Encode: func(value any) ([]byte, error) {
				v, ok := value.(uint64)
				if !ok {
					return nil, errors.New("expected uint64")
				}
				e := tlv.NewEncoder()
				e.PutUint(tlv.AnonymousTag(), v)
				return e.Bytes()
			},
		},
		{
			// Empty UTF-8 string with context-tag 1 (0x2C 0x01 0x00).
			Name: "UTF8Str_context1_empty",
			Wire: conformance.MustHex("2C 01 00"),
			Decode: func(data []byte) (any, error) {
				d := tlv.NewDecoder(data)
				el, err := d.Next()
				if err != nil {
					return nil, err
				}
				if el.Type != tlv.TypeUTF8Str1 {
					return nil, fmt.Errorf("expected utf8str1, got 0x%02X", el.Type)
				}
				return el.String, nil
			},
			Encode: func(value any) ([]byte, error) {
				s, ok := value.(string)
				if !ok {
					return nil, errors.New("expected string")
				}
				e := tlv.NewEncoder()
				e.PutUTF8(tlv.ContextTag(1), s)
				return e.Bytes()
			},
		},
		{
			// Null with context-tag 2 (0x34 0x02).
			Name: "Null_context2",
			Wire: conformance.MustHex("34 02"),
			Decode: func(data []byte) (any, error) {
				d := tlv.NewDecoder(data)
				return d.Next()
			},
			Encode: func(value any) ([]byte, error) {
				e := tlv.NewEncoder()
				e.PutNull(tlv.ContextTag(2))
				return e.Bytes()
			},
		},
	})
}
