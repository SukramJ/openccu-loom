// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Compile-time guard: *DataPoint[T] satisfies the BaseDataPoint
// contract via the embedded [datapoint.BaseDataPointFields] (Phase
// 5C-1). The concrete instantiation below picks bool to keep the
// guard cheap; the same satisfaction rule applies to every T.
var _ datapoint.BaseDataPoint = (*DataPoint[bool])(nil)

// TestDataPointSatisfiesBaseDataPoint exercises the promotion at
// runtime, not just at compile time, so the foundation interface
// surface (UniqueID / Visible / EnabledByDefault) keeps producing
// the documented results when a concrete *DataPoint[T] is treated as
// a [datapoint.BaseDataPoint].
func TestDataPointSatisfiesBaseDataPoint(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.CentralName = "ccu-prod"
	cfg.Key.ChannelAddress = "VCU0123456:1"
	cfg.Key.Parameter = "STATE"

	var iface datapoint.BaseDataPoint = NewDataPoint[bool](cfg)
	if got, want := iface.UniqueID(), "ccu-prod:VCU0123456:1:STATE"; got != want {
		t.Fatalf("iface.UniqueID() = %q, want %q", got, want)
	}
	if !iface.Visible() {
		t.Fatal("iface.Visible() must be true for default-usage DP")
	}
	if !iface.EnabledByDefault() {
		t.Fatal("iface.EnabledByDefault() must be true for default-usage DP")
	}
}

