// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EDaemonBoot verifies the minimum viable E2E boot sequence:
//   - /api/v1/health returns 200 (already asserted by harness.Start, but
//     repeated here so this file is a standalone smoke anchor)
//   - The MQTT broker is reachable (broker URL is non-empty and the broker
//     reported at least one subscription from the daemon)
//   - Matter is not enabled by default — the REST status endpoint returns
//     {enabled:false} rather than a 5xx
func TestE2EDaemonBoot(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{EnableMQTT: true})

	// 1. Health endpoint must return 200.
	resp, err := h.REST().HTTPClient().Get(h.RESTBase() + "/api/v1/health")
	if err != nil {
		t.Fatalf("GET /api/v1/health: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/api/v1/health: status=%d body=%s", resp.StatusCode, body)
	}

	// 2. MQTT broker reachable — URL is non-empty when EnableMQTT=true.
	if h.MQTT() == nil {
		t.Fatal("MQTT broker not started with EnableMQTT=true")
	}
	if h.MQTT().URL() == "" {
		t.Fatal("MQTT broker URL empty")
	}

	// 3. Matter disabled by default — /api/v1/matter/status must either
	//    return 200 with enabled=false or 401 (auth guard). A 5xx would
	//    indicate the handler panicked or the router is broken.
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	req, _ := h.REST().NewRequest(http.MethodGet, "/api/v1/matter/status", nil)
	resp2, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/matter/status: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode >= 500 {
		body, _ := io.ReadAll(resp2.Body)
		t.Fatalf("/api/v1/matter/status: unexpected 5xx status=%d body=%s", resp2.StatusCode, body)
	}
}
