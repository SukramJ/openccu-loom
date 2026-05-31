// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmerr_test

import (
	"bytes"
	"errors"
	"log/slog"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// --------------------------------------------------------------------------
// LogBoundaryError — smoke tests
// --------------------------------------------------------------------------

func TestLogBoundaryError_NilErr_NoLog(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	hmerr.LogBoundaryError(logger, "rpc.init", "connect", nil, hmerr.BoundaryLevelAuto, nil, "")
	if buf.Len() != 0 {
		t.Errorf("expected no output for nil error, got: %s", buf.String())
	}
}

func TestLogBoundaryError_DomainError_WarnLevel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hmerr.LogBoundaryError(logger, "central", "init", hmerr.ErrNoConnection, hmerr.BoundaryLevelAuto, nil, "")
	if buf.Len() == 0 {
		t.Error("expected log output, got none")
	}
	if !containsAny(buf.String(), "WARN", "error_boundary") {
		t.Errorf("expected WARN level log, got: %s", buf.String())
	}
}

func TestLogBoundaryError_UnknownError_ErrorLevel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hmerr.LogBoundaryError(logger, "central", "init", errors.New("unexpected"), hmerr.BoundaryLevelAuto, nil, "")
	if buf.Len() == 0 {
		t.Error("expected log output, got none")
	}
	if !containsAny(buf.String(), "ERROR") {
		t.Errorf("expected ERROR level log, got: %s", buf.String())
	}
}

func TestLogBoundaryError_OverrideLevel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hmerr.LogBoundaryError(logger, "rpc", "ping", hmerr.ErrAuthFailure, slog.LevelInfo, nil, "custom message")
	if buf.Len() == 0 {
		t.Error("expected log output, got none")
	}
	if !containsAny(buf.String(), "INFO") {
		t.Errorf("expected INFO level log, got: %s", buf.String())
	}
}

func TestLogBoundaryError_NilLogger_NoPanic(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("LogBoundaryError panicked: %v", r)
		}
	}()
	hmerr.LogBoundaryError(nil, "boundary", "action", errors.New("err"), hmerr.BoundaryLevelAuto, nil, "")
}

func TestLogBoundaryError_ContextRedaction(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := map[string]any{
		"password": "secret123",
		"device":   "ABC001",
	}
	hmerr.LogBoundaryError(logger, "b", "a", errors.New("err"), hmerr.BoundaryLevelAuto, ctx, "")
	out := buf.String()
	if containsAny(out, "secret123") {
		t.Errorf("sensitive value leaked in log: %s", out)
	}
	if !containsAny(out, "***") {
		t.Errorf("expected redacted value '***' in log: %s", out)
	}
}

func TestLogBoundaryError_MessageAppended(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hmerr.LogBoundaryError(logger, "b", "a", errors.New("err"), slog.LevelWarn, nil, "extra context here")
	if !containsAny(buf.String(), "extra context here") {
		t.Errorf("expected message in log output, got: %s", buf.String())
	}
}

// --------------------------------------------------------------------------
// helpers
// --------------------------------------------------------------------------

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if s != "" && sub != "" {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
