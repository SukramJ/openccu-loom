// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"context"
	"sync"
	"time"
)

// Observer accumulates metric events published on the event bus and
// provides query methods for the Aggregator.
//
// Components call Observe* methods (or use the emitter helpers) to feed
// data in. Aggregator.Snapshot calls read back aggregated views.
//
// Thread-safe: all methods may be called concurrently.
type Observer struct {
	mu       sync.RWMutex
	counters map[string]int64
	gauges   map[string]float64
	latency  map[string]*LatencyStats
	service  map[string]*ServiceStats
}

// NewObserver allocates a zeroed Observer.
func NewObserver() *Observer {
	return &Observer{
		counters: make(map[string]int64),
		gauges:   make(map[string]float64),
		latency:  make(map[string]*LatencyStats),
		service:  make(map[string]*ServiceStats),
	}
}

// ObserveLatency records a latency sample for key.
func (o *Observer) ObserveLatency(key string, durationMs float64) {
	o.mu.Lock()
	l, ok := o.latency[key]
	if !ok {
		l = NewLatencyStats()
		o.latency[key] = l
	}
	o.mu.Unlock()
	l.Record(durationMs)
}

// ObserveCounter adds delta to the counter for key.
func (o *Observer) ObserveCounter(key string, delta int64) {
	o.mu.Lock()
	o.counters[key] += delta
	o.mu.Unlock()
}

// ObserveGauge sets the gauge for key.
func (o *Observer) ObserveGauge(key string, value float64) {
	o.mu.Lock()
	o.gauges[key] = value
	o.mu.Unlock()
}

// ObserveService records a service call (method, duration, hadError).
func (o *Observer) ObserveService(method string, durationMs float64, hadError bool) {
	o.mu.Lock()
	s, ok := o.service[method]
	if !ok {
		s = &ServiceStats{}
		o.service[method] = s
	}
	o.mu.Unlock()
	s.Record(durationMs, hadError)
}

// GetCounter returns the cumulative counter value for key (0 if unknown).
func (o *Observer) GetCounter(key string) int64 {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.counters[key]
}

// GetGauge returns the latest gauge value for key (0 if unknown).
func (o *Observer) GetGauge(key string) float64 {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.gauges[key]
}

// GetLatency returns the LatencyStatsSnapshot for key.
// ok is false if no samples have been recorded yet.
func (o *Observer) GetLatency(key string) (LatencyStatsSnapshot, bool) {
	o.mu.RLock()
	l, ok := o.latency[key]
	o.mu.RUnlock()
	if !ok {
		return LatencyStatsSnapshot{}, false
	}
	return l.Snapshot(), true
}

// AggregatedCounter sums all counters whose key has the given prefix.
func (o *Observer) AggregatedCounter(prefix string) int64 {
	o.mu.RLock()
	defer o.mu.RUnlock()
	var sum int64
	for k, v := range o.counters {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			sum += v
		}
	}
	return sum
}

// AggregatedLatency returns a merged LatencyStatsSnapshot for all keys
// matching prefix.
func (o *Observer) AggregatedLatency(prefix string) LatencyStatsSnapshot {
	o.mu.RLock()
	defer o.mu.RUnlock()
	var (
		count   int
		totalMs float64
		maxMs   float64
	)
	for k, l := range o.latency {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			snap := l.Snapshot()
			count += snap.Count
			totalMs += snap.TotalMs
			if snap.MaxMs > maxMs {
				maxMs = snap.MaxMs
			}
		}
	}
	return LatencyStatsSnapshot{Count: count, TotalMs: totalMs, MaxMs: maxMs}
}

// ServiceSnapshot returns a snapshot of per-method service stats.
func (o *Observer) ServiceSnapshot() map[string]ServiceStatsSnapshot {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make(map[string]ServiceStatsSnapshot, len(o.service))
	for k, s := range o.service {
		out[k] = s.Snapshot()
	}
	return out
}

