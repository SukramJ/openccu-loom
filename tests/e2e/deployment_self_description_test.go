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
	"slices"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestDeploymentReachesInfo pins, through the built binary, that the
// daemon's deployment and the login paths that hang on it arrive at
// `GET /api/v1/info` (ADR 0081) — one run per deployment kind, each
// driven only by what the packaging or the host supplies.
//
// The consumer is a client deciding which setup fields to show: the box
// fields on a lite box, none of them anywhere else. A handler test cannot
// prove this; it is handed the value the composition root has to produce.
//
// Not parallel: the daemon child inherits the test process environment,
// which t.Setenv steers.
func TestDeploymentReachesInfo(t *testing.T) {
	const (
		haIngress     = "auth.ha_ingress.v1"
		occuliteSSO   = "auth.occulite_sso.v1"
		occuliteToken = "auth.occulite_token.v1"
		ccuAuth       = "auth.ccu.v1"
	)
	hostBound := []string{haIngress, occuliteSSO, occuliteToken, ccuAuth}

	cases := []struct {
		name        string
		declared    string
		liteBox     bool
		wantKind    string
		wantIngress string
		wantPaths   []string // the host-bound login paths expected; the rest must be absent
	}{
		{name: "nothing declared", wantKind: "standalone"},
		{name: "home assistant add-on", declared: "ha-addon", wantKind: "ha-addon", wantPaths: []string{haIngress}},
		{name: "add-on on a classic CCU", declared: "ccu-addon", wantKind: "ccu-addon", wantPaths: []string{ccuAuth}},
		{
			name: "add-on on a lite box", declared: "ccu-addon", liteBox: true,
			wantKind: "lite-addon", wantIngress: "/addons/loom/",
			wantPaths: []string{occuliteSSO, occuliteToken, ccuAuth},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.declared != "" {
				t.Setenv("OPENCCU_LOOM_DEPLOYMENT", tc.declared)
			}
			// The restart supervisor is set by the HA image and by the CCU
			// start script alike; it must decide nothing here.
			t.Setenv("OPENCCU_LOOM_SUPERVISOR", "1")
			opts := harness.Options{}
			if tc.liteBox {
				opts = harness.Options{NoCentral: true}
				pointAtLiteBox(t)
			}
			h := harness.Start(t, opts)
			info := fetchInfo(t, h)

			dep, _ := info["deployment"].(map[string]any)
			if got, _ := dep["kind"].(string); got != tc.wantKind {
				t.Errorf("deployment.kind = %q, want %q (deployment: %v)", got, tc.wantKind, dep)
			}
			if got, _ := dep["ingress_path"].(string); got != tc.wantIngress {
				t.Errorf("deployment.ingress_path = %q, want %q", got, tc.wantIngress)
			}

			var caps []string
			for _, c := range info["capabilities"].([]any) {
				caps = append(caps, c.(string))
			}
			for _, p := range hostBound {
				want := slices.Contains(tc.wantPaths, p)
				if got := slices.Contains(caps, p); got != want {
					t.Errorf("capability %s present = %v, want %v (capabilities: %v)", p, got, want, caps)
				}
			}
		})
	}
}

// pointAtLiteBox makes the daemon child see an openccu-lite box that
// minted this add-on's token: the two host facts the lite add-on is
// recognised by, and a fake box to verify against.
func pointAtLiteBox(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
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
}

// fetchInfo reads GET /info without a credential, as a client does before
// it has one.
func fetchInfo(t *testing.T, h *harness.Harness) map[string]any {
	t.Helper()
	resp, err := h.REST().HTTPClient().Get(h.RESTBase() + "/api/v1/info")
	if err != nil {
		t.Fatalf("GET /api/v1/info: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/api/v1/info: status=%d body=%s", resp.StatusCode, body)
	}
	var info map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode /api/v1/info: %v", err)
	}
	return info
}
