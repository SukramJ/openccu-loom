// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package metrics

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestMetricsSnapshotJSONRoundtrip(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 28, 12, 0, 0, 0, time.UTC)
	snap := MetricsSnapshot{
		Timestamp: ts,
		RPC: RpcMetrics{
			TotalRequests:      100,
			SuccessfulRequests: 90,
			FailedRequests:     5,
			RejectedRequests:   5,
			AvgLatencyMs:       12.345,
			MaxLatencyMs:       99.99,
		},
		RPCServer: RpcServerMetrics{
			TotalRequests: 200,
			TotalErrors:   10,
			ActiveTasks:   3,
			AvgLatencyMs:  8.0,
		},
		Health: HealthMetrics{
			OverallScore:   0.95,
			ClientsTotal:   4,
			ClientsHealthy: 3,
		},
	}

	data, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Basic field presence checks.
	for _, needle := range []string{
		`"total_requests":100`,
		`"avg_latency_ms"`,
		`"overall_score"`,
		`"timestamp"`,
	} {
		if !strings.Contains(string(data), needle) {
			t.Errorf("missing %q in JSON:\n%s", needle, data)
		}
	}

	// Roundtrip: unmarshal back into a generic map and check key counts.
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := m["rpc"]; !ok {
		t.Error("missing 'rpc' key")
	}
	if _, ok := m["rpc_server"]; !ok {
		t.Error("missing 'rpc_server' key")
	}
	if _, ok := m["health"]; !ok {
		t.Error("missing 'health' key")
	}
}

func TestRpcMetricsRates(t *testing.T) {
	t.Parallel()

	r := RpcMetrics{
		TotalRequests:      100,
		SuccessfulRequests: 80,
		FailedRequests:     10,
		RejectedRequests:   10,
		CoalescedRequests:  20,
	}
	if got := r.SuccessRate(); got != 80 {
		t.Errorf("success rate=%f", got)
	}
	if got := r.FailureRate(); got != 10 {
		t.Errorf("failure rate=%f", got)
	}
	if got := r.RejectionRate(); got != 10 {
		t.Errorf("rejection rate=%f", got)
	}
	if got := r.CoalesceRate(); got != 20 {
		t.Errorf("coalesce rate=%f", got)
	}
}

func TestRpcMetricsZeroTotal(t *testing.T) {
	t.Parallel()

	r := RpcMetrics{}
	if r.SuccessRate() != 100 {
		t.Error("zero total should give 100% success")
	}
	if r.FailureRate() != 0 {
		t.Error("zero total should give 0% failure")
	}
}

func TestCacheMetricsSnapshotTotalEntries(t *testing.T) {
	t.Parallel()

	snap := CacheMetricsSnapshot{
		DeviceDescriptions:   SizeOnlySnapshot{Size: 5},
		ParamsetDescriptions: SizeOnlySnapshot{Size: 10},
		VisibilityRegistry:   SizeOnlySnapshot{Size: 3},
		PingPongTracker:      SizeOnlySnapshot{Size: 2},
		CommandTracker:       SizeOnlySnapshot{Size: 1},
		DataCache:            CacheStatsSnapshot{Size: 50},
	}
	if got := snap.TotalEntries(); got != 71 {
		t.Errorf("total=%d, want 71", got)
	}
}

func TestRecoveryMetricsSuccessRate(t *testing.T) {
	t.Parallel()

	r := RecoveryMetrics{AttemptsTotal: 10, Successes: 7}
	if got := r.SuccessRate(); got != 70 {
		t.Errorf("success rate=%f, want 70", got)
	}

	empty := RecoveryMetrics{}
	if empty.SuccessRate() != 100 {
		t.Error("zero attempts should give 100% success")
	}
}

func TestServiceMetricsSnapshotErrorRate(t *testing.T) {
	t.Parallel()

	s := ServiceMetricsSnapshot{TotalCalls: 10, TotalErrors: 2}
	if got := s.ErrorRate(); got != 20 {
		t.Errorf("error rate=%f, want 20", got)
	}
}

// ---------------------------------------------------------------------------
// RpcServerMetrics — ErrorRate and SuccessRate
// ---------------------------------------------------------------------------

func TestRpcServerMetricsRates(t *testing.T) {
	t.Parallel()
	m := RpcServerMetrics{TotalRequests: 10, TotalErrors: 3}
	if math.Abs(m.ErrorRate()-30) > 0.001 {
		t.Errorf("ErrorRate=%f, want 30", m.ErrorRate())
	}
	if math.Abs(m.SuccessRate()-70) > 0.001 {
		t.Errorf("SuccessRate=%f, want 70", m.SuccessRate())
	}
}

