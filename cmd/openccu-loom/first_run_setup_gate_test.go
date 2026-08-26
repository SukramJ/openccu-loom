// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestFirstRunSetupGateHonoursBootstrapToggle boots the real composition
// root twice against an empty database with no authentication source at
// all — the exact state in which the unauthenticated onboarding surface is
// open — and asserts what the two values of bootstrap.allow_first_run_setup
// actually do on the wire.
//
// It goes through daemonServe rather than constructing the SetupService,
// because the toggle's failure mode was precisely that nothing in a running
// daemon ever read it: the accessor was correct and simply unreachable, so
// every test that handed the probe to the handler stayed green while an
// operator's hardened deployment kept accepting anonymous admin creation.
//
// The "allowed" half is not a courtesy case: without it the refusal in the
// "disabled" half could come from any unrelated boot condition rather than
// from the toggle.
func TestFirstRunSetupGateHonoursBootstrapToggle(t *testing.T) {
	for _, tc := range []struct {
		name           string
		allowFirstRun  *bool
		wantRequired   bool
		wantPostStatus int
	}{
		{
			name:           "toggle unset: onboarding open on a fresh install",
			wantRequired:   true,
			wantPostStatus: http.StatusNoContent,
		},
		{
			name:           "toggle false: onboarding dormant despite zero users",
			allowFirstRun:  new(false),
			wantRequired:   false,
			wantPostStatus: http.StatusForbidden,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := firstRunGateConfig(t)
			base.Bootstrap.AllowFirstRunSetup = tc.allowFirstRun
			addr := startDaemonForTest(t, base)

			var status struct {
				Required bool `json:"required"`
			}
			getJSON(t, "http://"+addr+"/api/v1/setup/status", &status)
			if status.Required != tc.wantRequired {
				t.Errorf("GET /api/v1/setup/status required=%v, want %v", status.Required, tc.wantRequired)
			}

			body := strings.NewReader(`{"admin":{"username":"admin","password":"correct-horse"},` +
				`"locale":{"locale":"en","theme":"system"}}`)
			resp, err := http.Post("http://"+addr+"/api/v1/setup", "application/json", body) //nolint:noctx // loopback call against the test daemon; the test's own deadline bounds it
			if err != nil {
				t.Fatalf("POST /api/v1/setup: %v", err)
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != tc.wantPostStatus {
				t.Errorf("POST /api/v1/setup status=%d want %d, body=%s", resp.StatusCode, tc.wantPostStatus, raw)
			}
		})
	}
}

// firstRunGateConfig returns a daemon config in genuine first-run state:
// a fresh data dir, no central, no YAML user, no CCU-delegated login and no
// OIDC — so the onboarding endpoints are the only way in.
func firstRunGateConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.DataDir = t.TempDir()
	cfg.North.REST.Enabled = new(true)
	cfg.North.REST.Listen = "127.0.0.1:0"
	// The onboarding POST is a plain JSON call from a non-browser client;
	// the double-submit guard is covered by its own tests.
	cfg.North.REST.CSRFEnabled = new(false)
	cfg.North.UI.Enabled = new(false)
	cfg.North.REST.Auth.CCU.Enabled = ptrBool(false)
	cfg.Callback.Port = 0
	cfg.Callback.BinPort = 0
	cfg.Centrals = nil
	return cfg
}

// restListenAddrRe extracts the bound REST address from the daemon's own
// `rest.listen` log record. The daemon binds port 0, so the effective port
// is only knowable after boot — reading it back from the log keeps the test
// free of a pre-bound port it would have to race the daemon for.
var restListenAddrRe = regexp.MustCompile(`"msg":"rest\.listen","addr":"([^"]+)"`)

// startDaemonForTest runs the composition root in the background and returns
// the address its REST listener bound. The daemon is stopped on cleanup.
func startDaemonForTest(t *testing.T, cfg *config.Config) string {
	t.Helper()
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemonServe(ctx, cfg, &out, io.Discard) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("daemonServe: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("daemon did not shut down in time")
		}
	})

	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if m := restListenAddrRe.FindStringSubmatch(out.String()); m != nil {
			return m[1]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("REST listener never reported its address; logs:\n%s", out.String())
	return ""
}

func getJSON(t *testing.T, url string, dst any) {
	t.Helper()
	resp, err := http.Get(url) //nolint:noctx // loopback call against the test daemon; the test's own deadline bounds it
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s: status=%d body=%s", url, resp.StatusCode, raw)
	}
	if err := json.NewDecoder(resp.Body).Decode(dst); err != nil {
		t.Fatalf("GET %s: decode: %v", url, err)
	}
}
