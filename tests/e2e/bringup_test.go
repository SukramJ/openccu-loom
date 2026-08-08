// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestBringUpSmoke verifies that the harness can actually start the
// openccu-loom daemon as a sub-process, that /api/v1/health returns
// 200, and that /api/v1/info returns a JSON envelope. It is the
// minimum viable proof that step 2 of notes/testplans/e2e-testplan.md works
// end-to-end before any walker is built on top.
func TestBringUpSmoke(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})

	hc := h.REST().HTTPClient()

	// Health: must be 200.
	resp, err := hc.Get(h.RESTBase() + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /api/v1/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/api/v1/health: status=%d body=%s", resp.StatusCode, body)
	}

	// Info: must parse as JSON object with a non-empty version.
	resp2, err := hc.Get(h.RESTBase() + "/api/v1/info")
	if err != nil {
		t.Fatalf("GET /api/v1/info: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("/api/v1/info: status=%d body=%s", resp2.StatusCode, body)
	}
	var info map[string]any
	if err := json.NewDecoder(resp2.Body).Decode(&info); err != nil {
		t.Fatalf("decode /api/v1/info body: %v", err)
	}
	if v, _ := info["version"].(string); v == "" {
		t.Fatalf("/api/v1/info: empty version field, body=%v", info)
	}
}