// KeysByPrefix returns all counter/latency keys that start with prefix.
func (o *Observer) KeysByPrefix(prefix string) []string {
	o.mu.RLock()
	defer o.mu.RUnlock()
	seen := make(map[string]struct{})
	for k := range o.counters {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			seen[k] = struct{}{}
		}
	}
	for k := range o.latency {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			seen[k] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

// -----------------------------------------------------------------------
// Aggregator
// -----------------------------------------------------------------------

// Aggregator collects metrics from various system components and exposes
// typed snapshots. One Aggregator is created per Unit.
//
// All provider fields are optional. Nil providers cause the corresponding
// snapshot section to return zero-value structs. The concrete providers
// live in internal/metrics/wiring and are attached per central by the
// daemon's boot wiring.
type Aggregator struct {
	centralName      string
	observer         *Observer
	clientProvider   ClientForMetrics              // optional
	deviceProvider   DeviceForMetrics              // optional
	hubManager       HubDataPointManagerForMetrics // optional
	cacheProvider    CacheProviderForMetrics       // optional
	recoveryProvider RecoveryProviderForMetrics    // optional
	eventBus         EventBusForMetrics            // optional
	healthTracker    HealthTrackerForMetrics       // optional
}

// AggregatorOption configures an Aggregator.
type AggregatorOption func(*Aggregator)

// WithClientProvider wires the client metrics provider.
func WithClientProvider(p ClientForMetrics) AggregatorOption {
	return func(a *Aggregator) { a.clientProvider = p }
}

// WithDeviceProvider wires the device metrics provider.
func WithDeviceProvider(p DeviceForMetrics) AggregatorOption {
	return func(a *Aggregator) { a.deviceProvider = p }
}

// WithHubManager wires the hub data point manager.
func WithHubManager(p HubDataPointManagerForMetrics) AggregatorOption {
	return func(a *Aggregator) { a.hubManager = p }
}

// WithCacheProvider wires the cache statistics provider.
func WithCacheProvider(p CacheProviderForMetrics) AggregatorOption {
	return func(a *Aggregator) { a.cacheProvider = p }
}

// WithRecoveryProvider wires the recovery statistics provider.
func WithRecoveryProvider(p RecoveryProviderForMetrics) AggregatorOption {
	return func(a *Aggregator) { a.recoveryProvider = p }
}

// WithEventBus wires the event bus statistics source.
func WithEventBus(b EventBusForMetrics) AggregatorOption {
	return func(a *Aggregator) { a.eventBus = b }
}

// WithHealthTracker wires the health tracker.
func WithHealthTracker(h HealthTrackerForMetrics) AggregatorOption {
	return func(a *Aggregator) { a.healthTracker = h }
}

// NewAggregator constructs an Aggregator for centralName.
// observer must not be nil; use NewObserver() if no shared observer exists.
func NewAggregator(centralName string, observer *Observer, opts ...AggregatorOption) *Aggregator {
	a := &Aggregator{centralName: centralName, observer: observer}
	for _, o := range opts {
		o(a)
	}
	return a
}

// CentralName returns the CCU name this aggregator is scoped to.
func (a *Aggregator) CentralName() string { return a.centralName }

// RPC returns aggregated outbound RPC metrics.
func (a *Aggregator) RPC() RpcMetrics {
	failedRequests := a.observer.AggregatedCounter("circuit.failure.")
	rejectedRequests := a.observer.AggregatedCounter("circuit.rejection.")
	stateTransitions := a.observer.AggregatedCounter("circuit.state_transition.")
	coalescedRequests := a.observer.AggregatedCounter("coalescer.coalesced.")

	latency := a.observer.AggregatedLatency("ping_pong.rtt")
	avgLatencyMs := latency.AvgMs()

	var (
		totalRequests           int
		executedRequests        int
		pendingRequests         int
		circuitBreakersOpen     int
		circuitBreakersHalfOpen int
		lastFailureTime         *time.Time
	)

	if a.clientProvider != nil {
		for _, c := range a.clientProvider.Clients() {
			totalRequests += c.TotalRequests()
			pendingRequests += c.PendingRequests()
			executedRequests += c.ExecutedRequests()
			switch c.CircuitState() {
			case 1:
				circuitBreakersOpen++
			case 2:
				circuitBreakersHalfOpen++
			}
			// LastFailureTime returns *interface{} to avoid the time import in
			// the protocol definition. Callers in production will store *time.Time.
			if ft := c.LastFailureTime(); ft != nil {
				if t, ok := (*ft).(time.Time); ok {
					if lastFailureTime == nil || t.After(*lastFailureTime) {
						cp := t
						lastFailureTime = &cp
					}
				}
			}
		}
	}

	successfulRequests := max(totalRequests-int(failedRequests)-int(rejectedRequests), 0)

	return RpcMetrics{
		TotalRequests:           totalRequests,
		SuccessfulRequests:      successfulRequests,
		FailedRequests:          int(failedRequests),
		RejectedRequests:        int(rejectedRequests),
		CoalescedRequests:       int(coalescedRequests),
		ExecutedRequests:        executedRequests,
		PendingRequests:         pendingRequests,
		CircuitBreakersOpen:     circuitBreakersOpen,
		CircuitBreakersHalfOpen: circuitBreakersHalfOpen,
		StateTransitions:        int(stateTransitions),
		AvgLatencyMs:            avgLatencyMs,
		MaxLatencyMs:            latency.MaxMs,
		LastFailureTime:         lastFailureTime,
	}
}

// RPCServer returns inbound RPC server metrics from the observer.
func (a *Aggregator) RPCServer() RpcServerMetrics {
	totalRequests := int(a.observer.GetCounter(MetricKeys.RPCServerRequest().String()))
	totalErrors := int(a.observer.GetCounter(MetricKeys.RPCServerError().String()))
	activeTasks := int(a.observer.GetGauge(MetricKeys.RPCServerActiveTasks().String()))

	latency, _ := a.observer.GetLatency(MetricKeys.RPCServerRequestLatency().String())

	return RpcServerMetrics{
		TotalRequests: totalRequests,
		TotalErrors:   totalErrors,
		ActiveTasks:   activeTasks,
		AvgLatencyMs:  latency.AvgMs(),
		MaxLatencyMs:  latency.MaxMs,
	}
}

// Events returns event bus metrics.
//
// HandlersExecuted, HandlerErrors, AvgHandlerDurationMs and
// MaxHandlerDurationMs are populated from the bus's HandlerStats()
// snapshot (which aggregates per-event-type match counts), combined with
// observer-recorded latency and error counters for duration/error data.
// This mirrors
// metrics/aggregator.py.
func (a *Aggregator) Events() EventMetrics {
	if a.eventBus == nil {
		return EventMetrics{}
	}
	stats := a.eventBus.EventStats()
	sub := a.eventBus.TotalSubscriptionCount()
	handlerStats := a.eventBus.HandlerStats()

	// Sum executed counts and aggregate duration/error fields from the
	// per-event-type snapshots returned by the bus. Duration and error
	// data are filled in by the wiring adapter from observer data; the
	// raw bus only supplies Executed.
	var (
		handlersExecuted     int
		handlerErrors        int
		totalDurationMs      float64
		maxHandlerDurationMs float64
	)
	for _, hs := range handlerStats {
		handlersExecuted += hs.Executed
		handlerErrors += hs.Errors
		totalDurationMs += hs.AvgDurationMs * float64(hs.Executed)
		if hs.MaxDurationMs > maxHandlerDurationMs {
			maxHandlerDurationMs = hs.MaxDurationMs
		}
	}

	// Fall back to observer-aggregated latency when the bus snapshot does
	// not carry duration (i.e. Executed > 0 but all AvgDurationMs == 0).
	// This preserves backward compatibility with observer-only wiring.
	handlerLatency := a.observer.AggregatedLatency("handler.execution.")
	if handlerLatency.Count > 0 && totalDurationMs == 0 {
		handlersExecuted = handlerLatency.Count
		totalDurationMs = handlerLatency.TotalMs
		maxHandlerDurationMs = handlerLatency.MaxMs
	}

	// Use observer error counters as the authoritative error source when
	// the bus snapshot carries no error data.
	observerErrors := int(a.observer.AggregatedCounter("handler.error."))
	if handlerErrors == 0 && observerErrors > 0 {
		handlerErrors = observerErrors
	}

	avgHandlerDurationMs := 0.0
	if handlersExecuted > 0 && totalDurationMs > 0 {
		avgHandlerDurationMs = totalDurationMs / float64(handlersExecuted)
	}

	return EventMetrics{
		TotalPublished:       sumMapValues(stats),
		TotalSubscriptions:   sub,
		HandlersExecuted:     handlersExecuted,
		HandlerErrors:        handlerErrors,
		AvgHandlerDurationMs: avgHandlerDurationMs,
		MaxHandlerDurationMs: maxHandlerDurationMs,
		EventsByType:         stats,
		CircuitBreakerTrips:  stats["client.circuit_breaker_state_changed"],
		StateChanges:         stats["client.state_changed"] + stats["central.state_changed"],
		// The scheduler's background refresh job publishes both halves
		// on the same bus as every counter above. Leaving them unread
		// made the dump report "no background refresh ever ran" next to
		// sibling fields carrying real counts.
		DataRefreshesTriggered: stats["scheduler.refresh_triggered"],
		DataRefreshesCompleted: stats["scheduler.refresh_completed"],
		ProgramsExecuted:       stats["hub.program_executed"],
		RequestsCoalesced:      stats["client.request_coalesced"],
		HealthRecords:          stats["health.recorded"],
	}
}

// Cache returns cache statistics.
func (a *Aggregator) Cache() CacheMetricsSnapshot {
	snap := CacheMetricsSnapshot{}
	if a.cacheProvider == nil {
		return snap
	}

	dbSnap := a.cacheProvider.DataCacheStats()
	snap.DataCache = dbSnap
	snap.DeviceDescriptions = SizeOnlySnapshot{Size: a.cacheProvider.DeviceDescriptionsSize()}
	snap.ParamsetDescriptions = SizeOnlySnapshot{Size: a.cacheProvider.ParamsetDescriptionsSize()}
	snap.VisibilityRegistry = SizeOnlySnapshot{Size: a.cacheProvider.VisibilityCacheSize()}

	if a.clientProvider != nil {
		var cmdSize, ppSize int
		for _, c := range a.clientProvider.Clients() {
			cmdSize += c.CommandTrackerSize()
			ppSize += c.PingPongSize()
		}
		// No cumulative eviction counter is exposed by CommandTracker today;
		// Evictions stays at its zero value until that source exists.
		snap.CommandTracker = SizeOnlySnapshot{Size: cmdSize}
		snap.PingPongTracker = SizeOnlySnapshot{Size: ppSize}
	}
	return snap
}

// Health returns health metrics.
func (a *Aggregator) Health() HealthMetrics {
	if a.healthTracker == nil {
		return HealthMetrics{OverallScore: 1.0}
	}
	s := a.healthTracker.HealthSummary()
	return HealthMetrics{
		OverallScore:      s.OverallScore,
		ClientsTotal:      s.ClientsHealthy + s.ClientsDegraded + s.ClientsFailed,
		ClientsHealthy:    s.ClientsHealthy,
		ClientsDegraded:   s.ClientsDegraded,
		ClientsFailed:     s.ClientsFailed,
		ReconnectAttempts: s.ReconnectAttempts,
	}
}

// Recovery returns recovery statistics.
func (a *Aggregator) Recovery() RecoveryMetrics {
	if a.recoveryProvider == nil {
		return RecoveryMetrics{}
	}
	states := a.recoveryProvider.RecoveryStates()
	if len(states) == 0 {
		return RecoveryMetrics{InProgress: a.recoveryProvider.InRecovery()}
	}

	var (
		attemptsTotal     int
		successes         int
		failures          int
		maxRetriesReached int
		lastRecoveryTime  *time.Time
	)
	for _, st := range states {
		attemptsTotal += st.AttemptCount()
		failures += st.ConsecutiveFailures()
		if st.AttemptCount() > st.ConsecutiveFailures() {
			successes += st.AttemptCount() - st.ConsecutiveFailures()
		}
		if !st.CanRetry() {
			maxRetriesReached++
		}
	}

	return RecoveryMetrics{
		AttemptsTotal:     attemptsTotal,
		Successes:         successes,
		Failures:          failures,
		MaxRetriesReached: maxRetriesReached,
		InProgress:        a.recoveryProvider.InRecovery(),
		LastRecoveryTime:  lastRecoveryTime,
	}
}

// Model returns domain model statistics.
func (a *Aggregator) Model() ModelMetrics {
	if a.deviceProvider == nil {
		return ModelMetrics{}
	}
	devices := a.deviceProvider.Devices()

	var (
		available  int
		channels   int
		generic    int
		custom     int
		calculated int
		byCategory = make(map[string]int)
	)
	for _, d := range devices {
		if d.Available() {
			available++
		}
		channels += d.ChannelCount()
		g, cu, ca := d.DataPointCounts()
		generic += g
		custom += cu
		calculated += ca
		for cat, n := range d.DataPointsByCategory() {
			byCategory[cat] += n
		}
	}

	var programs, sysvars int
	if a.hubManager != nil {
		programs = a.hubManager.ProgramCount()
		sysvars = a.hubManager.SysvarCount()
	}

	subscribed := 0
	if a.eventBus != nil {
		subscribed = a.eventBus.TotalSubscriptionCount()
	}

	return ModelMetrics{
		DevicesTotal:         len(devices),
		DevicesAvailable:     available,
		ChannelsTotal:        channels,
		DataPointsGeneric:    generic,
		DataPointsCustom:     custom,
		DataPointsCalculated: calculated,
		DataPointsSubscribed: subscribed,
		DataPointsByCategory: byCategory,
		ProgramsTotal:        programs,
		SysvarsTotal:         sysvars,
	}
}

// Services returns service call statistics from the observer.
func (a *Aggregator) Services() ServiceMetricsSnapshot {
	byMethod := a.observer.ServiceSnapshot()
	if len(byMethod) == 0 {
		return ServiceMetricsSnapshot{}
	}

	var totalCalls, totalErrors int
	var totalDuration, maxDuration float64
	for _, s := range byMethod {
		totalCalls += s.CallCount
		totalErrors += s.ErrorCount
		totalDuration += s.TotalDurationMs
		if s.MaxDurationMs > maxDuration {
			maxDuration = s.MaxDurationMs
		}
	}

	avgDuration := 0.0
	if totalCalls > 0 {
		avgDuration = totalDuration / float64(totalCalls)
	}

	return ServiceMetricsSnapshot{
		TotalCalls:    totalCalls,
		TotalErrors:   totalErrors,
		AvgDurationMs: avgDuration,
		MaxDurationMs: maxDuration,
		ByMethod:      byMethod,
	}
}

// Snapshot returns a point-in-time MetricsSnapshot.
func (a *Aggregator) Snapshot(_ context.Context) MetricsSnapshot {
	return MetricsSnapshot{
		Timestamp: time.Now(),
		RPC:       a.RPC(),
		RPCServer: a.RPCServer(),
		Events:    a.Events(),
		Cache:     a.Cache(),
		Health:    a.Health(),
		Recovery:  a.Recovery(),
		Model:     a.Model(),
		Services:  a.Services(),
	}
}

// sumMapValues sums all int values of a map.
func sumMapValues(m map[string]int) int {
	var sum int
	for _, v := range m {
		sum += v
	}
	return sum
}
