// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type wiringFakeCaller struct{ err error }

func (f *wiringFakeCaller) Call(_ context.Context, _ string, _ []any) (any, error) {
	return nil, f.err
}

func TestSubscribeObserverFanOutsAllMetricEvents(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	obs := metrics.NewObserver()

	cancel := SubscribeObserver(bus, obs)
	defer cancel()

	events.Publish(bus, hmevent.LatencyMetricEvent{
		MetricEvent: hmevent.MetricEvent{Base: hmevent.NewBase(), MetricKey: "lat.k"},
		DurationMs:  10.0,
	})
	events.Publish(bus, hmevent.CounterMetricEvent{
		MetricEvent: hmevent.MetricEvent{Base: hmevent.NewBase(), MetricKey: "ctr.k"},
		Delta:       7,
	})
	events.Publish(bus, hmevent.GaugeMetricEvent{
		MetricEvent: hmevent.MetricEvent{Base: hmevent.NewBase(), MetricKey: "gauge.k"},
		Value:       42,
	})
	events.Publish(bus, hmevent.HealthMetricEvent{
		MetricEvent: hmevent.MetricEvent{Base: hmevent.NewBase(), MetricKey: "health.k"},
		Healthy:     true,
	})

	if got, ok := obs.GetLatency("lat.k"); !ok || got.Count != 1 {
		t.Errorf("latency snapshot=%+v ok=%v", got, ok)
	}
	if got := obs.GetCounter("ctr.k"); got != 7 {
		t.Errorf("counter=%d, want 7", got)
	}
	if got := obs.GetGauge("gauge.k"); got != 42 {
		t.Errorf("gauge=%f, want 42", got)
	}
	if got := obs.GetGauge("health.k"); got != 1 {
		t.Errorf("health gauge=%f, want 1", got)
	}
}

func TestSubscribeObserverCancelDetachesAll(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	obs := metrics.NewObserver()

	cancel := SubscribeObserver(bus, obs)
	cancel()
	cancel() // idempotent.

	events.Publish(bus, hmevent.CounterMetricEvent{
		MetricEvent: hmevent.MetricEvent{Base: hmevent.NewBase(), MetricKey: "post.cancel"},
		Delta:       5,
	})
	if got := obs.GetCounter("post.cancel"); got != 0 {
		t.Errorf("counter=%d after cancel, want 0", got)
	}
	if got := bus.TotalSubscriptionCount(); got != 0 {
		t.Errorf("subscriptions remaining after cancel: %d", got)
	}
}

func TestSubscribeObserverNilBusOrObserverIsNoop(t *testing.T) {
	t.Parallel()

	if got := SubscribeObserver(nil, metrics.NewObserver()); got == nil {
		t.Fatal("nil bus -> got nil cancel func")
	} else {
		got() // no panic
	}
	if got := SubscribeObserver(events.NewBus(), nil); got == nil {
		t.Fatal("nil observer -> got nil cancel func")
	} else {
		got() // no panic
	}
}

func TestAggregatorRoundtripWithAllProvidersWired(t *testing.T) {
	t.Parallel()

	const centralName = "ccu-roundtrip"

	// 1. EventBus + observer — fed by SubscribeObserver.
	bus := events.NewBus()
	obs := metrics.NewObserver()
	cancel := SubscribeObserver(bus, obs)
	defer cancel()

	// 2. Client provider.
	cp := client.NewMetricsClientProvider(centralName)
	ic, err := client.New(client.Config{
		CentralName: centralName, Interface: hmenum.InterfaceHmIPRF,
		Caller: &wiringFakeCaller{err: errors.New("simulated")},
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	defer ic.Close()
	cp.Register(ic)
	for range 4 {
		_, _ = ic.Call(context.Background(), "ping", nil, hmenum.CommandPriorityLow, "")
	}

	// 3. Cache provider.
	cc := coordinators.NewCacheCoordinator()
	key := hmtypes.DataPointKey{
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "0001:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Parameter:      "STATE",
	}
	_, _ = cc.Get(key) // miss.
	cc.Set(key, hmtypes.BoolValue(true), "src")
	_, _ = cc.Get(key) // hit.

	// 4. Recovery provider.
	rc := coordinators.NewConnectionRecoveryCoordinator(centralName, bus)
	pipeline := []coordinators.Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error {
			return errors.New("rec err")
		}},
	}
	_ = rc.Run(context.Background(), "iface", pipeline)

	// 5. Health provider.
	tr := health.NewTracker()
	tr.Record("hmip-rf", health.Sample{Healthy: true})

	// Bus stats — publish a few telemetry counters.
	for range 3 {
		events.Publish(bus, hmevent.CounterMetricEvent{
			MetricEvent: hmevent.MetricEvent{Base: hmevent.NewBase(), MetricKey: "k"},
			Delta:       1,
		})
	}

	agg := metrics.NewAggregator(
		centralName, obs,
		metrics.WithClientProvider(NewClientProvider(cp)),
		metrics.WithCacheProvider(NewCacheProvider(cc)),
		metrics.WithRecoveryProvider(NewRecoveryProvider(rc)),
		metrics.WithEventBus(NewEventBusProvider(bus)),
		metrics.WithHealthTracker(NewHealthProvider(tr, rc)),
	)

	snap := agg.Snapshot(context.Background())

	if snap.RPC.TotalRequests != 4 {
		t.Errorf("rpc.total_requests=%d, want 4", snap.RPC.TotalRequests)
	}
	if snap.RPC.LastFailureTime == nil {
		t.Error("rpc.last_failure_time is nil; want set after errored calls")
	}
	if snap.Cache.DataCache.Hits != 1 {
		t.Errorf("cache.hits=%d, want 1", snap.Cache.DataCache.Hits)
	}
	if snap.Cache.DataCache.Misses != 1 {
		t.Errorf("cache.misses=%d, want 1", snap.Cache.DataCache.Misses)
	}
	if !snap.Recovery.InProgress && snap.Recovery.AttemptsTotal == 0 {
		// run already returned — InProgress should be false; AttemptsTotal must be 1
		if snap.Recovery.AttemptsTotal != 1 {
			t.Errorf("recovery.attempts=%d, want 1", snap.Recovery.AttemptsTotal)
		}
	}
	if snap.Health.ClientsHealthy != 1 {
		t.Errorf("health.clientsHealthy=%d, want 1", snap.Health.ClientsHealthy)
	}
	if snap.Health.OverallScore != 1.0 {
		t.Errorf("health.score=%f, want 1.0", snap.Health.OverallScore)
	}
	// Recovery attempts should be summed into ReconnectAttempts via the
	// HealthProvider's recovery rollup.
	if snap.Health.ReconnectAttempts == 0 {
		t.Error("health.reconnectAttempts=0; want recovery rollup to contribute")
	}
	if snap.Events.TotalSubscriptions == 0 {
		t.Error("events.totalSubscriptions==0; the observer wiring should register subscriptions")
	}
	if got := snap.Events.EventsByType["metric.counter"]; got < 3 {
		t.Errorf("events.metric.counter=%d, want >=3", got)
	}
}
