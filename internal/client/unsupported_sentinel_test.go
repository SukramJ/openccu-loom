// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client

import (
	"errors"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// TestIsUnsupportedMatchesWrappedSentinels pins that the capability
// fallbacks recognise an unsupported-operation error that travelled up
// through a %w wrap. String comparison broke on the first wrap, turning a
// graceful capability skip into a hard error.
func TestIsUnsupportedMatchesWrappedSentinels(t *testing.T) {
	t.Parallel()

	for _, sentinel := range []error{backends.ErrUnsupported, hmerr.ErrUnsupported} {
		if !isUnsupported(sentinel) {
			t.Errorf("bare sentinel %v not recognised", sentinel)
		}
		wrapped := fmt.Errorf("getParamset: %w", sentinel)
		if !isUnsupported(wrapped) {
			t.Errorf("wrapped sentinel %v not recognised", wrapped)
		}
		twice := fmt.Errorf("fetch: %w", wrapped)
		if !isUnsupported(twice) {
			t.Errorf("doubly wrapped sentinel %v not recognised", twice)
		}
	}
	if isUnsupported(errors.New("backend: operation unsupported (lookalike)")) {
		t.Error("an unrelated error must not be treated as unsupported")
	}
	if isUnsupported(nil) {
		t.Error("nil must not be treated as unsupported")
	}
}
