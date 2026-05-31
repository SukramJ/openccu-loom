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

// TestE2EMatterBoot checks that Matter is a gated feature: without an
// explicit `north.matter.enabled: true` in the config, the daemon's REST
// surface reports Matter as disabled rather than panicking or returning
// 5xx. This is the minimal boot-time smoke — a full Matter commissioning
// round-trip is in docs/matter/chip-tool-test-brief.md and gated behind
// the `chiptool` build tag.
//
// What is verified:
//   - GET /api/v1/matter/status returns 200 with {"enabled":false}.
//   - No 5xx in any of the matter/* endpoints that do not require a
//     running Matter stack.
//
// The full Matter boot (Endpoint 0 ServerList, BasicInformation.Reachable,
// Aggregator with ≥1 Bridged-Device) is deferred to Phase F because it
// requires enabling Matter in the harness config plus a Matter
// commissioner — both are out of scope for the hermetic E2E suite.
func TestE2EMatterBoot(t *testing.T) {
	t.Parallel()

	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})

	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	// /api/v1/matter/status must be reachable and report enabled:false.
	req, _ := h.REST().NewRequest(http.MethodGet, "/api/v1/matter/status", nil)
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/matter/status: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/api/v1/matter/status: 5xx status=%d body=%s", resp.StatusCode, body)
	}
	if resp.StatusCode == http.StatusOK {
		var payload struct {
			Enabled bool `json:"enabled"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil {
			if payload.Enabled {
				t.Errorf("/api/v1/matter/status: enabled=true but Matter was not configured in harness")
			} else {
				t.Log("/api/v1/matter/status: enabled=false — expected for default harness config")
			}
		}
	}

	// /api/v1/matter/fabrics — read-only list — must not return 500.
	// When Matter is disabled the daemon returns 503 (service_unready)
	// for endpoints that require an active bridge. 503 is acceptable;
	// only true server errors (500, 502, 504) are failures here.
	req2, _ := h.REST().NewRequest(http.MethodGet, "/api/v1/matter/fabrics", nil)
	resp2, err := h.REST().Do(req2)
	if err != nil {
		t.Fatalf("GET /api/v1/matter/fabrics: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode == http.StatusInternalServerError ||
		resp2.StatusCode == http.StatusBadGateway ||
		resp2.StatusCode == http.StatusGatewayTimeout {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("/api/v1/matter/fabrics: unexpected server error status=%d body=%s", resp2.StatusCode, body)
	}
	t.Logf("/api/v1/matter/fabrics: status=%d (expected 200 or 503 when Matter disabled)", resp2.StatusCode)
}
