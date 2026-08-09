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

// TestConfigUIURLReachesInfo pins that a configured public URL actually
// arrives at `GET /api/v1/info`.
//
// Black-box against the built binary on purpose. The handler test proves
// only that Info renders the string it is handed; whether anything hands
// it one is a property of the composition root, and a test that assembles
// the router itself gets to pass the value in and would stay green with
// the wiring removed. Here the only input is a YAML key.
//
// The consumer is a client that wants to link a person at this daemon's
// UI — the Home Assistant integration above all, which can reach the
// daemon over a container network at an address no browser can follow.
func TestConfigUIURLReachesInfo(t *testing.T) {
	t.Parallel()

	const publicURL = "https://loom.example.de"

	t.Run("configured", func(t *testing.T) {
		t.Parallel()
		h := harness.Start(t, harness.Options{PublicURL: publicURL})
		// The SPA mount is appended by the daemon, not by the operator:
		// public_url names an origin, and a client that had to know where
		// the UI is mounted would break on the next mount change.
		if got := infoField(t, h, "config_ui_url"); got != publicURL+"/app/" {
			t.Errorf("config_ui_url = %q, want %q", got, publicURL+"/app/")
		}
	})

	t.Run("not configured", func(t *testing.T) {
		t.Parallel()
		h := harness.Start(t, harness.Options{})
		// Empty rather than a guess: the client's fallback is its own
		// connection address, which it knows and the daemon does not.
		if got := infoField(t, h, "config_ui_url"); got != "" {
			t.Errorf("config_ui_url = %q, want empty", got)
		}
	})
}

// infoField fetches GET /info and returns one string field.
func infoField(t *testing.T, h *harness.Harness, key string) string {
	t.Helper()
	resp, err := h.REST().HTTPClient().Get(h.RESTBase() + "/api/v1/info")
	if err != nil {
		t.Fatalf("GET /api/v1/info: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/api/v1/info: status=%d body=%s", resp.StatusCode, body)
	}
	var info map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode /api/v1/info: %v", err)
	}
	v, ok := info[key]
	if !ok {
		t.Fatalf("/api/v1/info has no %q field: %v", key, info)
	}
	s, _ := v.(string)
	return s
}
