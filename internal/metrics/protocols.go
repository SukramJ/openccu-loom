// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package metrics

import "time"

// Provider interfaces used by Aggregator to pull metrics from system
// components. Defined here (not in pkg/interfaces) to avoid circular imports.
//
// The concrete implementations live in internal/metrics/wiring, which adapts
// the components a central owns onto these interfaces.

// ClientForMetrics is the minimal interface needed to collect RPC metrics
// from an interface client.
type ClientForMetrics interface {
	// Clients returns all connected interface clients.
	Clients() []InterfaceClientMetrics
}

// InterfaceClientMetrics exposes per-client counters read directly
// (no event emission) by the Aggregator.
type InterfaceClientMetrics interface {
	// TotalRequests returns the total number of outbound RPC requests.
	TotalRequests() int
	// PendingRequests returns the number of currently in-flight requests.
	PendingRequests() int
	// ExecutedRequests returns the number of requests that actually executed
	// (not coalesced).
	ExecutedRequests() int
	// CircuitState returns 0=closed, 1=open, 2=half-open.
	CircuitState() int
	// LastFailureTime returns the wall time of the most recent failure; nil
	// if no failure has occurred.
	LastFailureTime() *any //nolint:gocritic // *time.Time boxed as *interface{} to avoid import cycle; callers cast
	// CommandTrackerSize returns the number of entries currently held in
	// this client's optimistic-update command tracker.
	CommandTrackerSize() int
	// PingPongSize returns the number of pending/unknown entries currently
	// held in this client's ping/pong tracker.
	PingPongSize() int
}

// DeviceForMetrics is the minimal interface needed to collect model metrics.
type DeviceForMetrics interface {
	// Devices returns all registered devices. Each element must expose
	// the device-level reflection used by Aggregator.Model().
	Devices() []DeviceMetrics
}

// DeviceMetrics exposes the per-device data Aggregator needs.
type DeviceMetrics interface {
	// Available reports whether the device is reachable.
	Available() bool
	// ChannelCount returns the number of channels.
	ChannelCount() int
	// DataPointCounts returns (generic, custom, calculated) counts.
	DataPointCounts() (generic, custom, calculated int)
	// DataPointsByCategory returns a map of category-name → count.
	DataPointsByCategory() map[string]int
}

// HubDataPointManagerForMetrics is the minimal interface for hub entity counts.
type HubDataPointManagerForMetrics interface {
	// ProgramCount returns the number of CCU programs.
	ProgramCount() int
	// SysvarCount returns the number of system variables.
	SysvarCount() int
}

// CacheProviderForMetrics is the minimal interface for cache statistics.
type CacheProviderForMetrics interface {
	// DataCacheSize returns current data cache entry count.
	DataCacheSize() int
	// DataCacheStats returns a snapshot of the data cache hit/miss stats.
	DataCacheStats() CacheStatsSnapshot
	// DeviceDescriptionsSize returns the device description registry size.
	DeviceDescriptionsSize() int
	// ParamsetDescriptionsSize returns the paramset description registry size.
	ParamsetDescriptionsSize() int
	// VisibilityCacheSize returns the visibility rule memoization cache size.
	VisibilityCacheSize() int
}

// RecoveryProviderForMetrics is the minimal interface for recovery statistics.
type RecoveryProviderForMetrics interface {
	// InRecovery reports whether any interface is currently being recovered.
	InRecovery() bool
	// RecoveryStates returns the per-interface recovery states. Each value
	// must implement RecoveryStateMetrics.
	RecoveryStates() map[string]RecoveryStateMetrics
}

// RecoveryStateMetrics exposes per-interface recovery state data.
type RecoveryStateMetrics interface {
	// AttemptCount returns total recovery attempts.
	AttemptCount() int
	// ConsecutiveFailures returns the current consecutive failure streak.
	ConsecutiveFailures() int
	// CanRetry reports whether further retries are permitted.
	CanRetry() bool
}

// RecoveryStateTimestamps is the optional half of RecoveryStateMetrics: a
// state source that can date its last recovery attempt implements it, and
// Aggregator.Recovery then reports the newest one as LastRecoveryTime.
//
// It is separate from RecoveryStateMetrics because the counter half is
// satisfied by a plain value snapshot, while the timestamp has to be read
// from the live coordinator — the adapter in internal/metrics/wiring joins
// the two. Without this the aggregator declared a LastRecoveryTime it had no
// way to fill, so "when did recovery last run" answered nothing forever.
type RecoveryStateTimestamps interface {
	// LastAttempt returns the wall time of the most recent recovery attempt.
	// The zero value means no attempt has been recorded.
	LastAttempt() time.Time
}

// EventBusForMetrics is the minimal event bus interface needed by the
// Aggregator to read operational counters.
type EventBusForMetrics interface {
	// EventStats returns a map of event type name → publish count.
	EventStats() map[string]int
	// TotalSubscriptionCount returns the number of active subscriptions.
	TotalSubscriptionCount() int
	// HandlerStats returns a per-event-type snapshot of handler execution
	// counters (matches/calls). Duration and error fields are populated by
	// adapter implementations that have access to observer data; the bus
	// itself only surfaces Executed counts.
	HandlerStats() map[string]HandlerStatSnapshot
}

// HandlerStatSnapshot is a per-event-type aggregation of handler execution
// counters. It is keyed by event type name in the map returned by
// EventBusForMetrics.HandlerStats.
type HandlerStatSnapshot struct {
	// Executed is the total number of times handlers for this event type
	// were actually invoked (i.e. passed the key filter).
	Executed int
	// Errors is the total number of handler invocations that returned an
	// error or panicked. Populated from observer data where available.
	Errors int
	// AvgDurationMs is the rolling average handler execution time in
	// milliseconds. Populated from observer data where available.
	AvgDurationMs float64
	// MaxDurationMs is the maximum handler execution time in milliseconds
	// seen so far. Populated from observer data where available.
	MaxDurationMs float64
}

// HealthTrackerForMetrics is the minimal interface for health data.
type HealthTrackerForMetrics interface {
	// HealthSummary returns the current health summary.
	HealthSummary() HealthSummary
}

// HealthSummary carries the health snapshot the Aggregator needs.
type HealthSummary struct {
	OverallScore      float64
	ClientsHealthy    int
	ClientsDegraded   int
	ClientsFailed     int
	ReconnectAttempts int
}
