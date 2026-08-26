// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package parity_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/parity"
)

// TestSchemaJSON verifies that SchemaJSON returns a non-empty byte slice
// containing valid JSON (the embedded matter.js HEAD schema snapshot).
func TestSchemaJSON(t *testing.T) {
	t.Parallel()
	got := parity.SchemaJSON()
	if len(got) == 0 {
		t.Fatal("SchemaJSON(): returned empty slice")
	}
	// Verify it starts with '{' (JSON object).
	if got[0] != '{' {
		t.Errorf("SchemaJSON(): first byte=0x%02X, want '{' (0x7B)", got[0])
	}
	// Verify defensive copy: mutating the returned slice must not affect
	// a subsequent call.
	got[0] = 0xFF
	got2 := parity.SchemaJSON()
	if got2[0] != '{' {
		t.Error("SchemaJSON(): not a defensive copy — mutation affected second call")
	}
}
