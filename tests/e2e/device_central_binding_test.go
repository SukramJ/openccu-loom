// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestDeviceRouteCentralBindingThroughTheDaemon pins the composition root's
// half of the `?central=` binding: the built daemon hands the device-route
// guard a locator. Every router-level test injects its own; a daemon that
// never wired one answers 503 to every request naming a central, which only a
// run of the real binary shows.
func TestDeviceRouteCentralBindingThroughTheDaemon(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	devices := getJSONArray(t, h, "/api/v1/devices", "items")
	first, _ := devices[0].(map[string]any)
	addr, _ := first["address"].(string)
	central, _ := first["central"].(string)
	if addr == "" || central == "" {
		t.Fatalf("no device address or central in %v", devices[0])
	}

	get := func(query string) (int, string) {
		t.Helper()
		req, err := h.REST().NewRequest(http.MethodGet, "/api/v1/devices/"+addr+query, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := h.REST().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", query, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if status, body := get("?central=" + central); status != http.StatusOK {
		t.Errorf("?central=%s: status %d body %.200s, want 200", central, status, body)
	}
	if status, body := get("?central=no-such-central"); status != http.StatusNotFound {
		t.Errorf("?central=no-such-central: status %d body %.200s, want 404 from the guard", status, body)
	}
}
