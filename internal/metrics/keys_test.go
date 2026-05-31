// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import "testing"

func TestMetricKeyString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		key  MetricKey
		want string
	}{
		{
			key:  MetricKey{Component: "cache", Metric: "data", Identifier: "hit"},
			want: "cache.data.hit",
		},
		{
			key:  MetricKey{Component: "rpc_server", Metric: "request"},
			want: "rpc_server.request",
		},
		{
			key:  MetricKey{Component: "circuit", Metric: "failure", Identifier: "hmip_rf"},
			want: "circuit.failure.hmip_rf",
		},
		{
			key:  MetricKey{Component: "ping_pong", Metric: "rtt", Identifier: "hmip_rf", CentralName: "ccu1"},
			want: "ccu1.ping_pong.rtt.hmip_rf",
		},
		{
			key:  MetricKey{Component: "rpc_server", Metric: "latency", CentralName: "ccu2"},
			want: "ccu2.rpc_server.latency",
		},
	}

	for _, tc := range cases {
		if got := tc.key.String(); got != tc.want {
			t.Errorf("key=%+v: got %q, want %q", tc.key, got, tc.want)
		}
	}
}

func TestMetricKeyMatchesPrefix(t *testing.T) {
	t.Parallel()

	k := MetricKey{Component: "circuit", Metric: "failure", Identifier: "hmip_rf"}
	if !k.MatchesPrefix("circuit.failure") {
		t.Error("expected prefix match")
	}
	if k.MatchesPrefix("circuit.rejection") {
		t.Error("unexpected prefix match")
	}
}

func TestMetricKeyEquality(t *testing.T) {
	t.Parallel()

	a := MetricKeys.PingPongRTT("hmip_rf")
	b := MetricKeys.PingPongRTT("hmip_rf")
	if a != b {
		t.Error("same key should be equal")
	}
	c := MetricKeys.PingPongRTT("cuxd")
	if a == c {
		t.Error("different identifier should not be equal")
	}
}

func TestMetricKeysFactory(t *testing.T) {
	t.Parallel()

	cases := []struct {
		key  MetricKey
		want string
	}{
		{MetricKeys.CacheHit("data"), "cache.data.hit"},
		{MetricKeys.CacheMiss("data"), "cache.data.miss"},
		{MetricKeys.CacheSize("data"), "cache.data.size"},
		{MetricKeys.CacheEviction("data"), "cache.data.eviction"},
		{MetricKeys.CircuitFailure("hmip_rf"), "circuit.failure.hmip_rf"},
		{MetricKeys.CircuitRejection("hmip_rf"), "circuit.rejection.hmip_rf"},
		{MetricKeys.CircuitState("hmip_rf"), "circuit.state.hmip_rf"},
		{MetricKeys.CircuitStateTransition("hmip_rf"), "circuit.state_transition.hmip_rf"},
		{MetricKeys.ClientHealth("hmip_rf"), "client.health.hmip_rf"},
		{MetricKeys.CoalescerCoalesced("hmip_rf"), "coalescer.coalesced.hmip_rf"},
		{MetricKeys.CoalescerFailure("hmip_rf"), "coalescer.failure.hmip_rf"},
		{MetricKeys.HandlerError("DataPointValueChanged"), "handler.error.DataPointValueChanged"},
		{MetricKeys.HandlerExecution("DataPointValueChanged"), "handler.execution.DataPointValueChanged"},
		{MetricKeys.PingPongRTT("hmip_rf"), "ping_pong.rtt.hmip_rf"},
		{MetricKeys.RPCServerActiveTasks(), "rpc_server.active_tasks"},
		{MetricKeys.RPCServerError(), "rpc_server.error"},
		{MetricKeys.RPCServerRequest(), "rpc_server.request"},
		{MetricKeys.RPCServerRequestLatency(), "rpc_server.latency"},
		{MetricKeys.SelfHealingRecovery("hmip_rf"), "self_healing.recovery.hmip_rf"},
		{MetricKeys.SelfHealingRefreshFailure("hmip_rf"), "self_healing.refresh_failure.hmip_rf"},
		{MetricKeys.SelfHealingRefreshSuccess("hmip_rf"), "self_healing.refresh_success.hmip_rf"},
		{MetricKeys.SelfHealingTrip("hmip_rf"), "self_healing.trip.hmip_rf"},
		{MetricKeys.ServiceCall("set_value"), "service.call.set_value"},
		{MetricKeys.ServiceError("set_value"), "service.error.set_value"},
	}
	for _, tc := range cases {
		if got := tc.key.String(); got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}
