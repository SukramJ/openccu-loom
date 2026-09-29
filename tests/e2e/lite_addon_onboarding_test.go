// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestLiteAddonAutoOnboardingFirstBoot pins ADR 0077 through the built
// binary: a daemon that boots with an empty central list on a host that
// looks like an openccu-lite box (VARIANT=lite marker, a minted add-on
// token file) adopts the local box by itself — the composition root's
// call, the persist-then-adopt path and the bring-up included, proven by
// the box's devices arriving over the lite API with nobody ever pasting
// a token.
//
// Not parallel: the daemon child inherits the test process environment,
// which t.Setenv steers.
func TestLiteAddonAutoOnboardingFirstBoot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	box, err := litefake.Start(ctx, litefake.Options{})
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = box.Close() })

	dir := t.TempDir()
	version := filepath.Join(dir, "VERSION")
	if err := os.WriteFile(version, []byte("VERSION=1.0.0\nVARIANT=lite\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "openccu-loom.api")
	if err := os.WriteFile(token, []byte(litefake.DefaultToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENCCU_LOOM_LITE_ADDON_VERSION_FILE", version)
	t.Setenv("OPENCCU_LOOM_LITE_ADDON_TOKEN_FILE", token)
	t.Setenv("OPENCCU_LOOM_LITE_ADDON_BASE_URL", box.URL())

	h := harness.Start(t, harness.Options{NoCentral: true})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	get := func(path string) (int, []byte) {
		t.Helper()
		req, err := h.REST().NewRequest(http.MethodGet, path, http.NoBody)
		if err != nil {
			t.Fatalf("request %s: %v", path, err)
		}
		resp, err := h.REST().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, body
	}

	// 1. The central appears by itself, named after the box's hostname,
	//    credentialed by the token file (never a stored token).
	deadline := time.Now().Add(60 * time.Second)
	var centrals []byte
	for {
		status, body := get("/api/v1/centrals")
		if status == http.StatusOK && strings.Contains(string(body), `"system_type":"openccu-lite"`) {
			centrals = body
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no auto-onboarded central (status=%d body=%s)", status, body)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.Contains(string(centrals), `"name":"`+litefake.DefaultHostname+`"`) {
		t.Errorf("central is not named after the box hostname: %s", centrals)
	}
	if !strings.Contains(string(centrals), `"api_token_file":`) {
		t.Errorf("central carries no api_token_file: %s", centrals)
	}

	// 2. The adoption is live, not just persisted: the box's devices
	//    arrive over the lite API.
	for {
		status, body := get("/api/v1/devices?central=" + litefake.DefaultHostname + "&page_size=1")
		if status == http.StatusOK {
			var out struct {
				Total int `json:"total"`
			}
			if json.Unmarshal(body, &out) == nil && out.Total > 0 {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no devices from the auto-onboarded box (status=%d body=%.200s)", status, body)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
