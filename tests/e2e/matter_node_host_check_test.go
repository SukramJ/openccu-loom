// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2ENodeRoutesAnswerUnderThePublicURLName boots the built daemon with
// north.rest.public_url set and asserts what the composition root hands
// go-fabric-ui's Host check: the node routes answer under the public URL's
// host name, and a name nobody configured is refused with 403
// host_not_allowed — go-fabric-ui's defence against DNS rebinding, which
// admits only loopback names and IP addresses on its own. Both directions
// matter: the first fails when the configured name never reaches the
// module (every Matter page behind a named proxy would answer 403), the
// second when the check is switched off.
func TestE2ENodeRoutesAnswerUnderThePublicURLName(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{PublicURL: "http://loom.example.test:8080"})

	status := func(host string) (int, string) {
		t.Helper()
		req, err := h.REST().NewRequest(http.MethodGet, "/api/v1/node/status", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Host = host
		// Basic credentials authenticate each request on its own,
		// whatever Host it names.
		req.Header.Set("Authorization", "Basic "+harness.BasicAuthHeader(harness.AdminUser, harness.AdminPass))
		resp, err := h.REST().Do(req)
		if err != nil {
			t.Fatalf("GET /api/v1/node/status (Host %s): %v", host, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	if code, body := status("loom.example.test:8080"); code != http.StatusOK {
		t.Errorf("Host loom.example.test (the public URL's name) = %d %s, want 200", code, body)
	}
	if code, body := status("rebound.example.test"); code != http.StatusForbidden || !strings.Contains(body, "host_not_allowed") {
		t.Errorf("Host rebound.example.test (configured nowhere) = %d %s, want 403 host_not_allowed", code, body)
	}
}
