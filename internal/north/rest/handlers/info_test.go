// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

func TestInfo_HappyPath(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	Info(startedAt, nil).ServeHTTP(w, req)

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

func TestInfo_StartedAtIsRFC3339(t *testing.T) {
	t.Parallel()
	startedAt := time.Date(2026, 4, 27, 8, 30, 0, 0, time.UTC)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	Info(startedAt, nil).ServeHTTP(w, req)

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
	Info(time.Now(), nil).ServeHTTP(w, req)

	ct := w.Header().Get("Content-Type")
	if ct == "" {
		t.Fatal("Content-Type must be set")
	}
}

type fakeCapDetector struct {
	mqtt, matter, oidc bool
}

func (f fakeCapDetector) HasMQTTDiscovery() bool     { return f.mqtt }
func (f fakeCapDetector) HasMatterBridge() bool      { return f.matter }
func (f fakeCapDetector) HasOIDC() bool              { return f.oidc }
func (f fakeCapDetector) HasSupervisedRestart() bool { return false }
func (f fakeCapDetector) HasMCP() bool               { return false }
func (f fakeCapDetector) HasMCPWrite() bool          { return false }

func TestInfo_APIVersionAndAlwaysOnCapabilities(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", http.NoBody)
	w := httptest.NewRecorder()
	Info(time.Now(), nil).ServeHTTP(w, req)

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
	Info(time.Now(), fakeCapDetector{mqtt: true, matter: true, oidc: false}).ServeHTTP(w, req)

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
}
