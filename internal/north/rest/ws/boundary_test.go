// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmreqctx"
)

// TestRouterBoundaryEnrichesContext verifies that Dispatch installs a
// [hmreqctx.RequestContext] tagged with the WS operation prefix and the
// configured central name before the handler runs. Mirrors the REST
// ReqContext middleware so log aggregation across both transports
// uses the same shape (audit O13).
func TestRouterBoundaryEnrichesContext(t *testing.T) {
	r := NewRouter()
	r.SetBoundary(slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), "ccu-99")

	var seen hmreqctx.RequestContext
	var ok bool
	r.Register("test.cmd", func(ctx context.Context, _ json.RawMessage) (any, error) {
		seen, ok = hmreqctx.FromContext(ctx)
		return "ok", nil
	})

	res := r.Dispatch(context.Background(), "test.cmd", nil)
	if res.Error != nil {
		t.Fatalf("Dispatch error: %v", res.Error)
	}
	if !ok {
		t.Fatal("RequestContext missing in handler ctx")
	}
	if seen.Operation != "ws.command:test.cmd" {
		t.Errorf("Operation=%q want ws.command:test.cmd", seen.Operation)
	}
	if seen.CentralName != "ccu-99" {
		t.Errorf("CentralName=%q want ccu-99", seen.CentralName)
	}
	if seen.StartedAt.IsZero() {
		t.Error("StartedAt was not populated")
	}
}

// TestRouterBoundaryLogsOutcomes verifies that one structured slog
// record is emitted per dispatch, with `command`, `status`, and
// `elapsed` fields. Failed commands log at warn level and include
// `error_code`; success logs at debug with `status=ok`.
func TestRouterBoundaryLogsOutcomes(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r := NewRouter()
	r.SetBoundary(logger, "ccu-7")
	r.Register("ok.cmd", func(_ context.Context, _ json.RawMessage) (any, error) {
		return map[string]any{"hello": "world"}, nil
	})
	r.Register("err.cmd", func(_ context.Context, _ json.RawMessage) (any, error) {
		return nil, errors.New("upstream timeout")
	})

	if res := r.Dispatch(context.Background(), "ok.cmd", nil); res.Error != nil {
		t.Fatalf("ok.cmd unexpected error: %v", res.Error)
	}
	if res := r.Dispatch(context.Background(), "err.cmd", nil); res.Error == nil {
		t.Fatal("err.cmd missing error")
	}
	if res := r.Dispatch(context.Background(), "missing.cmd", nil); res.Error == nil ||
		res.Error.Code != CommandErrorUnknownCommand {
		t.Fatalf("missing.cmd: %+v", res.Error)
	}

	out := buf.String()
	if !strings.Contains(out, "command=ok.cmd") || !strings.Contains(out, "status=ok") ||
		!strings.Contains(out, "level=DEBUG") {
		t.Errorf("ok.cmd log missing expected fields (debug-level success): %q", out)
	}
	if !strings.Contains(out, "command=err.cmd") ||
		!strings.Contains(out, "error_code=internal_error") ||
		!strings.Contains(out, "level=WARN") {
		t.Errorf("err.cmd log missing expected fields: %q", out)
	}
	if !strings.Contains(out, "command=missing.cmd") ||
		!strings.Contains(out, "error_code=unknown_command") {
		t.Errorf("unknown.cmd log missing expected fields: %q", out)
	}
	if !strings.Contains(out, "central_name=ccu-7") {
		t.Errorf("central_name not propagated: %q", out)
	}
}

// TestRouterBoundaryNoLoggerSilent verifies the historical behaviour
// when [SetBoundary] has not been called: Dispatch still works, but
// no slog records are emitted (tests that don't wire a boundary stay
// silent so they don't pollute test output).
func TestRouterBoundaryNoLoggerSilent(t *testing.T) {
	r := NewRouter()
	r.Register("plain.cmd", func(_ context.Context, _ json.RawMessage) (any, error) {
		return "plain", nil
	})
	res := r.Dispatch(context.Background(), "plain.cmd", nil)
	if res.Error != nil {
		t.Fatalf("plain.cmd error: %v", res.Error)
	}
	if got, ok := res.Data.(string); !ok || got != "plain" {
		t.Fatalf("plain.cmd data=%v", res.Data)
	}
}
