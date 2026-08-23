// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/reqctx"
)

// TestLogger_AccessLogHasNoDuplicateRequestIDKey builds the same middleware
// chain the router mounts in production — [RequestID], [ReqContextWithCentral],
// then [Logger], with the logger backed by [reqctx.ContextHandler] the way
// [pkg/hmlog] wires it — and serves one request through it.
//
// [reqctx.ContextHandler] injects "request_id" into every record that
// carries a [reqctx.RequestContext], which [ReqContextWithCentral] installs
// before [Logger] runs. [Logger] must not also add its own "request_id"
// attribute: doing so emits a JSON object with the same key twice, which
// some decoders reject outright and others resolve inconsistently — a
// log pipeline can silently drop the daemon's entire access log over it.
//
// json.Unmarshal into a map hides the duplicate (the second write just
// overwrites the first in memory), so this test also counts raw
// occurrences of the literal key in the encoded bytes.
func TestLogger_AccessLogHasNoDuplicateRequestIDKey(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	jsonHandler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(reqctx.NewContextHandler(jsonHandler))

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := RequestID(ReqContextWithCentral("")(Logger(logger)(inner)))

	req := httptest.NewRequest(http.MethodGet, "/test", http.NoBody)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("expected one access-log line, got none")
	}

	if n := strings.Count(line, `"request_id"`); n != 1 {
		t.Fatalf("access-log line has %d occurrences of the \"request_id\" key, want 1: %s", n, line)
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(line), &decoded); err != nil {
		t.Fatalf("access-log line is not valid JSON: %v (%s)", err, line)
	}
	if id, _ := decoded["request_id"].(string); id == "" {
		t.Fatal("expected a non-empty request_id in the decoded access-log line")
	}
}
