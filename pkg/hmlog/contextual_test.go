// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func newTestLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func TestNewWithFieldsAppendsToRecords(t *testing.T) {
	var buf bytes.Buffer
	base := newTestLogger(&buf)
	cl := New(base, Fields{"central": "ccu1", "iface": "HmIP-RF"})
	cl.Info("test-message")
	out := buf.String()
	if !strings.Contains(out, "central=ccu1") {
		t.Errorf("expected 'central=ccu1' in log output, got: %s", out)
	}
	if !strings.Contains(out, "iface=HmIP-RF") {
		t.Errorf("expected 'iface=HmIP-RF' in log output, got: %s", out)
	}
	if !strings.Contains(out, "test-message") {
		t.Errorf("expected message in log output, got: %s", out)
	}
}

func TestNewNilBaseUsesDefault(t *testing.T) {
	// Should not panic.
	cl := New(nil, Fields{"key": "val"})
	if cl == nil {
		t.Fatal("New(nil, fields) returned nil")
	}
	if cl.Logger() == nil {
		t.Fatal("Logger() returned nil")
	}
}

func TestWithContextAndGet(t *testing.T) {
	var buf bytes.Buffer
	base := newTestLogger(&buf)
	ctx := context.Background()
	ctx = WithContext(ctx, Fields{"session": "abc123"})
	cl := Get(ctx, base)
	cl.Info("ctx-message")
	out := buf.String()
	if !strings.Contains(out, "session=abc123") {
		t.Errorf("expected 'session=abc123' in log output, got: %s", out)
	}
}

func TestWithContextMergesFields(t *testing.T) {
	ctx := context.Background()
	ctx = WithContext(ctx, Fields{"a": "1"})
	ctx = WithContext(ctx, Fields{"b": "2"})
	f, ok := ctx.Value(contextKey{}).(Fields)
	if !ok {
		t.Fatal("context has no Fields")
	}
	if f["a"] != "1" || f["b"] != "2" {
		t.Errorf("merged fields = %v, want a=1 b=2", f)
	}
}

func TestWithDerivesChildLogger(t *testing.T) {
	var buf bytes.Buffer
	base := newTestLogger(&buf)
	cl := New(base, Fields{"central": "x"})
	child := cl.With(Fields{"method": "init"})
	child.Debug("child-log")
	out := buf.String()
	if !strings.Contains(out, "central=x") {
		t.Errorf("expected parent field in child output, got: %s", out)
	}
	if !strings.Contains(out, "method=init") {
		t.Errorf("expected child field in output, got: %s", out)
	}
}

func TestGetWithNoContextFields(t *testing.T) {
	var buf bytes.Buffer
	base := newTestLogger(&buf)
	cl := Get(context.Background(), base)
	cl.Info("no-fields")
	out := buf.String()
	if !strings.Contains(out, "no-fields") {
		t.Errorf("expected message in output, got: %s", out)
	}
}

func TestWithEmptyFieldsIsNoop(t *testing.T) {
	var buf bytes.Buffer
	base := newTestLogger(&buf)
	cl := New(base, Fields{"k": "v"})
	same := cl.With(nil)
	if same != cl {
		t.Error("With(nil) should return same instance")
	}
}
