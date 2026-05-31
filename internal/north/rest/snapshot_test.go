// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// TestSnapshotEnvelopeAggregatesAllSources verifies that the
// `/snapshot` endpoint folds devices, hub state, and interface state
// into one envelope.
func TestSnapshotEnvelopeAggregatesAllSources(t *testing.T) {
	h := newHubRouter(t)

	req := httptest.NewRequest("GET", "/api/v1/snapshot", http.NoBody)
	rr := httptest.NewRecorder()
	h.handler.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var env handlers.SnapshotEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rr.Body.String())
	}
	if env.GeneratedAt == "" {
		t.Fatal("generated_at empty — snapshot must always carry a timestamp")
	}
	// Hub-fed slices: programs + sysvars from hubHarness.
	if len(env.Programs) != 1 || env.Programs[0].ID != "P1" {
		t.Fatalf("programs=%+v want one entry P1", env.Programs)
	}
	if len(env.Sysvars) != 1 || env.Sysvars[0].Name != "PartyMode" {
		t.Fatalf("sysvars=%+v want one entry PartyMode", env.Sysvars)
	}
	// Interfaces from fakeInterfaceIndex.
	if len(env.Interfaces) != 1 || env.Interfaces[0].ID != "HmIP-RF" {
		t.Fatalf("interfaces=%+v want HmIP-RF", env.Interfaces)
	}
}

// TestSnapshotEmptyDepsStillReturns200 guards the contract that
// missing sources contribute empty slices rather than 5xx — backup
// tooling pulling /snapshot during boot must not crash on a half-
// wired daemon.
func TestSnapshotEmptyDepsStillReturns200(t *testing.T) {
	r := NewRouter(Deps{})
	req := httptest.NewRequest("GET", "/api/v1/snapshot", http.NoBody)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var env handlers.SnapshotEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v body=%s", err, rr.Body.String())
	}
	if env.GeneratedAt == "" {
		t.Fatal("generated_at must be set even when no source is wired")
	}
	if len(env.Devices) != 0 || len(env.Programs) != 0 || len(env.Sysvars) != 0 {
		t.Fatalf("expected empty body, got %+v", env)
	}
}

// TestSnapshotAnonymizeRedactsLabels verifies the privacy mode: with
// `?anonymize=1`, operator-assigned strings are tokenised but addresses, IDs,
// and structural shape stay intact.
func TestSnapshotAnonymizeRedactsLabels(t *testing.T) {
	h := newHubRouter(t)

	plain := httptest.NewRequest("GET", "/api/v1/snapshot", http.NoBody)
	rr := httptest.NewRecorder()
	h.handler.ServeHTTP(rr, plain)
	var ref handlers.SnapshotEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &ref); err != nil {
		t.Fatalf("unmarshal plain: %v", err)
	}

	anon := httptest.NewRequest("GET", "/api/v1/snapshot?anonymize=1", http.NoBody)
	rr2 := httptest.NewRecorder()
	h.handler.ServeHTTP(rr2, anon)
	if rr2.Code != 200 {
		t.Fatalf("status=%d body=%s", rr2.Code, rr2.Body.String())
	}
	var got handlers.SnapshotEnvelope
	if err := json.Unmarshal(rr2.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal anon: %v", err)
	}

	// Sysvar names must change under anonymisation.
	if len(got.Sysvars) != len(ref.Sysvars) {
		t.Fatalf("sysvar count drift: %d vs %d", len(got.Sysvars), len(ref.Sysvars))
	}
	for i, s := range ref.Sysvars {
		if got.Sysvars[i].Name == s.Name {
			t.Fatalf("sysvar[%d] not anonymised: %q", i, s.Name)
		}
	}
	// Programs: name must change, description cleared.
	for i, p := range ref.Programs {
		if got.Programs[i].Name == p.Name {
			t.Fatalf("program[%d] name not anonymised: %q", i, p.Name)
		}
		if got.Programs[i].Description != "" {
			t.Fatalf("program[%d] description must be cleared, got %q", i, got.Programs[i].Description)
		}
	}
}

// TestSnapshotAnonymizeStableTokens guards the property that the
// same input twice yields the same anonymised output — diagnostics
// across two snapshots correlate via these tokens.
func TestSnapshotAnonymizeStableTokens(t *testing.T) {
	h := newHubRouter(t)

	first := httptest.NewRequest("GET", "/api/v1/snapshot?anonymize=true", http.NoBody)
	rr := httptest.NewRecorder()
	h.handler.ServeHTTP(rr, first)
	var a handlers.SnapshotEnvelope
	_ = json.Unmarshal(rr.Body.Bytes(), &a)

	second := httptest.NewRequest("GET", "/api/v1/snapshot?anonymize=yes", http.NoBody)
	rr2 := httptest.NewRecorder()
	h.handler.ServeHTTP(rr2, second)
	var b handlers.SnapshotEnvelope
	_ = json.Unmarshal(rr2.Body.Bytes(), &b)

	if len(a.Sysvars) != len(b.Sysvars) {
		t.Fatalf("sysvar drift")
	}
	for i := range a.Sysvars {
		if a.Sysvars[i].Name != b.Sysvars[i].Name {
			t.Fatalf("sysvar[%d] token mismatch: %q vs %q", i, a.Sysvars[i].Name, b.Sysvars[i].Name)
		}
	}
}
