// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// buildPayload wraps a set of context-tagged fields inside an anonymous
// Structure and returns the resulting wire bytes. The caller supplies a
// function that writes each field into the encoder; buildPayload opens
// the struct, calls fields, closes it, and asserts Bytes succeeds.
func buildPayload(t *testing.T, fields func(e *tlv.Encoder)) []byte {
	t.Helper()
	e := tlv.NewEncoder()
	e.StartStruct(tlv.AnonymousTag())
	fields(e)
	if err := e.EndContainer(); err != nil {
		t.Fatalf("buildPayload EndContainer: %v", err)
	}
	b, err := e.Bytes()
	if err != nil {
		t.Fatalf("buildPayload Bytes: %v", err)
	}
	return b
}
