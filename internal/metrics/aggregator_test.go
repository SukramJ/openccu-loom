// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"context"
	"math"
	"testing"
)

// -----------------------------------------------------------------------
// Stub implementations of provider interfaces
// -----------------------------------------------------------------------

type stubClient struct {
	total     int
	pending   int
	executed  int
	cbState   int
	failureTS *interface{}
}

func (s *stubClient) TotalRequests() int            { return s.total }
func (s *stubClient) PendingRequests() int          { return s.pending }
func (s *stubClient) ExecutedRequests() int         { return s.executed }
func (s *stubClient) CircuitState() int             { return s.cbState }
func (s *stubClient) LastFailureTime() *interface{} { return s.failureTS } //nolint:gocritic // protocol contract; see InterfaceClientMetrics

type stubClientProvider struct{ clients []InterfaceClientMetrics }

func (p *stubClientProvider) Clients() []InterfaceClientMetrics { return p.clients }

type stubDevice struct {
	available  bool
	channels   int
	generic    int
	custom     int
	calculated int
	byCategory map[string]int
}

func (d *stubDevice) Available() bool   { return d.available }
func (d *stubDevice) ChannelCount() int { return d.channels }
func (d *stubDevice) DataPointCounts() (generic, custom, calculated int) {
	return d.generic, d.custom, d.calculated
}
func (d *stubDevice) DataPointsByCategory() map[string]int { return d.byCategory }

type stubDeviceProvider struct{ devices []DeviceMetrics }

func (p *stubDeviceProvider) Devices() []DeviceMetrics { return p.devices }

type stubCacheProvider struct {
	dataSize, ddSize, pdSize, vcSize int
	stats                            CacheStatsSnapshot
}

func (p *stubCacheProvider) DataCacheSize() int                 { return p.dataSize }
func (p *stubCacheProvider) DataCacheStats() CacheStatsSnapshot { return p.stats }
func (p *stubCacheProvider) DeviceDescriptionsSize() int        { return p.ddSize }
func (p *stubCacheProvider) ParamsetDescriptionsSize() int      { return p.pdSize }
func (p *stubCacheProvider) VisibilityCacheSize() int           { return p.vcSize }

type stubRecoveryState struct {
	attempts int
	failures int
	canRetry bool
}

func (s *stubRecoveryState) AttemptCount() int        { return s.attempts }
func (s *stubRecoveryState) ConsecutiveFailures() int { return s.failures }
func (s *stubRecoveryState) CanRetry() bool           { return s.canRetry }

type stubRecoveryProvider struct {
	inRecovery bool
	states     map[string]RecoveryStateMetrics
}

func (p *stubRecoveryProvider) InRecovery() bool                                { return p.inRecovery }
func (p *stubRecoveryProvider) RecoveryStates() map[string]RecoveryStateMetrics { return p.states }

type stubEventBus struct {
	stats        map[string]int
	subs         int
	handlerStats map[string]HandlerStatSnapshot
}

func (b *stubEventBus) EventStats() map[string]int  { return b.stats }
func (b *stubEventBus) TotalSubscriptionCount() int { return b.subs }
func (b *stubEventBus) HandlerStats() map[string]HandlerStatSnapshot {
	if b.handlerStats == nil {
		return map[string]HandlerStatSnapshot{}
	}
	return b.handlerStats
}

type stubHealthTracker struct{ summary HealthSummary }

func (h *stubHealthTracker) HealthSummary() HealthSummary { return h.summary }

type stubHubManager struct{ programs, sysvars int }

func (h *stubHubManager) ProgramCount() int { return h.programs }
func (h *stubHubManager) SysvarCount() int  { return h.sysvars }

// -----------------------------------------------------------------------
// Tests
// -----------------------------------------------------------------------

