// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/reliability"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type wiringFakeCaller struct{ err error }

func (f *wiringFakeCaller) Call(_ context.Context, _ string, _ []any) (any, error) {
	return nil, f.err
}

// newModelFixture returns a ModelRegistry holding one device with one
// real channel (plus the implicit root pseudo-channel) and no data
// points.
func newModelFixture(t *testing.T) *registry.ModelRegistry {
	t.Helper()
	reg := registry.NewModelRegistry()
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "VCU0000001",
		Model:       "HmIP-SW-TEST",
	})
	dev.AddChannel("VCU0000001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	reg.Put(dev)
	return reg
}

func TestDeviceProviderReportsModelCounts(t *testing.T) {
	t.Parallel()

	p := NewDeviceProvider(newModelFixture(t))
	devs := p.Devices()
	if len(devs) != 1 {
		t.Fatalf("Devices() len=%d, want 1", len(devs))
	}
	d := devs[0]
	if got := d.ChannelCount(); got < 1 {
		t.Errorf("ChannelCount()=%d, want >=1", got)
	}
	g, cu, ca := d.DataPointCounts()
	if g != 0 || cu != 0 || ca != 0 {
		t.Errorf("DataPointCounts()=(%d,%d,%d), want all 0 for an empty device", g, cu, ca)
	}
	if got := d.DataPointsByCategory(); len(got) != 0 {
		t.Errorf("DataPointsByCategory()=%v, want empty for a device without attachables", got)
	}
}

func TestDeviceProviderNilRegistryIsSafe(t *testing.T) {
	t.Parallel()
	if got := NewDeviceProvider(nil).Devices(); got != nil {
		t.Errorf("nil registry Devices()=%v, want nil", got)
	}
}

func TestHubProviderNilCoordinatorIsSafe(t *testing.T) {
	t.Parallel()
	p := NewHubProvider(nil)
	if got := p.ProgramCount(); got != 0 {
		t.Errorf("ProgramCount()=%d, want 0", got)
	}
	if got := p.SysvarCount(); got != 0 {
		t.Errorf("SysvarCount()=%d, want 0", got)
	}
}

func TestAggregatorRoundtripWithAllProvidersWired(t *testing.T) {
	t.Parallel()

	const centralName = "ccu-roundtrip"

	bus := events.NewBus()
	obs := metrics.NewObserver()

	// Client provider. The caller always fails, so every Call below runs the
	// full retry chain; what this test asserts is the aggregator's rollup of
	// the resulting counters, not how long the backoff between attempts is.
	// An explicit Retrier with the same attempt count but a microsecond
	// backoff keeps every counter identical while dropping the production
	// 2s/4s waits that would otherwise dominate the whole package's runtime.
	cp := client.NewMetricsClientProvider(centralName)
	ic, err := client.New(client.Config{
		CentralName: centralName, Interface: hmenum.InterfaceHmIPRF,
		Caller: &wiringFakeCaller{err: errors.New("simulated")},
		Retrier: reliability.NewRetrier(reliability.RetryConfig{
			Initial: time.Microsecond,
			Max:     time.Microsecond,
		}),
	})
	if err != nil {
		t.Fatalf("client.New: %v", err)
	}
	defer ic.Close()
	cp.Register(ic)
	for range 4 {
		_, _ = ic.Call(context.Background(), "ping", nil, hmenum.CommandPriorityLow, "")
	}

	// Cache provider.
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

	// Recovery provider.
	rc := coordinators.NewConnectionRecoveryCoordinator(centralName, bus)
	pipeline := []coordinators.Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error {
			return errors.New("rec err")
		}},
	}
	_ = rc.Run(context.Background(), "iface", pipeline)

	// Health provider.
	tr := health.NewTracker()
	tr.Record("hmip-rf", health.Sample{Healthy: true})

	agg := metrics.NewAggregator(
		centralName, obs,
		metrics.WithClientProvider(NewClientProvider(cp)),
		metrics.WithCacheProvider(NewCacheProvider(cc)),
		metrics.WithRecoveryProvider(NewRecoveryProvider(rc)),
		metrics.WithEventBus(NewEventBusProvider(bus)),
		metrics.WithHealthTracker(NewHealthProvider(tr, rc)),
		metrics.WithDeviceProvider(NewDeviceProvider(newModelFixture(t))),
		metrics.WithHubManager(NewHubProvider(nil)),
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
	// The coordinator dates every attempt; the snapshot used to declare
	// last_recovery_time and never fill it, so "when did recovery last run"
	// answered nothing on a daemon that had just run one.
	if snap.Recovery.LastRecoveryTime == nil {
		t.Error("recovery.last_recovery_time is nil after a recovery run")
	} else if snap.Recovery.LastRecoveryTime.IsZero() {
		t.Error("recovery.last_recovery_time is the zero time after a recovery run")
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
	// Model section: fed by the device provider wired above.
	if snap.Model.DevicesTotal != 1 {
		t.Errorf("model.devices_total=%d, want 1", snap.Model.DevicesTotal)
	}
	if snap.Model.ChannelsTotal < 1 {
		t.Errorf("model.channels_total=%d, want >=1", snap.Model.ChannelsTotal)
	}
	if snap.Model.ProgramsTotal != 0 || snap.Model.SysvarsTotal != 0 {
		t.Errorf("model programs/sysvars=(%d,%d), want (0,0) with a nil hub provider",
			snap.Model.ProgramsTotal, snap.Model.SysvarsTotal)
	}
}
