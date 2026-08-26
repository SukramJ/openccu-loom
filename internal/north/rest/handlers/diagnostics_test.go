// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// stubInterfaceIndex serves a fixed interface list to the diagnostics dump.
type stubInterfaceIndex struct{ states []hmapi.InterfaceState }

func (s *stubInterfaceIndex) Interfaces() []hmapi.InterfaceState { return s.states }

func (s *stubInterfaceIndex) Interface(id string) (hmapi.InterfaceState, bool) {
	for _, st := range s.states {
		if st.ID == id {
			return st, true
		}
	}
	return hmapi.InterfaceState{}, false
}

func (s *stubInterfaceIndex) Reconnect(context.Context, string) error { return nil }

// stubIncidentsReader serves a fixed incident list to the diagnostics dump.
type stubIncidentsReader struct{ incidents []hmapi.Incident }

func (s *stubIncidentsReader) Incidents() []hmapi.Incident { return s.incidents }

// stubSystemStatusReader serves a fixed status list to the diagnostics dump.
type stubSystemStatusReader struct{ entries []handlers.SystemStatusEntry }

func (s *stubSystemStatusReader) SystemStatusEntries() []handlers.SystemStatusEntry {
	return s.entries
}

// TestDiagnosticsAnonymisesWhatItClaimsTo drives the default (?anonymize
// absent → true) path. The dump self-certifies as anonymised and is the
// artefact operators attach to bug reports, so the CCU host and the device
// addresses spliced into incident and status free text must not survive it
// verbatim — while the surrounding prose, verdicts and join keys must.
func TestDiagnosticsAnonymisesWhatItClaimsTo(t *testing.T) {
	t.Parallel()

	deps := handlers.DiagnosticsDeps{
		Interfaces: &stubInterfaceIndex{states: []hmapi.InterfaceState{
			{ID: "home-HmIP-RF", Name: "HmIP-RF", Interface: "HmIP-RF", CentralID: "home", Host: "ccu.example.lan"},
		}},
		Incidents: &stubIncidentsReader{incidents: []hmapi.Incident{
			{
				ID:        "i1",
				Component: "client",
				Severity:  "warning",
				Summary:   "device LEQ1234567:1 unreachable",
				Detail:    "dial tcp 10.0.0.5:2010: connection refused",
			},
		}},
		SystemStatus: &stubSystemStatusReader{entries: []handlers.SystemStatusEntry{
			{
				Central:     "home",
				Component:   "interface",
				InterfaceID: "home-HmIP-RF",
				Reason:      "UNREACH on 0001D3C99C1234:2",
				Issues:      []string{"paramset read failed for VCU0000123"},
			},
		}},
	}

	rr := httptest.NewRecorder()
	handlers.Diagnostics(deps).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics", http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	body := rr.Body.String()

	var env handlers.DiagnosticsEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !env.Anonymized {
		t.Fatalf("anonymized = false, want the default true")
	}
	for _, secret := range []string{"ccu.example.lan", "LEQ1234567", "10.0.0.5", "0001D3C99C1234", "VCU0000123"} {
		if strings.Contains(body, secret) {
			t.Errorf("anonymised dump still carries %q: %s", secret, body)
		}
	}
	// The diagnostic signal has to survive: prose, verdicts and the join
	// keys the envelope's sections are correlated by.
	for _, kept := range []string{"connection refused", "unreachable", "UNREACH", "home", "home-HmIP-RF"} {
		if !strings.Contains(body, kept) {
			t.Errorf("anonymised dump lost %q: %s", kept, body)
		}
	}
	// The stub's slices are stand-ins for a live store's; anonymisation
	// must not write through to them.
	if got := deps.Incidents.Incidents()[0].Summary; got != "device LEQ1234567:1 unreachable" {
		t.Errorf("source incident was mutated: %q", got)
	}
	if got := deps.SystemStatus.SystemStatusEntries()[0].Issues[0]; got != "paramset read failed for VCU0000123" {
		t.Errorf("source status issue was mutated: %q", got)
	}
}

// TestDiagnosticsAnonymizeOffKeepsRawValues is the counterpart: an operator
// who explicitly asks for fidelity gets it.
func TestDiagnosticsAnonymizeOffKeepsRawValues(t *testing.T) {
	t.Parallel()

	deps := handlers.DiagnosticsDeps{
		Interfaces: &stubInterfaceIndex{states: []hmapi.InterfaceState{
			{ID: "home-HmIP-RF", Host: "ccu.example.lan"},
		}},
	}

	rr := httptest.NewRecorder()
	handlers.Diagnostics(deps).ServeHTTP(rr,
		httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics?anonymize=0", http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "ccu.example.lan") {
		t.Errorf("anonymize=0 must keep the raw host: %s", rr.Body.String())
	}
}

// TestDiagnosticsCarriesPerCentralMetrics pins the typed metrics block
// of the composite dump. The per-central aggregator is the only source
// of RPC / recovery / model counters in structured form; if the handler
// stops rendering it, the daemon computes those counters on every
// request and throws them away, and a support artefact loses the
// numbers an escalation is triaged from.
func TestDiagnosticsCarriesPerCentralMetrics(t *testing.T) {
	t.Parallel()

	deps := handlers.DiagnosticsDeps{
		CentralMetrics: func(context.Context) map[string]metrics.MetricsSnapshot {
			return map[string]metrics.MetricsSnapshot{
				"ccu-alpha": {
					Timestamp: time.Now(),
					Model:     metrics.ModelMetrics{DevicesTotal: 3, ChannelsTotal: 7},
					RPC:       metrics.RpcMetrics{TotalRequests: 11},
				},
			}
		},
	}

	rr := httptest.NewRecorder()
	handlers.Diagnostics(deps).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics", http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var env handlers.DiagnosticsEnvelope
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	snap, ok := env.Metrics["ccu-alpha"]
	if !ok {
		t.Fatalf("envelope carries no metrics block for ccu-alpha: %s", rr.Body.String())
	}
	if snap.Model.DevicesTotal != 3 || snap.Model.ChannelsTotal != 7 {
		t.Errorf("model section = %+v, want DevicesTotal=3 ChannelsTotal=7", snap.Model)
	}
	if snap.RPC.TotalRequests != 11 {
		t.Errorf("rpc.total_requests = %d, want 11", snap.RPC.TotalRequests)
	}
}

// TestDiagnosticsOmitsMetricsWithoutProvider verifies the block stays
// absent when the composition root wired no provider, so a dump from a
// daemon without metrics does not claim an all-zero fleet.
func TestDiagnosticsOmitsMetricsWithoutProvider(t *testing.T) {
	t.Parallel()

	rr := httptest.NewRecorder()
	handlers.Diagnostics(handlers.DiagnosticsDeps{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/diagnostics", http.NoBody))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if _, ok := raw["metrics"]; ok {
		t.Errorf("metrics block present without a provider: %s", rr.Body.String())
	}
}