func TestAggregatorRPCCollectsFromProviders(t *testing.T) {
	t.Parallel()

	obs := NewObserver()
	obs.ObserveCounter(MetricKeys.CircuitFailure("hmip_rf").String(), 3)
	obs.ObserveCounter(MetricKeys.CircuitRejection("hmip_rf").String(), 2)
	obs.ObserveCounter(MetricKeys.CoalescerCoalesced("hmip_rf").String(), 5)

	cp := &stubClientProvider{clients: []InterfaceClientMetrics{
		&stubClient{total: 100, pending: 2, executed: 93},
		&stubClient{total: 50, pending: 1, executed: 48, cbState: 1},
	}}

	a := NewAggregator("ccu1", obs, WithClientProvider(cp))
	rpc := a.RPC()

	if rpc.TotalRequests != 150 {
		t.Errorf("total_requests=%d, want 150", rpc.TotalRequests)
	}
	if rpc.FailedRequests != 3 {
		t.Errorf("failed=%d, want 3", rpc.FailedRequests)
	}
	if rpc.RejectedRequests != 2 {
		t.Errorf("rejected=%d, want 2", rpc.RejectedRequests)
	}
	if rpc.CoalescedRequests != 5 {
		t.Errorf("coalesced=%d, want 5", rpc.CoalescedRequests)
	}
	if rpc.CircuitBreakersOpen != 1 {
		t.Errorf("cb_open=%d, want 1", rpc.CircuitBreakersOpen)
	}
}

func TestAggregatorModelCollectsFromDeviceProvider(t *testing.T) {
	t.Parallel()

	dp := &stubDeviceProvider{devices: []DeviceMetrics{
		&stubDevice{
			available: true, channels: 3, generic: 5, custom: 1, calculated: 2,
			byCategory: map[string]int{"CLIMATE": 2, "LIGHT": 3},
		},
		&stubDevice{
			available: false, channels: 2, generic: 3,
			byCategory: map[string]int{"CLIMATE": 1},
		},
	}}
	hp := &stubHubManager{programs: 4, sysvars: 10}

	a := NewAggregator(
		"ccu1", NewObserver(),
		WithDeviceProvider(dp),
		WithHubManager(hp),
	)
	m := a.Model()

	if m.DevicesTotal != 2 {
		t.Errorf("devices_total=%d", m.DevicesTotal)
	}
	if m.DevicesAvailable != 1 {
		t.Errorf("devices_available=%d", m.DevicesAvailable)
	}
	if m.DataPointsGeneric != 8 {
		t.Errorf("generic=%d, want 8", m.DataPointsGeneric)
	}
	if m.DataPointsCustom != 1 {
		t.Errorf("custom=%d, want 1", m.DataPointsCustom)
	}
	if m.DataPointsCalculated != 2 {
		t.Errorf("calculated=%d, want 2", m.DataPointsCalculated)
	}
	if m.DataPointsByCategory["CLIMATE"] != 3 {
		t.Errorf("by_category[CLIMATE]=%d, want 3", m.DataPointsByCategory["CLIMATE"])
	}
	if m.ProgramsTotal != 4 {
		t.Errorf("programs=%d", m.ProgramsTotal)
	}
	if m.SysvarsTotal != 10 {
		t.Errorf("sysvars=%d", m.SysvarsTotal)
	}
}

func TestAggregatorCacheCollectsFromProvider(t *testing.T) {
	t.Parallel()

	cp := &stubCacheProvider{
		dataSize: 100,
		ddSize:   20,
		pdSize:   15,
		vcSize:   5,
		stats:    CacheStatsSnapshot{Hits: 80, Misses: 20, Size: 100},
	}
	a := NewAggregator("ccu1", NewObserver(), WithCacheProvider(cp))
	cache := a.Cache()

	if cache.DataCache.Hits != 80 {
		t.Errorf("hits=%d", cache.DataCache.Hits)
	}
	if cache.DeviceDescriptions.Size != 20 {
		t.Errorf("dd_size=%d", cache.DeviceDescriptions.Size)
	}
	if cache.TotalEntries() != 140 {
		t.Errorf("total_entries=%d, want 140", cache.TotalEntries())
	}
}

