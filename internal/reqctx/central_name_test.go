// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package reqctx_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/reqctx"
)

// TestWithCentralName verifies the RequestContext.WithCentralName copy method.
func TestWithCentralName(t *testing.T) {
	t.Parallel()

	base := reqctx.RequestContext{
		RequestID: "r1",
		Operation: "op",
		StartedAt: time.Now(),
	}
	enriched := base.WithCentralName("ccu-primary")

	if enriched.CentralName != "ccu-primary" {
		t.Errorf("CentralName = %q, want %q", enriched.CentralName, "ccu-primary")
	}
	// Original must be unchanged.
	if base.CentralName != "" {
		t.Error("base RequestContext was mutated")
	}
}

// TestContextHandlerWithAttrsAndWithGroup exercises the WithAttrs and
// WithGroup delegation paths on ContextHandler (currently 0 % covered).
func TestContextHandlerWithAttrsAndWithGroup(t *testing.T) {
	t.Parallel()

	inner := slog.NewTextHandler(&discardWriter{}, nil)
	h := reqctx.NewContextHandler(inner)

	// WithAttrs must return a non-nil slog.Handler that is still a *ContextHandler
	// (or at minimum satisfies the interface).
	ha := h.WithAttrs([]slog.Attr{slog.String("k", "v")})
	if ha == nil {
		t.Fatal("WithAttrs returned nil")
	}
	if !ha.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("WithAttrs result is not enabled for Info level")
	}

	// WithGroup must return a non-nil slog.Handler.
	hg := h.WithGroup("grp")
	if hg == nil {
		t.Fatal("WithGroup returned nil")
	}
}

// discardWriter implements io.Writer that throws everything away.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