func TestRpcServerMetricsZeroRequests(t *testing.T) {
	t.Parallel()
	m := RpcServerMetrics{}
	if m.ErrorRate() != 0 {
		t.Errorf("ErrorRate with no requests=%f, want 0", m.ErrorRate())
	}
	if m.SuccessRate() != 100 {
		t.Errorf("SuccessRate with no requests=%f, want 100", m.SuccessRate())
	}
}

// ---------------------------------------------------------------------------
// EventMetrics — ErrorRate
// ---------------------------------------------------------------------------

func TestEventMetricsErrorRate(t *testing.T) {
	t.Parallel()
	e := EventMetrics{HandlersExecuted: 20, HandlerErrors: 4}
	if math.Abs(e.ErrorRate()-20) > 0.001 {
		t.Errorf("ErrorRate=%f, want 20", e.ErrorRate())
	}
}

func TestEventMetricsErrorRateZero(t *testing.T) {
	t.Parallel()
	e := EventMetrics{}
	if e.ErrorRate() != 0 {
		t.Error("ErrorRate with no handlers must be 0")
	}
}

// ---------------------------------------------------------------------------
// CacheMetricsSnapshot — OverallHitRate
// ---------------------------------------------------------------------------

func TestCacheMetricsSnapshotOverallHitRate(t *testing.T) {
	t.Parallel()
	c := CacheMetricsSnapshot{DataCache: CacheStatsSnapshot{Hits: 8, Misses: 2, Size: 10}}
	if math.Abs(c.OverallHitRate()-80) > 0.001 {
		t.Errorf("OverallHitRate=%f, want 80", c.OverallHitRate())
	}
}

// ---------------------------------------------------------------------------
// HealthMetrics — AvailabilityRate
// ---------------------------------------------------------------------------

func TestHealthMetricsAvailabilityRate(t *testing.T) {
	t.Parallel()
	h := HealthMetrics{ClientsTotal: 4, ClientsHealthy: 3}
	if math.Abs(h.AvailabilityRate()-75) > 0.001 {
		t.Errorf("AvailabilityRate=%f, want 75", h.AvailabilityRate())
	}
}

func TestHealthMetricsAvailabilityRateZeroClients(t *testing.T) {
	t.Parallel()
	h := HealthMetrics{}
	if h.AvailabilityRate() != 100 {
		t.Error("zero clients must return 100% availability")
	}
}

// ---------------------------------------------------------------------------
// ServiceMetricsSnapshot — ErrorRate zero/nonzero
// ---------------------------------------------------------------------------

func TestServiceMetricsSnapshotErrorRateNonZero(t *testing.T) {
	t.Parallel()
	s := ServiceMetricsSnapshot{TotalCalls: 10, TotalErrors: 2}
	if math.Abs(s.ErrorRate()-20) > 0.001 {
		t.Errorf("ErrorRate=%f, want 20", s.ErrorRate())
	}
}

func TestServiceMetricsSnapshotErrorRateZeroCalls(t *testing.T) {
	t.Parallel()
	s := ServiceMetricsSnapshot{}
	if s.ErrorRate() != 0 {
		t.Error("zero calls must return 0% error rate")
	}
}

// ---------------------------------------------------------------------------
// RpcMetrics — CoalesceRate and RejectionRate
// ---------------------------------------------------------------------------

func TestRpcMetricsCoalesceRate(t *testing.T) {
	t.Parallel()
	m := RpcMetrics{TotalRequests: 10, CoalescedRequests: 3}
	if math.Abs(m.CoalesceRate()-30) > 0.001 {
		t.Errorf("CoalesceRate=%f, want 30", m.CoalesceRate())
	}
	zero := RpcMetrics{}
	if zero.CoalesceRate() != 0 {
		t.Error("zero TotalRequests must return 0 CoalesceRate")
	}
}

func TestRpcMetricsRejectionRate(t *testing.T) {
	t.Parallel()
	m := RpcMetrics{TotalRequests: 10, RejectedRequests: 2}
	if math.Abs(m.RejectionRate()-20) > 0.001 {
		t.Errorf("RejectionRate=%f, want 20", m.RejectionRate())
	}
	zero := RpcMetrics{}
	if zero.RejectionRate() != 0 {
		t.Error("zero TotalRequests must return 0 RejectionRate")
	}
}