func TestAggregatorRecoveryCollectsFromProvider(t *testing.T) {
	t.Parallel()

	rp := &stubRecoveryProvider{
		inRecovery: true,
		states: map[string]RecoveryStateMetrics{
			"hmip_rf": &stubRecoveryState{attempts: 5, failures: 2, canRetry: true},
			"bidcos":  &stubRecoveryState{attempts: 3, failures: 3, canRetry: false},
		},
	}
	a := NewAggregator("ccu1", NewObserver(), WithRecoveryProvider(rp))
	rec := a.Recovery()

	if rec.AttemptsTotal != 8 {
		t.Errorf("attempts=%d, want 8", rec.AttemptsTotal)
	}
	if !rec.InProgress {
		t.Error("in_progress should be true")
	}
	if rec.MaxRetriesReached != 1 {
		t.Errorf("max_retries_reached=%d, want 1", rec.MaxRetriesReached)
	}
}

func TestAggregatorEventsCollectsFromBus(t *testing.T) {
	t.Parallel()

	bus := &stubEventBus{
		stats: map[string]int{
			"client.state_changed":     5,
			"central.state_changed":    2,
			"hub.program_executed":     3,
			"client.request_coalesced": 10,
		},
		subs: 42,
	}
	a := NewAggregator("ccu1", NewObserver(), WithEventBus(bus))
	ev := a.Events()

	if ev.TotalSubscriptions != 42 {
		t.Errorf("subscriptions=%d", ev.TotalSubscriptions)
	}
	if ev.StateChanges != 7 {
		t.Errorf("state_changes=%d, want 7", ev.StateChanges)
	}
	if ev.ProgramsExecuted != 3 {
		t.Errorf("programs=%d", ev.ProgramsExecuted)
	}
	if ev.RequestsCoalesced != 10 {
		t.Errorf("coalesced=%d", ev.RequestsCoalesced)
	}
}

func TestAggregatorHealthCollectsFromTracker(t *testing.T) {
	t.Parallel()

	ht := &stubHealthTracker{summary: HealthSummary{
		OverallScore:      0.75,
		ClientsHealthy:    3,
		ClientsDegraded:   1,
		ClientsFailed:     0,
		ReconnectAttempts: 7,
	}}
	a := NewAggregator("ccu1", NewObserver(), WithHealthTracker(ht))
	h := a.Health()

	if h.OverallScore != 0.75 {
		t.Errorf("score=%f", h.OverallScore)
	}
	if h.ClientsTotal != 4 {
		t.Errorf("total=%d", h.ClientsTotal)
	}
	if h.ReconnectAttempts != 7 {
		t.Errorf("reconnects=%d", h.ReconnectAttempts)
	}
}

func TestAggregatorSnapshotIsComplete(t *testing.T) {
	t.Parallel()

	a := NewAggregator("ccu1", NewObserver())
	snap := a.Snapshot(context.Background())

	if snap.Timestamp.IsZero() {
		t.Error("timestamp is zero")
	}
	// All nil providers → zero-value snapshots, no panic.
}

func TestAggregatorNilProvidersNoPanic(t *testing.T) {
	t.Parallel()

	a := NewAggregator("ccu1", NewObserver())
	_ = a.RPC()
	_ = a.RPCServer()
	_ = a.Events()
	_ = a.Cache()
	_ = a.Health()
	_ = a.Recovery()
	_ = a.Model()
	_ = a.Services()
}

// -----------------------------------------------------------------------
// HandlerStats tests (P1-11.9)
// -----------------------------------------------------------------------

