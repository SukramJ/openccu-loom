// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"time"
)

// RpcMetrics is an immutable snapshot of outbound RPC client metrics.
//
//nolint:revive // RpcMetrics mirrors the Python name
type RpcMetrics struct {
	TotalRequests           int        `json:"total_requests"`
	SuccessfulRequests      int        `json:"successful_requests"`
	FailedRequests          int        `json:"failed_requests"`
	RejectedRequests        int        `json:"rejected_requests"`
	CoalescedRequests       int        `json:"coalesced_requests"`
	ExecutedRequests        int        `json:"executed_requests"`
	PendingRequests         int        `json:"pending_requests"`
	CircuitBreakersOpen     int        `json:"circuit_breakers_open"`
	CircuitBreakersHalfOpen int        `json:"circuit_breakers_half_open"`
	StateTransitions        int        `json:"state_transitions"`
	AvgLatencyMs            float64    `json:"avg_latency_ms"`
	MaxLatencyMs            float64    `json:"max_latency_ms"`
	LastFailureTime         *time.Time `json:"last_failure_time,omitempty"`
}

// CoalesceRate returns coalesce rate as percentage.
func (r RpcMetrics) CoalesceRate() float64 {
	if r.TotalRequests == 0 {
		return 0.0
	}
	return float64(r.CoalescedRequests) / float64(r.TotalRequests) * 100.0
}

// FailureRate returns failure rate as percentage.
func (r RpcMetrics) FailureRate() float64 {
	if r.TotalRequests == 0 {
		return 0.0
	}
	return float64(r.FailedRequests) / float64(r.TotalRequests) * 100.0
}

// RejectionRate returns rejection rate as percentage.
func (r RpcMetrics) RejectionRate() float64 {
	if r.TotalRequests == 0 {
		return 0.0
	}
	return float64(r.RejectedRequests) / float64(r.TotalRequests) * 100.0
}

// SuccessRate returns success rate as percentage.
func (r RpcMetrics) SuccessRate() float64 {
	if r.TotalRequests == 0 {
		return 100.0
	}
	return float64(r.SuccessfulRequests) / float64(r.TotalRequests) * 100.0
}

// RpcServerMetrics is an immutable snapshot of inbound RPC server metrics.
//
//nolint:revive // RpcServerMetrics mirrors the Python name
type RpcServerMetrics struct {
	TotalRequests int     `json:"total_requests"`
	TotalErrors   int     `json:"total_errors"`
	ActiveTasks   int     `json:"active_tasks"`
	AvgLatencyMs  float64 `json:"avg_latency_ms"`
	MaxLatencyMs  float64 `json:"max_latency_ms"`
}

// ErrorRate returns error rate as percentage.
func (r RpcServerMetrics) ErrorRate() float64 {
	if r.TotalRequests == 0 {
		return 0.0
	}
	return float64(r.TotalErrors) / float64(r.TotalRequests) * 100.0
}

// SuccessRate returns success rate as percentage.
func (r RpcServerMetrics) SuccessRate() float64 {
	if r.TotalRequests == 0 {
		return 100.0
	}
	return float64(r.TotalRequests-r.TotalErrors) / float64(r.TotalRequests) * 100.0
}

// EventMetrics is an immutable snapshot of event bus metrics.
type EventMetrics struct {
	TotalPublished         int            `json:"total_published"`
	TotalSubscriptions     int            `json:"total_subscriptions"`
	HandlersExecuted       int            `json:"handlers_executed"`
	HandlerErrors          int            `json:"handler_errors"`
	AvgHandlerDurationMs   float64        `json:"avg_handler_duration_ms"`
	MaxHandlerDurationMs   float64        `json:"max_handler_duration_ms"`
	EventsByType           map[string]int `json:"events_by_type,omitempty"`
	CircuitBreakerTrips    int            `json:"circuit_breaker_trips"`
	StateChanges           int            `json:"state_changes"`
	DataRefreshesTriggered int            `json:"data_refreshes_triggered"`
	DataRefreshesCompleted int            `json:"data_refreshes_completed"`
	ProgramsExecuted       int            `json:"programs_executed"`
	RequestsCoalesced      int            `json:"requests_coalesced"`
	HealthRecords          int            `json:"health_records"`
}

// ErrorRate returns handler error rate as percentage.
func (e EventMetrics) ErrorRate() float64 {
	if e.HandlersExecuted == 0 {
		return 0.0
	}
	return float64(e.HandlerErrors) / float64(e.HandlersExecuted) * 100.0
}

