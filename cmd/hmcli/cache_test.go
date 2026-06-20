// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// newTestConfig builds a two-central topology used by the offline
// scope-expansion tests: ccu1 has HmIP-RF + BidCos-RF, ccu2 has HmIP-RF.
func newTestConfig() *config.Config {
	return &config.Config{
		Centrals: []config.CentralConfig{
			{
				Name: "ccu1",
				Interfaces: []config.InterfaceSpec{
					{Name: "HmIP-RF"},
					{Name: "BidCos-RF"},
				},
			},
			{
				Name: "ccu2",
				Interfaces: []config.InterfaceSpec{
					{Name: "HmIP-RF"},
				},
			},
		},
	}
}

// ─── scope validation ─────────────────────────────────────────────────────────

func TestCacheClearUnknownScopeReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "clear", "--scope", "bogus"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown scope")
	}
	if !strings.Contains(err.Error(), "scope") {
		t.Errorf("error should mention scope, got: %v", err)
	}
}

func TestCacheClearCentralScopeRequiresCentral(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "clear", "--scope", "central"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when --central missing for scope=central")
	}
	if !strings.Contains(err.Error(), "central") {
		t.Errorf("error should mention central, got: %v", err)
	}
}

func TestCacheClearInterfaceScopeRequiresBothQualifiers(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "clear", "--scope", "interface", "--central", "ccu1"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when --interface missing for scope=interface")
	}
	if !strings.Contains(err.Error(), "interface") {
		t.Errorf("error should mention interface, got: %v", err)
	}
}

func TestCacheClearDeviceScopeRequiresAllQualifiers(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "device",
		"--central", "ccu1", "--interface", "HmIP-RF",
	}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when --device missing for scope=device")
	}
	if !strings.Contains(err.Error(), "device") {
		t.Errorf("error should mention device, got: %v", err)
	}
}

// ─── subcommand routing ───────────────────────────────────────────────────────

func TestCacheClearMissingOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error when cache has no operation")
	}
	if !strings.Contains(err.Error(), "missing operation") {
		t.Errorf("error=%v, want 'missing operation'", err)
	}
}

func TestCacheClearUnknownCacheOperationReturnsError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "frobnicate"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for unknown cache operation")
	}
}

// ─── online mode: flag → request body mapping ─────────────────────────────────

// capturedRequest records the JSON body a cache-clear test server received.
type capturedRequest struct {
	Kind      string `json:"kind"`
	Central   string `json:"central"`
	Interface string `json:"interface"`
	Device    string `json:"device"`
}