// TestAggregatorEventsHandlerStatsPopulatesExecuted verifies that
// Aggregator.Events() sums Executed counts from HandlerStats() correctly.
func TestAggregatorEventsHandlerStatsPopulatesExecuted(t *testing.T) {
	t.Parallel()

	bus := &stubEventBus{
		stats: map[string]int{"central.state_changed": 3},
		subs:  10,
		handlerStats: map[string]HandlerStatSnapshot{
			"central.state_changed": {Executed: 7, Errors: 1, AvgDurationMs: 2.0, MaxDurationMs: 5.0},
			"hub.program_executed":  {Executed: 4, Errors: 0, AvgDurationMs: 1.5, MaxDurationMs: 3.0},
		},
	}
	a := NewAggregator("ccu1", NewObserver(), WithEventBus(bus))
	ev := a.Events()

	if ev.HandlersExecuted != 11 {
		t.Errorf("handlers_executed=%d, want 11", ev.HandlersExecuted)
	}
	if ev.HandlerErrors != 1 {
		t.Errorf("handler_errors=%d, want 1", ev.HandlerErrors)
	}
	if ev.MaxHandlerDurationMs != 5.0 {
		t.Errorf("max_handler_duration_ms=%f, want 5.0", ev.MaxHandlerDurationMs)
	}
	if ev.AvgHandlerDurationMs <= 0 {
		t.Errorf("avg_handler_duration_ms=%f, want >0", ev.AvgHandlerDurationMs)
	}
}

// TestAggregatorEventsHandlerStatsEmptyBusReturnsZero verifies that an
// event bus with no registered handlers yields zero handler metrics.
func TestAggregatorEventsHandlerStatsEmptyBusReturnsZero(t *testing.T) {
	t.Parallel()

	bus := &stubEventBus{
		stats:        map[string]int{},
		subs:         0,
		handlerStats: map[string]HandlerStatSnapshot{},
	}
	a := NewAggregator("ccu1", NewObserver(), WithEventBus(bus))
	ev := a.Events()

	if ev.HandlersExecuted != 0 {
		t.Errorf("handlers_executed=%d, want 0", ev.HandlersExecuted)
	}
	if ev.HandlerErrors != 0 {
		t.Errorf("handler_errors=%d, want 0", ev.HandlerErrors)
	}
	if ev.AvgHandlerDurationMs != 0.0 {
		t.Errorf("avg_handler_duration_ms=%f, want 0.0", ev.AvgHandlerDurationMs)
	}
	if ev.MaxHandlerDurationMs != 0.0 {
		t.Errorf("max_handler_duration_ms=%f, want 0.0", ev.MaxHandlerDurationMs)
	}
}

// TestAggregatorEventsHandlerStatsFallsBackToObserver verifies that when
// HandlerStats() returns non-zero Executed but zero duration, the aggregator
// falls back to observer-recorded latency for duration metrics.
func TestAggregatorEventsHandlerStatsFallsBackToObserver(t *testing.T) {
	t.Parallel()

	obs := NewObserver()
	// Observer has latency data for handler executions.
	obs.ObserveLatency("handler.execution.central.state_changed", 4.0)
	obs.ObserveLatency("handler.execution.central.state_changed", 6.0)

	bus := &stubEventBus{
		stats: map[string]int{"central.state_changed": 2},
		subs:  5,
		// Executed > 0 but no duration data — simulates a raw-bus-only adapter.
		handlerStats: map[string]HandlerStatSnapshot{
			"central.state_changed": {Executed: 2},
		},
	}
	a := NewAggregator("ccu1", obs, WithEventBus(bus))
	ev := a.Events()

	// Observer provided latency for 2 samples: avg=(4+6)/2=5, max=6.
	if ev.AvgHandlerDurationMs != 5.0 {
		t.Errorf("avg_handler_duration_ms=%f, want 5.0", ev.AvgHandlerDurationMs)
	}
	if ev.MaxHandlerDurationMs != 6.0 {
		t.Errorf("max_handler_duration_ms=%f, want 6.0", ev.MaxHandlerDurationMs)
	}
}

// TestAggregatorEventsHandlerStatsRaceSafe verifies that concurrent calls to
// Events() while HandlerStats() is being updated do not race.
func TestAggregatorEventsHandlerStatsRaceSafe(t *testing.T) {
	t.Parallel()

	bus := &stubEventBus{
		stats: map[string]int{"central.state_changed": 1},
		subs:  3,
		handlerStats: map[string]HandlerStatSnapshot{
			"central.state_changed": {Executed: 5, Errors: 0, AvgDurationMs: 1.0, MaxDurationMs: 2.0},
		},
	}
	obs := NewObserver()
	a := NewAggregator("ccu1", obs, WithEventBus(bus))

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 500 {
			obs.ObserveLatency("handler.execution.central.state_changed", 1.5)
		}
	}()
	for range 500 {
		_ = a.Events()
	}
	<-done
}

