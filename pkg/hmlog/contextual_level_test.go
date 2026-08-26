// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmlog

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestWarnLogsAtWarnLevel verifies Warn produces output even at the
// default log level when the handler is set to LevelDebug.
func TestWarnLogsAtWarnLevel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cl := New(newTestLogger(&buf), Fields{"pkg": "hmlog"})
	cl.Warn("warn-message")
	out := buf.String()
	if !strings.Contains(out, "warn-message") {
		t.Errorf("Warn did not appear in output: %s", out)
	}
	if !strings.Contains(out, "WARN") {
		t.Errorf("expected WARN level in output: %s", out)
	}
}

// TestErrorLogsAtErrorLevel verifies Error produces output.
func TestErrorLogsAtErrorLevel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cl := New(newTestLogger(&buf), Fields{"pkg": "hmlog"})
	cl.Error("error-message")
	out := buf.String()
	if !strings.Contains(out, "error-message") {
		t.Errorf("Error did not appear in output: %s", out)
	}
	if !strings.Contains(out, "ERROR") {
		t.Errorf("expected ERROR level in output: %s", out)
	}
}

// TestDebugContextLogsWithContext verifies DebugContext passes through
// the context and emits the message.
func TestDebugContextLogsWithContext(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cl := New(newTestLogger(&buf), Fields{"pkg": "hmlog"})
	cl.DebugContext(context.Background(), "debug-ctx-message")
	out := buf.String()
	if !strings.Contains(out, "debug-ctx-message") {
		t.Errorf("DebugContext did not appear in output: %s", out)
	}
}

// TestInfoContextLogsWithContext verifies InfoContext works correctly.
func TestInfoContextLogsWithContext(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cl := New(newTestLogger(&buf), Fields{"pkg": "hmlog"})
	cl.InfoContext(context.Background(), "info-ctx-message")
	out := buf.String()
	if !strings.Contains(out, "info-ctx-message") {
		t.Errorf("InfoContext did not appear in output: %s", out)
	}
}

// TestWarnContextLogsWithContext verifies WarnContext works correctly.
func TestWarnContextLogsWithContext(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cl := New(newTestLogger(&buf), Fields{"pkg": "hmlog"})
	cl.WarnContext(context.Background(), "warn-ctx-message")
	out := buf.String()
	if !strings.Contains(out, "warn-ctx-message") {
		t.Errorf("WarnContext did not appear in output: %s", out)
	}
}

// TestErrorContextLogsWithContext verifies ErrorContext works correctly.
func TestErrorContextLogsWithContext(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cl := New(newTestLogger(&buf), Fields{"pkg": "hmlog"})
	cl.ErrorContext(context.Background(), "error-ctx-message")
	out := buf.String()
	if !strings.Contains(out, "error-ctx-message") {
		t.Errorf("ErrorContext did not appear in output: %s", out)
	}
}

// TestContextLevelMethodsCarryFields confirms the contextual fields set
// via New are present in every Context-variant log call.
func TestContextLevelMethodsCarryFields(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	cl := New(newTestLogger(&buf), Fields{"central": "ccu2"})
	ctx := context.Background()

	cl.DebugContext(ctx, "d")
	cl.InfoContext(ctx, "i")
	cl.WarnContext(ctx, "w")
	cl.ErrorContext(ctx, "e")

	out := buf.String()
	for _, sub := range []string{"central=ccu2"} {
		if count := strings.Count(out, sub); count < 4 {
			t.Errorf("expected field %q in all 4 context calls, found %d occurrences in:\n%s", sub, count, out)
		}
	}
}

// TestGetNilBaseUsesDefault mirrors TestNewNilBaseUsesDefault for Get.
func TestGetNilBaseUsesDefault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ctx = WithContext(ctx, Fields{"x": "y"})
	cl := Get(ctx, nil) // nil base → slog.Default()
	if cl == nil {
		t.Fatal("Get(ctx, nil) returned nil")
	}
	if cl.Logger() == nil {
		t.Fatal("Logger() returned nil")
	}
}

// TestWithContextEmptyFieldsIsNoop checks that an empty Fields map
// does not mutate the context.
func TestWithContextEmptyFieldsIsNoop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ctx2 := WithContext(ctx, nil)
	if ctx2 != ctx {
		t.Error("WithContext(ctx, nil) must return original context unchanged")
	}
}
