// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"
)

// TestBuildFullStack_ErrorAttrsRenderAsStrings pins the error-attr
// contract of the stdout log stack: an `"error", err` attribute must
// reach the JSON output as the error's message. The stdlib JSON
// handler special-cases error values already; this pin guards the
// full handler chain (hmreqctx → tee → redact → core) against any
// future wrapper or ReplaceAttr hook regressing it to a marshalled
// `"error": {}`.
func TestBuildFullStack_ErrorAttrsRenderAsStrings(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	stack := BuildFullStack(StackOptions{Writer: &buf, Format: FormatJSON}, slog.LevelInfo)

	wrapped := fmt.Errorf("install-mode: %w", errors.New("no backend for ccu/HmIP-RF"))
	stack.Logger.Error("Install mode write failed", "error", wrapped)

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("unmarshal log record: %v (raw: %s)", err, buf.String())
	}
	got, ok := rec["error"].(string)
	if !ok {
		t.Fatalf(`log "error" attr = %#v, want a string (never an empty object)`, rec["error"])
	}
	if got != wrapped.Error() {
		t.Fatalf(`log "error" attr = %q, want %q`, got, wrapped.Error())
	}
}

// TestBuildFullStack_NilErrorAttrStaysHarmless guards the replace hook
// against typed-nil error values inside the attr.
func TestBuildFullStack_NilErrorAttrStaysHarmless(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	stack := BuildFullStack(StackOptions{Writer: &buf, Format: FormatJSON}, slog.LevelInfo)

	var err error
	stack.Logger.Error("boom", "error", err)

	var rec map[string]any
	if uerr := json.Unmarshal(buf.Bytes(), &rec); uerr != nil {
		t.Fatalf("unmarshal log record: %v (raw: %s)", uerr, buf.String())
	}
}
