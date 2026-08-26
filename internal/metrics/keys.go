// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package metrics

import "fmt"

// MetricKey is a type-safe, comparable metric key.
//
// String form: {component}.{metric} or {component}.{metric}.{identifier}
// Optionally scoped to a CCU with CentralName (multi-CCU support).
type MetricKey struct {
	Component   string
	Metric      string
	Identifier  string // optional instance qualifier (e.g. interface_id)
	CentralName string // optional CCU scope; "" means unscoped / global
}

// String returns the full metric key string.
//
// Pattern: [central.]component.metric[.identifier]
func (k MetricKey) String() string {
	var base string
	if k.Identifier != "" {
		base = fmt.Sprintf("%s.%s.%s", k.Component, k.Metric, k.Identifier)
	} else {
		base = fmt.Sprintf("%s.%s", k.Component, k.Metric)
	}
	if k.CentralName != "" {
		return k.CentralName + "." + base
	}
	return base
}

// MatchesPrefix reports whether the key's String() has the given prefix.
func (k MetricKey) MatchesPrefix(prefix string) bool {
	s := k.String()
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// MetricKeys is a factory for all well-known metric keys.
var MetricKeys = metricKeyFactory{}

// metricKeyFactory groups all factory methods.
type metricKeyFactory struct{}

// Cache keys.

func (metricKeyFactory) CacheEviction(cacheName string) MetricKey {
	return MetricKey{Component: "cache", Metric: cacheName, Identifier: "eviction"}
}

func (metricKeyFactory) CacheHit(cacheName string) MetricKey {
	return MetricKey{Component: "cache", Metric: cacheName, Identifier: "hit"}
}

func (metricKeyFactory) CacheMiss(cacheName string) MetricKey {
	return MetricKey{Component: "cache", Metric: cacheName, Identifier: "miss"}
}

func (metricKeyFactory) CacheSize(cacheName string) MetricKey {
	return MetricKey{Component: "cache", Metric: cacheName, Identifier: "size"}
}

// Circuit breaker keys.

func (metricKeyFactory) CircuitFailure(interfaceID string) MetricKey {
	return MetricKey{Component: "circuit", Metric: "failure", Identifier: interfaceID}
}

func (metricKeyFactory) CircuitRejection(interfaceID string) MetricKey {
	return MetricKey{Component: "circuit", Metric: "rejection", Identifier: interfaceID}
}

func (metricKeyFactory) CircuitState(interfaceID string) MetricKey {
	return MetricKey{Component: "circuit", Metric: "state", Identifier: interfaceID}
}

func (metricKeyFactory) CircuitStateTransition(interfaceID string) MetricKey {
	return MetricKey{Component: "circuit", Metric: "state_transition", Identifier: interfaceID}
}

// Client health keys.

func (metricKeyFactory) ClientHealth(interfaceID string) MetricKey {
	return MetricKey{Component: "client", Metric: "health", Identifier: interfaceID}
}

// Coalescer keys.

func (metricKeyFactory) CoalescerCoalesced(interfaceID string) MetricKey {
	return MetricKey{Component: "coalescer", Metric: "coalesced", Identifier: interfaceID}
}

func (metricKeyFactory) CoalescerFailure(interfaceID string) MetricKey {
	return MetricKey{Component: "coalescer", Metric: "failure", Identifier: interfaceID}
}

// Handler keys.

func (metricKeyFactory) HandlerError(eventType string) MetricKey {
	return MetricKey{Component: "handler", Metric: "error", Identifier: eventType}
}

func (metricKeyFactory) HandlerExecution(eventType string) MetricKey {
	return MetricKey{Component: "handler", Metric: "execution", Identifier: eventType}
}

// Ping/pong keys.

func (metricKeyFactory) PingPongRTT(interfaceID string) MetricKey {
	return MetricKey{Component: "ping_pong", Metric: "rtt", Identifier: interfaceID}
}

// RPC server keys.

func (metricKeyFactory) RPCServerActiveTasks() MetricKey {
	return MetricKey{Component: "rpc_server", Metric: "active_tasks"}
}

func (metricKeyFactory) RPCServerError() MetricKey {
	return MetricKey{Component: "rpc_server", Metric: "error"}
}

func (metricKeyFactory) RPCServerRequest() MetricKey {
	return MetricKey{Component: "rpc_server", Metric: "request"}
}

func (metricKeyFactory) RPCServerRequestLatency() MetricKey {
	return MetricKey{Component: "rpc_server", Metric: "latency"}
}

// Self-healing keys.

func (metricKeyFactory) SelfHealingRecovery(interfaceID string) MetricKey {
	return MetricKey{Component: "self_healing", Metric: "recovery", Identifier: interfaceID}
}

func (metricKeyFactory) SelfHealingRefreshFailure(interfaceID string) MetricKey {
	return MetricKey{Component: "self_healing", Metric: "refresh_failure", Identifier: interfaceID}
}

func (metricKeyFactory) SelfHealingRefreshSuccess(interfaceID string) MetricKey {
	return MetricKey{Component: "self_healing", Metric: "refresh_success", Identifier: interfaceID}
}

func (metricKeyFactory) SelfHealingTrip(interfaceID string) MetricKey {
	return MetricKey{Component: "self_healing", Metric: "trip", Identifier: interfaceID}
}

// Service keys.

func (metricKeyFactory) ServiceCall(method string) MetricKey {
	return MetricKey{Component: "service", Metric: "call", Identifier: method}
}

func (metricKeyFactory) ServiceError(method string) MetricKey {
	return MetricKey{Component: "service", Metric: "error", Identifier: method}
}
