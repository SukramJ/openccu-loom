// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EColdBootGatesUntilCCUReady is the end-to-end guard for the
// readiness-gated startup: when the daemon co-boots with a CCU that is still
// warming up (JSON-RPC 503, /ise/checkrega.cgi != "OK"), the per-central
// southbound bring-up must WAIT — no half-loaded, unnamed devices — and only
// once the CCU reports ready do devices appear, each carrying its CCU-assigned
// name (names load together with the devices, not after a restart).
//
// godevccu boots not-ready (harness.Options.StartCCUNotReady); the test flips
// it live via h.CCU().V().SetReady(true) and observes the daemon through the
// black-box REST surface only.
func TestE2EColdBootGatesUntilCCUReady(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{StartCCUNotReady: true})

	// The north-bound surface is up immediately (Start already waited for
	// /api/v1/health == 200) even though the CCU is not ready — that is the
	// whole point of gating only the southbound bring-up.
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	// 1. While the CCU is still booting the gate holds: zero devices. Settle
	//    briefly and confirm it STAYS empty (a slow load would trickle in).
	time.Sleep(3 * time.Second)
	if items := deviceItems(t, h); len(items) != 0 {
		t.Fatalf("expected 0 devices while the CCU is not ready, got %d — the readiness gate did not hold", len(items))
	}

	// 2. The CCU finishes booting: checkrega flips to "OK".
	h.CCU().V().SetReady(true)

	// 3. The gate unblocks and the central brings itself up — hub names first,
	//    then the device load — so devices appear already named. Poll the REST
	//    surface until they show up.
	deadline := time.Now().Add(45 * time.Second)
	var items []map[string]any
	for time.Now().Before(deadline) {
		if items = deviceItems(t, h); len(items) > 0 {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if len(items) == 0 {
		t.Fatal("no devices appeared after the CCU became ready — gated bring-up did not run")
	}

	// 4. Every device must carry a non-empty name. An empty name is exactly the
	//    cold-boot regression this gate exists to prevent (devices loaded before
	//    Device.listAllDetail names were available).
	for _, d := range items {
		addr, _ := d["address"].(string)
		name, _ := d["name"].(string)
		if strings.TrimSpace(name) == "" {
			t.Errorf("device %q has an empty name — names must load together with the device", addr)
		}
	}
}

// deviceItems GETs /api/v1/devices and returns the `items` array. Fails the
// test on transport / decode errors or a non-200 status.
func deviceItems(t *testing.T, h *harness.Harness) []map[string]any {
	t.Helper()
	req, err := h.REST().NewRequest(http.MethodGet, "/api/v1/devices", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/devices: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /api/v1/devices: status=%d body=%s", resp.StatusCode, body)
	}
	var out struct {
		Items []map[string]any `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode devices: %v", err)
	}
	return out.Items
}