// TestDataPointUniqueIDFormat verifies the canonical
// "<central>:<address>:<key>" UniqueID promoted from
// [datapoint.BaseDataPointFields] tracks the DataPoint's Config.
// Multi-CCU correctness (ADR 0002) hinges on the central segment
// being present so two CCUs cannot collide on the same channel
// address.
func TestDataPointUniqueIDFormat(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		central string
		address string
		key     string
		want    string
	}{
		{
			name:    "production multi-CCU dp",
			central: "ccu-prod",
			address: "VCU0123456:1",
			key:     "LEVEL",
			want:    "ccu-prod:VCU0123456:1:LEVEL",
		},
		{
			name:    "second central, same address",
			central: "ccu-secondary",
			address: "VCU0123456:1",
			key:     "LEVEL",
			want:    "ccu-secondary:VCU0123456:1:LEVEL",
		},
		{
			name:    "test fixture without central",
			central: "",
			address: "A:1",
			key:     "STATE",
			want:    ":A:1:STATE",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseCfg(hmenum.Parameter(tc.key), hmenum.ParameterTypeBool, hmenum.OperationsRead)
			cfg.CentralName = tc.central
			cfg.Key.ChannelAddress = tc.address
			cfg.Key.Parameter = tc.key
			dp := NewDataPoint[bool](cfg)
			if got := dp.UniqueID(); got != tc.want {
				t.Fatalf("UniqueID() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDataPointEmbeddedFieldsAccessible verifies the promoted
// accessor methods (Central / Address / KeyName) on *DataPoint[T]
// return the values derived from the constructor's Config. This is
// the surface north-bound adapters and the central registry use to
// read DP identity without poking into Config.Key directly.
func TestDataPointEmbeddedFieldsAccessible(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.CentralName = "ccu-prod"
	cfg.Key.ChannelAddress = "VCU0123456:1"
	cfg.Key.Parameter = "LEVEL"
	dp := NewDataPoint[float64](cfg)

	if got, want := dp.Central(), "ccu-prod"; got != want {
		t.Fatalf("Central()=%q want %q", got, want)
	}
	if got, want := dp.Address(), "VCU0123456:1"; got != want {
		t.Fatalf("Address()=%q want %q", got, want)
	}
	if got, want := dp.KeyName(), "LEVEL"; got != want {
		t.Fatalf("KeyName()=%q want %q", got, want)
	}
}

// TestDataPointUsageFallbackChain pins down the documented fallback:
// forced (via embedded [datapoint.BaseDataPointFields]) > Spec.Usage
// > [hmenum.DataPointUsageDataPoint] default. The chain is the user-
// facing contract every concrete generic data-point type relies on,
// so regressions show up as broken north-bound rendering.
func TestDataPointUsageFallbackChain(t *testing.T) {
	t.Parallel()

	t.Run("default falls back to DataPoint", func(t *testing.T) {
		t.Parallel()
		dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
		if got := dp.Usage(); got != hmenum.DataPointUsageDataPoint {
			t.Fatalf("Usage()=%q want DataPoint default", got)
		}
	})

	t.Run("Spec.Usage wins over default", func(t *testing.T) {
		t.Parallel()
		cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
		cfg.Usage = hmenum.DataPointUsageCDPVisible
		dp := NewDataPoint[bool](cfg)
		if got := dp.Usage(); got != hmenum.DataPointUsageCDPVisible {
			t.Fatalf("Usage()=%q want CDPVisible", got)
		}
	})

	t.Run("forced wins over Spec.Usage", func(t *testing.T) {
		t.Parallel()
		cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
		cfg.Usage = hmenum.DataPointUsageCDPVisible
		dp := NewDataPoint[bool](cfg)
		dp.SetForcedUsage(hmenum.DataPointUsageNoCreate)
		if got := dp.Usage(); got != hmenum.DataPointUsageNoCreate {
			t.Fatalf("Usage()=%q want forced NoCreate", got)
		}
	})

	t.Run("forced over default", func(t *testing.T) {
		t.Parallel()
		dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
		dp.SetForcedUsage(hmenum.DataPointUsageCDPSecondary)
		if got := dp.Usage(); got != hmenum.DataPointUsageCDPSecondary {
			t.Fatalf("Usage()=%q want forced CDPSecondary", got)
		}
	})
}

// TestDataPointVisibleViaPromotion verifies that the DP-specific
// [Visible] override (which considers the full Spec.Usage fallback
// chain) shadows the promoted [datapoint.BaseDataPointFields.Visible]
// — so a forced [hmenum.DataPointUsageCDPSecondary] correctly hides
// the DP rather than passing the foundation's "only NoCreate hides
// it" rule.
func TestDataPointVisibleViaPromotion(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		forced      hmenum.DataPointUsage
		setForced   bool
		configUsage hmenum.DataPointUsage
		want        bool
	}{
		{name: "default DP is visible", want: true},
		{name: "Spec CDPSecondary hides", configUsage: hmenum.DataPointUsageCDPSecondary, want: false},
		{name: "forced NoCreate hides", forced: hmenum.DataPointUsageNoCreate, setForced: true, want: false},
		{name: "forced CDPSecondary hides via DP shadow", forced: hmenum.DataPointUsageCDPSecondary, setForced: true, want: false},
		{name: "forced CDPVisible shows", forced: hmenum.DataPointUsageCDPVisible, setForced: true, want: true},
		{name: "forced CDPVisible overrides Spec.NoCreate", forced: hmenum.DataPointUsageCDPVisible, setForced: true, configUsage: hmenum.DataPointUsageNoCreate, want: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
			cfg.Usage = tc.configUsage
			dp := NewDataPoint[bool](cfg)
			if tc.setForced {
				dp.SetForcedUsage(tc.forced)
			}
			if got := dp.Visible(); got != tc.want {
				t.Fatalf("Visible()=%v want %v", got, tc.want)
			}
		})
	}
}

// TestDataPointPublishUpdateViaPromotion exercises the promoted
// [datapoint.BaseDataPointFields.PublishUpdate] surface — a north-
// bound MQTT bridge or REST broadcaster can register a publisher
// against the DP without poking at internals. The forwarded key
// must match the DP's UniqueID() so subscribers can dispatch by
// identity.
func TestDataPointPublishUpdateViaPromotion(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead)
	cfg.CentralName = "ccu-prod"
	cfg.Key.ChannelAddress = "VCU0123456:1"
	cfg.Key.Parameter = "LEVEL"
	dp := NewDataPoint[float64](cfg)

	pub := &capturingPublisher{}
	dp.SetPublisher(pub)
	dp.PublishUpdate(context.Background(), 0.42)

	calls := pub.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected 1 publisher call, got %d", len(calls))
	}
	if got, want := calls[0].key, "ccu-prod:VCU0123456:1:LEVEL"; got != want {
		t.Fatalf("publisher key=%q want %q", got, want)
	}
	v, ok := calls[0].value.(float64)
	if !ok || v != 0.42 {
		t.Fatalf("publisher value=%v want 0.42", calls[0].value)
	}
}

