// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/configui"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// unencodableParamsetWriter rejects every write the way the south-bound
// transports do when a string parameter carries a rune ISO-8859-1 cannot
// represent.
type unencodableParamsetWriter struct{}

func (unencodableParamsetWriter) PutParamset(_ context.Context, _ configui.SessionKey, _ map[string]any) error {
	return fmt.Errorf("parameter %q: %w", "NAME", hmerr.ErrUnencodableString)
}

// TestParamsetPutUnencodableStringReturnsTypedCode pins that an
// unencodable-string rejection reaches the client as its own structured
// code rather than as a generic internal error, so the SPA can point at
// the offending field instead of showing a backend failure.
func TestParamsetPutUnencodableStringReturnsTypedCode(t *testing.T) {
	r := NewRouter()
	RegisterExtendedCommands(r, ExtendedCommandsConfig{Paramsets: unencodableParamsetWriter{}})

	raw, _ := json.Marshal(map[string]any{
		"channel_address": "ABC0001:1",
		"paramset_key":    "MASTER",
		"values":          map[string]any{"NAME": "Küche ✓"},
	})
	res := r.Dispatch(opCtx(), "paramset.put", raw)
	if res.Error == nil {
		t.Fatal("expected an error for an unencodable string, got nil")
	}
	if res.Error.Code != CommandErrorUnencodableString {
		t.Fatalf("error code = %q, want %q (message: %s)",
			res.Error.Code, CommandErrorUnencodableString, res.Error.Message)
	}
}