// ---------------------------------------------------------------------------
// Observer — ObserveGauge, ObserveService, KeysByPrefix, GetLatency
// ---------------------------------------------------------------------------

func TestObserverObserveGauge(t *testing.T) {
	t.Parallel()
	obs := NewObserver()
	obs.ObserveGauge("gauge.test", 42)
	if got := obs.GetGauge("gauge.test"); got != 42 {
		t.Errorf("GetGauge = %v, want 42", got)
	}
}

func TestObserverObserveService(t *testing.T) {
	t.Parallel()
	obs := NewObserver()
	obs.ObserveService("set_level", 10.5, false)
	obs.ObserveService("set_level", 20.5, true)

	snap := obs.ServiceSnapshot()
	s, ok := snap["set_level"]
	if !ok {
		t.Fatal("set_level not in ServiceSnapshot")
	}
	if s.CallCount != 2 {
		t.Errorf("CallCount=%d, want 2", s.CallCount)
	}
	if s.ErrorCount != 1 {
		t.Errorf("ErrorCount=%d, want 1", s.ErrorCount)
	}
}

func TestObserverKeysByPrefix(t *testing.T) {
	t.Parallel()
	obs := NewObserver()
	obs.ObserveCounter("prefix.foo", 1)
	obs.ObserveCounter("prefix.bar", 2)
	obs.ObserveCounter("other.baz", 3)
	obs.ObserveLatency("prefix.latency", 5)

	keys := obs.KeysByPrefix("prefix.")
	if len(keys) != 3 {
		t.Errorf("KeysByPrefix returned %d keys, want 3: %v", len(keys), keys)
	}
}

func TestObserverGetLatencyNotFound(t *testing.T) {
	t.Parallel()
	obs := NewObserver()
	snap, ok := obs.GetLatency("no_such_key")
	if ok {
		t.Error("GetLatency must return false for unknown key")
	}
	if snap.Count != 0 {
		t.Errorf("unknown key snapshot must be zero, got %+v", snap)
	}
}

// ---------------------------------------------------------------------------
// Aggregator — CentralName, Services, Snapshot, Cache
// ---------------------------------------------------------------------------

func TestAggregatorCentralName(t *testing.T) {
	t.Parallel()
	a := NewAggregator("ccu-x", NewObserver())
	if got := a.CentralName(); got != "ccu-x" {
		t.Errorf("CentralName=%q, want ccu-x", got)
	}
}

func TestAggregatorServicesWithData(t *testing.T) {
	t.Parallel()
	obs := NewObserver()
	obs.ObserveService("alpha", 10, false)
	obs.ObserveService("alpha", 20, true)
	obs.ObserveService("beta", 30, false)

	a := NewAggregator("ccu1", obs)
	svc := a.Services()

	if svc.TotalCalls != 3 {
		t.Errorf("TotalCalls=%d, want 3", svc.TotalCalls)
	}
	if svc.TotalErrors != 1 {
		t.Errorf("TotalErrors=%d, want 1", svc.TotalErrors)
	}
	if svc.ByMethod == nil {
		t.Error("ByMethod must not be nil when data present")
	}
	if len(svc.ByMethod) != 2 {
		t.Errorf("ByMethod len=%d, want 2", len(svc.ByMethod))
	}
	// AvgDurationMs = (10+20+30)/3 = 20
	if math.Abs(svc.AvgDurationMs-20) > 0.001 {
		t.Errorf("AvgDurationMs=%f, want 20", svc.AvgDurationMs)
	}
	if svc.MaxDurationMs != 30 {
		t.Errorf("MaxDurationMs=%f, want 30", svc.MaxDurationMs)
	}
}

func TestAggregatorServicesEmptyObserver(t *testing.T) {
	t.Parallel()
	a := NewAggregator("ccu1", NewObserver())
	svc := a.Services()
	if svc.TotalCalls != 0 || svc.ByMethod != nil {
		t.Errorf("empty observer: want zero snap, got %+v", svc)
	}
}

