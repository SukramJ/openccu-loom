// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/build"
)

func TestInfo_HappyPath(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	Info(startedAt, nil, "").ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
	}
	var body InfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.StartedAt == "" {
		t.Fatal("started_at must not be empty")
	}
	if body.Uptime == "" {
		t.Fatal("uptime must not be empty")
	}
}

// TestInfoServesTheConfigUIURL pins the field a client reads to link a
// person at this daemon's UI. Empty stays empty rather than becoming a
// guess: the client's fallback is its own connection address, and a
// daemon inventing one here would override a fallback it knows less
// about than the client does.
func TestInfoServesTheConfigUIURL(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, in, want string }{
		{"configured", "https://loom.example.de/app/", "https://loom.example.de/app/"},
		{"not configured", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
			Info(time.Now(), nil, tc.in).ServeHTTP(w, req)

			var body InfoResponse
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if body.ConfigUIURL != tc.want {
				t.Errorf("config_ui_url = %q, want %q", body.ConfigUIURL, tc.want)
			}
		})
	}
}

func TestInfo_StartedAtIsRFC3339(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 4, 27, 8, 30, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	Info(startedAt, nil, "").ServeHTTP(w, req)

	var body InfoResponse
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.StartedAt != "2026-04-27T08:30:00Z" {
		t.Fatalf("expected RFC3339 format, got %q", body.StartedAt)
	}
}

func TestInfo_ContentTypeIsJSON(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	Info(time.Now(), nil, "").ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct == "" {
		t.Fatal("Content-Type must be set")
	}
}

type fakeCapDetector struct {
	mqtt, matter, oidc, alarm bool
}

func (f fakeCapDetector) HasMQTTDiscovery() bool     { return f.mqtt }
func (f fakeCapDetector) HasMatterBridge() bool      { return f.matter }
func (f fakeCapDetector) HasOIDC() bool              { return f.oidc }
func (f fakeCapDetector) HasCCUAuth() bool           { return false }
func (f fakeCapDetector) HasSupervisedRestart() bool { return false }
func (f fakeCapDetector) HasMCP() bool               { return false }
func (f fakeCapDetector) HasMCPWrite() bool          { return false }
func (f fakeCapDetector) HasAlarm() bool             { return f.alarm }
func (f fakeCapDetector) HasHistory() bool           { return false }
func (f fakeCapDetector) HasAddonSelfUpdate() bool   { return false }

func TestInfo_APIVersionAndAlwaysOnCapabilities(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	Info(time.Now(), nil, "").ServeHTTP(w, req)

	var body InfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.APIVersion != APIVersion {
		t.Fatalf("api_version = %q, want %q", body.APIVersion, APIVersion)
	}
	want := map[string]bool{
		CapabilityREST:           true,
		CapabilityWSBroadcasts:   true,
		CapabilityProblemDetails: true,
	}
	for _, c := range body.Capabilities {
		delete(want, c)
	}
	if len(want) != 0 {
		t.Fatalf("missing always-on capabilities: %v (got %v)", want, body.Capabilities)
	}
}

func TestInfo_ConditionalCapabilities(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	Info(time.Now(), fakeCapDetector{mqtt: true, matter: true, oidc: false}, "").ServeHTTP(w, req)

	var body InfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	has := func(c string) bool {
		return slices.Contains(body.Capabilities, c)
	}
	if !has(CapabilityMQTTDiscovery) {
		t.Error("mqtt.discovery.v1 missing")
	}
	if !has(CapabilityMatterBridge) {
		t.Error("matter.bridge.v1 missing")
	}
	if has(CapabilityOIDC) {
		t.Error("auth.oidc.v1 should be absent")
	}
	if has(CapabilityAlarm) {
		t.Error("alarm.v1 should be absent when the alarm service is unmounted")
	}
}

func TestInfo_AlarmCapability(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	Info(time.Now(), fakeCapDetector{alarm: true}, "").ServeHTTP(w, req)

	var body InfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !slices.Contains(body.Capabilities, CapabilityAlarm) {
		t.Fatalf("alarm.v1 missing from %v when the alarm service is mounted", body.Capabilities)
	}
}

func TestInfo_SchemaDigestIsServed(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	Info(time.Now(), nil, "").ServeHTTP(w, req)

	var body InfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.SchemaDigest != SchemaDigest {
		t.Fatalf("schema_digest=%q, want generated constant %q", body.SchemaDigest, SchemaDigest)
	}
	if !strings.HasPrefix(body.SchemaDigest, "sha256:") || len(body.SchemaDigest) != len("sha256:")+64 {
		t.Fatalf("schema_digest %q must be sha256: plus 64 hex chars", body.SchemaDigest)
	}
}

func TestInfo_AddonBuild(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	Info(time.Now(), nil, "").ServeHTTP(w, req)

	var body InfoResponse
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.AddonBuild {
		t.Fatalf("addon_build = %v, want false by default", body.AddonBuild)
	}
	if !strings.Contains(w.Body.String(), `"addon_build":false`) {
		t.Fatalf("expected body to contain addon_build:false, got %s", w.Body.String())
	}

	original := build.AddonBuild
	build.AddonBuild = "true"
	t.Cleanup(func() { build.AddonBuild = original })

	req = httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w = httptest.NewRecorder()
	Info(time.Now(), nil, "").ServeHTTP(w, req)

	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !body.AddonBuild {
		t.Fatalf("addon_build = %v, want true after override", body.AddonBuild)
	}
	if !strings.Contains(w.Body.String(), `"addon_build":true`) {
		t.Fatalf("expected body to contain addon_build:true, got %s", w.Body.String())
	}
}
