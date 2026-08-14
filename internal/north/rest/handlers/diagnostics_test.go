// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

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