// newCacheClearServer returns a test server that captures the posted body and
// replies with a minimal report. The captured body is written through *got.
func newCacheClearServer(t *testing.T, got *capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method=%s, want POST", r.Method)
		}
		if r.URL.Path != "/api/v1/admin/cache/clear" {
			t.Errorf("path=%s, want /api/v1/admin/cache/clear", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, got); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"devices":1,"paramsets":2,"values":3,"master":4,"centrals_reinit":["ccu1"]}`))
	}))
}

func TestCacheClearOnlineGlobalPostsCorrectBody(t *testing.T) {
	t.Parallel()
	var got capturedRequest
	ts := newCacheClearServer(t, &got)
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	if err := run([]string{"cache", "clear", "--scope", "global", "--url", ts.URL}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Kind != "global" {
		t.Errorf("kind=%q, want global", got.Kind)
	}
	if got.Central != "" || got.Interface != "" || got.Device != "" {
		t.Errorf("qualifiers should be empty for global, got %+v", got)
	}
	if !strings.Contains(stdout.String(), "scope=global") {
		t.Errorf("stdout missing scope=global: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "ccu1") {
		t.Errorf("stdout missing re-pulled central: %q", stdout.String())
	}
}

func TestCacheClearOnlineCentralPostsCorrectBody(t *testing.T) {
	t.Parallel()
	var got capturedRequest
	ts := newCacheClearServer(t, &got)
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "central",
		"--central", "ccu1", "--url", ts.URL,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Kind != "central" {
		t.Errorf("kind=%q, want central", got.Kind)
	}
	if got.Central != "ccu1" {
		t.Errorf("central=%q, want ccu1", got.Central)
	}
}

func TestCacheClearOnlineInterfacePostsCorrectBody(t *testing.T) {
	t.Parallel()
	var got capturedRequest
	ts := newCacheClearServer(t, &got)
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "interface",
		"--central", "ccu1", "--interface", "HmIP-RF", "--url", ts.URL,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Kind != "interface" || got.Central != "ccu1" || got.Interface != "HmIP-RF" {
		t.Errorf("body mismatch: %+v", got)
	}
}

func TestCacheClearOnlineDevicePostsCorrectBody(t *testing.T) {
	t.Parallel()
	var got capturedRequest
	ts := newCacheClearServer(t, &got)
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "device",
		"--central", "ccu1", "--interface", "HmIP-RF", "--device", "ABC123", "--url", ts.URL,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got.Kind != "device" || got.Device != "ABC123" {
		t.Errorf("body mismatch: %+v", got)
	}
}

func TestCacheClearOnlineNon2xxReturnsError(t *testing.T) {
	t.Parallel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "service unready", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "clear", "--scope", "global", "--url", ts.URL}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error on non-2xx response")
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error should mention HTTP 503, got: %v", err)
	}
}

func TestCacheClearOnlineSendsBearerToken(t *testing.T) {
	t.Parallel()
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"devices":0,"paramsets":0,"values":0,"master":0}`))
	}))
	defer ts.Close()

	var stdout, stderr bytes.Buffer
	err := run([]string{
		"cache", "clear", "--scope", "global", "--url", ts.URL, "--token", "secret-tok",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotAuth != "Bearer secret-tok" {
		t.Errorf("Authorization=%q, want 'Bearer secret-tok'", gotAuth)
	}
}

// ─── offline scope → units expansion ──────────────────────────────────────────

func TestResolveOfflineUnitsInterfaceNeedsNoConfig(t *testing.T) {
	t.Parallel()
	units, err := resolveOfflineUnits("interface", "ccu1", "HmIP-RF", nil)
	if err != nil {
		t.Fatalf("resolveOfflineUnits: %v", err)
	}
	if len(units) != 1 || units[0].central != "ccu1" || units[0].iface != "HmIP-RF" {
		t.Fatalf("units=%+v", units)
	}
}

func TestResolveOfflineUnitsGlobalRequiresConfig(t *testing.T) {
	t.Parallel()
	if _, err := resolveOfflineUnits("global", "", "", nil); err == nil {
		t.Fatal("expected error: global scope without config cannot enumerate centrals")
	}
}

func TestResolveOfflineUnitsCentralUnknownNameErrors(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	if _, err := resolveOfflineUnits("central", "nope", "", cfg); err == nil {
		t.Fatal("expected error for unknown central name")
	}
}

func TestResolveOfflineUnitsGlobalEnumeratesAll(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	units, err := resolveOfflineUnits("global", "", "", cfg)
	if err != nil {
		t.Fatalf("resolveOfflineUnits: %v", err)
	}
	if len(units) != 3 { // ccu1: HmIP-RF, BidCos-RF ; ccu2: HmIP-RF
		t.Fatalf("want 3 units, got %d: %+v", len(units), units)
	}
}

func TestResolveOfflineUnitsCentralEnumeratesItsInterfaces(t *testing.T) {
	t.Parallel()
	cfg := newTestConfig()
	units, err := resolveOfflineUnits("central", "ccu1", "", cfg)
	if err != nil {
		t.Fatalf("resolveOfflineUnits: %v", err)
	}
	if len(units) != 2 {
		t.Fatalf("want 2 units for ccu1, got %d: %+v", len(units), units)
	}
}

// ─── offline DSN resolution ───────────────────────────────────────────────────

func TestResolveOfflineDSNRequiresConfigOrDB(t *testing.T) {
	t.Parallel()
	if _, _, err := resolveOfflineDSN("interface", "", ""); err == nil {
		t.Fatal("expected error: offline mode needs --config or --db")
	}
}

func TestResolveOfflineDSNDBOverrideWins(t *testing.T) {
	t.Parallel()
	dsn, cfg, err := resolveOfflineDSN("interface", "", "/tmp/custom.db")
	if err != nil {
		t.Fatalf("resolveOfflineDSN: %v", err)
	}
	if cfg != nil {
		t.Errorf("config should be nil when only --db is given, got %+v", cfg)
	}
	if !strings.Contains(dsn, "/tmp/custom.db") {
		t.Errorf("dsn should use the override path, got %q", dsn)
	}
}

func TestResolveOfflineDSNGlobalNeedsConfigEvenWithDB(t *testing.T) {
	t.Parallel()
	if _, _, err := resolveOfflineDSN("global", "", "/tmp/custom.db"); err == nil {
		t.Fatal("expected error: global scope cannot enumerate interfaces without config")
	}
}

func TestRunCacheClearOfflineMissingConfigErrors(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	err := run([]string{"cache", "clear", "--scope", "global", "--offline"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("expected error for offline global clear without --config")
	}
}
