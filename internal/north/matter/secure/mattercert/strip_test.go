// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mattercert

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

func TestStripSignature_RemovesSigField(t *testing.T) {
	t.Parallel()
	// Build a minimal cert with a dummy 64-byte signature.
	opts := makeRootCertOpts()
	opts.signature = make([]byte, 64)
	raw := buildTestCert(t, opts)

	tbs, err := stripSignature(raw)
	if err != nil {
		t.Fatalf("stripSignature: %v", err)
	}

	// TBS must be strictly shorter than the full encoding.
	if len(tbs) >= len(raw) {
		t.Errorf("TBS length %d >= raw length %d; signature was not stripped", len(tbs), len(raw))
	}

	// Decoding the TBS as a cert must fail with ErrMalformed (signature absent).
	_, err = Decode(tbs)
	if !errors.Is(err, ErrMalformed) {
		t.Errorf("Decode(TBS) expected ErrMalformed (no signature), got %v", err)
	}
}

func TestStripSignature_ErrorWhenSignatureAbsent(t *testing.T) {
	t.Parallel()
	opts := makeRootCertOpts()
	opts.omitSignature = true
	raw := buildTestCert(t, opts)

	_, err := stripSignature(raw)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed when signature absent, got %v", err)
	}
}

func TestStripSignature_TruncatedInput(t *testing.T) {
	t.Parallel()
	// A completely empty buffer must fail.
	_, err := stripSignature([]byte{})
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}

func TestStripSignature_NonStructureTop(t *testing.T) {
	t.Parallel()
	e := tlv.NewEncoder()
	e.PutUint(tlv.AnonymousTag(), 1)
	raw, _ := e.Bytes()
	_, err := stripSignature(raw)
	if !errors.Is(err, ErrMalformed) {
		t.Fatalf("expected ErrMalformed for non-structure top, got %v", err)
	}
}
