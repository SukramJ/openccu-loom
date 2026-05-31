// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSnapshot_NDJSON_AcceptHeader(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", http.NoBody)
	req.Header.Set("Accept", "application/x-ndjson")
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{}).ServeHTTP(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type = %q, want application/x-ndjson", ct)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	lines := splitNDJSON(t, w.Body.Bytes())
	if len(lines) == 0 {
		t.Fatal("ndjson body must contain at least the meta line")
	}
	if lines[0]["kind"] != "meta" {
		t.Fatalf("first line must be meta, got kind=%v", lines[0]["kind"])
	}
	if _, ok := lines[0]["data"].(map[string]any)["generated_at"].(string); !ok {
		t.Fatal("meta line must carry generated_at string")
	}
}

func TestSnapshot_NDJSON_FallsBackWhenAcceptOmitted(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{}).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", http.NoBody))
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("legacy path content-type = %q", ct)
	}
}

func TestSnapshot_NDJSON_AcceptQValueIgnored(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot", http.NoBody)
	req.Header.Set("Accept", "application/x-ndjson; q=0.8, */*")
	w := httptest.NewRecorder()
	Snapshot(SnapshotDeps{}).ServeHTTP(w, req)
	if ct := w.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type = %q, want application/x-ndjson", ct)
	}
}

// splitNDJSON parses every non-empty line as JSON.
func splitNDJSON(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var out []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal(line, &m); err != nil {
			t.Fatalf("line not JSON: %v\n%s", err, line)
		}
		out = append(out, m)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanner: %v", err)
	}
	return out
}