// TestDataPointConcurrentAfterMigration race-tests the migrated DP:
// the embedded BaseDataPointFields' lock and the DataPoint's own
// `mu` are independent locks, so concurrent forced-usage churn,
// publisher updates, and OnEvent traffic must not race or deadlock.
// Mirrors the foundation-layer concurrency test (4 writers × 4
// readers × 200 iterations) and additionally drives wire events.
func TestDataPointConcurrentAfterMigration(t *testing.T) {
	t.Parallel()

	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsEvent)
	cfg.CentralName = "ccu-prod"
	cfg.Key.ChannelAddress = "VCU0123456:1"
	cfg.Key.Parameter = "LEVEL"
	dp := NewDataPoint[int32](cfg)

	pub := &countingPublisher2{}
	dp.SetPublisher(pub)

	const (
		writers    = 4
		readers    = 4
		iterations = 200
	)

	usages := []hmenum.DataPointUsage{
		hmenum.DataPointUsageCDPPrimary,
		hmenum.DataPointUsageCDPVisible,
		hmenum.DataPointUsageDataPoint,
		hmenum.DataPointUsageEvent,
		hmenum.DataPointUsageCDPSecondary,
		hmenum.DataPointUsageNoCreate,
	}

	var wg sync.WaitGroup

	// Writers churn forced-usage / publisher / wire events.
	for w := 0; w < writers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			for i := 0; i < iterations; i++ {
				dp.SetForcedUsage(usages[(w+i)%len(usages)])
				if i%17 == 0 {
					dp.SetPublisher(pub)
				}
				dp.PublishUpdate(ctx, i)
				dp.OnEvent(int32(i))
			}
		}()
	}

	// Readers spin every observation surface promoted + DP-native.
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = dp.UniqueID()
				_ = dp.Visible()
				_ = dp.EnabledByDefault()
				_ = dp.Usage()
				_, _ = dp.ForcedUsage()
				_, _ = dp.Value()
				_ = dp.ModifiedAt()
				_ = dp.RefreshedAt()
			}
		}()
	}

	wg.Wait()

	// Sanity check: every PublishUpdate from every writer reached the
	// publisher.
	if got, want := pub.count(), int64(writers*iterations); got != want {
		t.Fatalf("publisher saw %d calls, want %d", got, want)
	}
}

// capturingPublisher records every PublishUpdate invocation for the
// promotion test.
type capturingPublisher struct {
	mu    sync.Mutex
	calls []capturedCall
}

type capturedCall struct {
	key   string
	value any
}

func (c *capturingPublisher) PublishUpdate(_ context.Context, key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, capturedCall{key: key, value: value})
}

func (c *capturingPublisher) snapshot() []capturedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]capturedCall, len(c.calls))
	copy(out, c.calls)
	return out
}

// countingPublisher2 mirrors the foundation-layer counting publisher
// for the race test — the suffix avoids colliding with the package-
// internal `countingPublisher` if one is added later.
type countingPublisher2 struct {
	n atomic.Int64
}

func (c *countingPublisher2) PublishUpdate(_ context.Context, _ string, _ any) {
	c.n.Add(1)
}

func (c *countingPublisher2) count() int64 { return c.n.Load() }