// CacheMetricsSnapshot is an immutable snapshot of all cache statistics.
type CacheMetricsSnapshot struct {
	DeviceDescriptions   SizeOnlySnapshot   `json:"device_descriptions"`
	ParamsetDescriptions SizeOnlySnapshot   `json:"paramset_descriptions"`
	VisibilityRegistry   SizeOnlySnapshot   `json:"visibility_registry"`
	PingPongTracker      SizeOnlySnapshot   `json:"ping_pong_tracker"`
	CommandTracker       SizeOnlySnapshot   `json:"command_tracker"`
	DataCache            CacheStatsSnapshot `json:"data_cache"`
}

// OverallHitRate returns the data cache hit rate (100.0 if no samples).
func (c CacheMetricsSnapshot) OverallHitRate() float64 {
	return c.DataCache.HitRate()
}

// TotalEntries returns the sum across all caches and registries.
func (c CacheMetricsSnapshot) TotalEntries() int {
	return c.DeviceDescriptions.Size +
		c.ParamsetDescriptions.Size +
		c.VisibilityRegistry.Size +
		c.PingPongTracker.Size +
		c.CommandTracker.Size +
		c.DataCache.Size
}

// HealthMetrics is an immutable snapshot of connection health.
type HealthMetrics struct {
	OverallScore      float64    `json:"overall_score"`
	ClientsTotal      int        `json:"clients_total"`
	ClientsHealthy    int        `json:"clients_healthy"`
	ClientsDegraded   int        `json:"clients_degraded"`
	ClientsFailed     int        `json:"clients_failed"`
	ReconnectAttempts int        `json:"reconnect_attempts"`
	LastEventTime     *time.Time `json:"last_event_time,omitempty"`
}

// AvailabilityRate returns client availability as percentage.
func (h HealthMetrics) AvailabilityRate() float64 {
	if h.ClientsTotal == 0 {
		return 100.0
	}
	return float64(h.ClientsHealthy) / float64(h.ClientsTotal) * 100.0
}

// RecoveryMetrics is an immutable snapshot of recovery statistics.
type RecoveryMetrics struct {
	AttemptsTotal     int        `json:"attempts_total"`
	Successes         int        `json:"successes"`
	Failures          int        `json:"failures"`
	MaxRetriesReached int        `json:"max_retries_reached"`
	InProgress        bool       `json:"in_progress"`
	LastRecoveryTime  *time.Time `json:"last_recovery_time,omitempty"`
}

// SuccessRate returns recovery success rate as percentage.
func (r RecoveryMetrics) SuccessRate() float64 {
	if r.AttemptsTotal == 0 {
		return 100.0
	}
	return float64(r.Successes) / float64(r.AttemptsTotal) * 100.0
}

// ModelMetrics is an immutable snapshot of domain model statistics.
type ModelMetrics struct {
	DevicesTotal         int            `json:"devices_total"`
	DevicesAvailable     int            `json:"devices_available"`
	ChannelsTotal        int            `json:"channels_total"`
	DataPointsGeneric    int            `json:"data_points_generic"`
	DataPointsCustom     int            `json:"data_points_custom"`
	DataPointsCalculated int            `json:"data_points_calculated"`
	DataPointsSubscribed int            `json:"data_points_subscribed"`
	DataPointsByCategory map[string]int `json:"data_points_by_category,omitempty"`
	ProgramsTotal        int            `json:"programs_total"`
	SysvarsTotal         int            `json:"sysvars_total"`
}

// ServiceMetricsSnapshot is an immutable snapshot of service call statistics.
type ServiceMetricsSnapshot struct {
	TotalCalls    int                             `json:"total_calls"`
	TotalErrors   int                             `json:"total_errors"`
	AvgDurationMs float64                         `json:"avg_duration_ms"`
	MaxDurationMs float64                         `json:"max_duration_ms"`
	ByMethod      map[string]ServiceStatsSnapshot `json:"by_method,omitempty"`
}

// ErrorRate returns the overall error rate as percentage.
func (s ServiceMetricsSnapshot) ErrorRate() float64 {
	if s.TotalCalls == 0 {
		return 0.0
	}
	return float64(s.TotalErrors) / float64(s.TotalCalls) * 100.0
}

// MetricsSnapshot is a point-in-time snapshot of all system metrics.
//
//nolint:revive // MetricsSnapshot name mirrors the Python class
type MetricsSnapshot struct {
	Timestamp time.Time              `json:"timestamp"`
	RPC       RpcMetrics             `json:"rpc"`
	RPCServer RpcServerMetrics       `json:"rpc_server"`
	Events    EventMetrics           `json:"events"`
	Cache     CacheMetricsSnapshot   `json:"cache"`
	Health    HealthMetrics          `json:"health"`
	Recovery  RecoveryMetrics        `json:"recovery"`
	Model     ModelMetrics           `json:"model"`
	Services  ServiceMetricsSnapshot `json:"services"`
}
