// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// TestDiagnosticsCentralScoresFollowTheRegistry pins that the composite dump
// scores the CCUs the daemon serves *now*, not the ones it served when the
// router was mounted.
//
// The router is built exactly once, at boot, while the central registry keeps
// changing: a CCU can be adopted at runtime and an existing one can be
// removed. Resolving the name set at mount time is invisible either way — the
// dump renders, every score in it is correct, and the CCU that joined
// afterwards is simply absent from `health.central_scores` while a removed one
// keeps being scored. Support triage reads that map as the daemon's CCU
// inventory, so both halves mislead.
//
// The dependency is passed the way the composition root passes it — the
// registry's own Names method — so the test crosses the same seam production
// does rather than a slice the test built.
func TestDiagnosticsCentralScoresFollowTheRegistry(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	registerCentral(t, reg, "ccu-boot")

	tracker := health.NewTracker()
	// One healthy component per central, named in the scope the tracker
	// attributes to a central, so the score is a real 100 rather than the
	// zero an unknown name yields.
	tracker.Record("ccu-boot"+health.ScopeSeparator+"HmIP-RF", health.Sample{Healthy: true})

	r := NewRouter(Deps{
		StartedAt:     time.Now(),
		Health:        tracker,
		HealthExtras:  tracker,
		KnownCentrals: reg.Names,
	})

	// The second CCU is adopted only now — after the router exists.
	registerCentral(t, reg, "ccu-late")
	tracker.Record("ccu-late"+health.ScopeSeparator+"HmIP-RF", health.Sample{Healthy: true})

	scores := diagnosticsCentralScores(t, r)
	if got, ok := scores["ccu-late"]; !ok {
		t.Errorf("central_scores = %v, want an entry for the CCU adopted after the router was built", scores)
	} else if got != 100 {
		t.Errorf("central_scores[ccu-late] = %d, want 100", got)
	}
	if got, ok := scores["ccu-boot"]; !ok || got != 100 {
		t.Errorf("central_scores[ccu-boot] = %d (present=%v), want 100", got, ok)
	}

	// ... and a CCU that leaves the registry leaves the dump with it.
	if !reg.Unregister("ccu-boot") {
		t.Fatal("Unregister reported the central was not registered")
	}
	scores = diagnosticsCentralScores(t, r)
	if _, ok := scores["ccu-boot"]; ok {
		t.Errorf("central_scores = %v, want no entry for the removed CCU", scores)
	}
	if _, ok := scores["ccu-late"]; !ok {
		t.Errorf("central_scores = %v, want the remaining CCU to stay scored", scores)
	}
}

// registerCentral puts a bare unit under name into reg.
func registerCentral(t *testing.T, reg *central.Registry, name string) {
	t.Helper()
	u, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New(%q): %v", name, err)
	}
	if err := reg.Register(u); err != nil {
		t.Fatalf("reg.Register(%q): %v", name, err)
	}
}

// diagnosticsCentralScores performs one GET /api/v1/diagnostics against h and
// returns the per-central score map of the response.
func diagnosticsCentralScores(t *testing.T, h http.Handler) map[string]int {
	t.Helper()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics", http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rr.Code, rr.Body.String())
	}
	var env handlers.DiagnosticsEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env.Health.CentralScores
}