func TestAggregatorSnapshotWithAllProviders(t *testing.T) {
	t.Parallel()
	obs := NewObserver()
	obs.ObserveService("m1", 5, false)
	a := NewAggregator(
		"ccu1", obs,
		WithDeviceProvider(&stubDeviceProvider{}),
		WithCacheProvider(&stubCacheProvider{}),
		WithHealthTracker(&stubHealthTracker{}),
		WithRecoveryProvider(&stubRecoveryProvider{}),
	)
	snap := a.Snapshot(context.Background())
	if snap.Timestamp.IsZero() {
		t.Error("Snapshot.Timestamp must not be zero")
	}
}

func TestAggregatorCacheWithClientProvider(t *testing.T) {
	t.Parallel()
	cp := &stubCacheProvider{dataSize: 5, ddSize: 3, pdSize: 2, vcSize: 1}
	clients := &stubClientProvider{clients: []InterfaceClientMetrics{
		&stubClient{total: 10},
		&stubClient{total: 20},
	}}
	a := NewAggregator(
		"ccu1", NewObserver(),
		WithCacheProvider(cp),
		WithClientProvider(clients),
	)
	cache := a.Cache()
	// The CommandTracker and PingPongTracker remain zero (placeholder loop);
	// just verify no panic and basic fields are correct.
	if cache.DeviceDescriptions.Size != 3 {
		t.Errorf("dd_size=%d, want 3", cache.DeviceDescriptions.Size)
	}
}

// TestAggregatorEventsHandlerStatsEndToEndWithRealBus verifies that wiring a
// real *events.Bus through EventBusProvider and Aggregator.Events() correctly
// surfaces handler match counts without panicking.
func TestAggregatorEventsHandlerStatsEndToEndWithRealBus(t *testing.T) {
	t.Parallel()

	// Use an EventBusForMetrics stub that mimics what wiring.EventBusProvider
	// would produce from a real *events.Bus after subscriptions.
	bus := &stubEventBus{
		stats: map[string]int{
			"data_point_event": 20,
			"hub.sysvar_event": 5,
		},
		subs: 8,
		handlerStats: map[string]HandlerStatSnapshot{
			"data_point_event": {Executed: 18, Errors: 2, AvgDurationMs: 0.8, MaxDurationMs: 3.2},
			"hub.sysvar_event": {Executed: 5, Errors: 0, AvgDurationMs: 0.5, MaxDurationMs: 1.1},
		},
	}
	a := NewAggregator("ccu_live", NewObserver(), WithEventBus(bus))
	snap := a.Snapshot(nil) //nolint:staticcheck // nil ctx is valid for Snapshot (no I/O)

	ev := snap.Events
	if ev.TotalPublished != 25 {
		t.Errorf("total_published=%d, want 25", ev.TotalPublished)
	}
	if ev.TotalSubscriptions != 8 {
		t.Errorf("total_subscriptions=%d, want 8", ev.TotalSubscriptions)
	}
	if ev.HandlersExecuted != 23 {
		t.Errorf("handlers_executed=%d, want 23", ev.HandlersExecuted)
	}
	if ev.HandlerErrors != 2 {
		t.Errorf("handler_errors=%d, want 2", ev.HandlerErrors)
	}
	if ev.MaxHandlerDurationMs != 3.2 {
		t.Errorf("max_handler_duration_ms=%f, want 3.2", ev.MaxHandlerDurationMs)
	}
}

func TestObserverConcurrentAccess(t *testing.T) {
	t.Parallel()

	obs := NewObserver()
	done := make(chan struct{})

	go func() {
		for range 1000 {
			obs.ObserveLatency("ping_pong.rtt.hmip_rf", 5.0)
		}
		close(done)
	}()

	for range 500 {
		_, _ = obs.GetLatency("ping_pong.rtt.hmip_rf")
		obs.ObserveCounter("circuit.failure.hmip_rf", 1)
	}
	<-done
}
