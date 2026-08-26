// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

type sizeStub struct{ n int }

func (s sizeStub) Len() int { return s.n }

func TestCacheCoordinatorMetricsCountsHitsAndMisses(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	key := dpKey("iface", "A:1", "LEVEL")

	if got := c.MetricsDataCacheHits(); got != 0 {
		t.Errorf("baseline hits=%d", got)
	}
	if got := c.MetricsDataCacheMisses(); got != 0 {
		t.Errorf("baseline misses=%d", got)
	}

	// Misses (cache empty).
	for range 4 {
		_, _ = c.Get(key)
	}
	c.Set(key, hmtypes.FloatValue(0.1), "src")
	for range 3 {
		_, _ = c.Get(key)
	}

	if got := c.MetricsDataCacheHits(); got != 3 {
		t.Errorf("hits=%d, want 3", got)
	}
	if got := c.MetricsDataCacheMisses(); got != 4 {
		t.Errorf("misses=%d, want 4", got)
	}
	if got := c.MetricsDataCacheSize(); got != 1 {
		t.Errorf("size=%d, want 1", got)
	}
}

func TestCacheCoordinatorMetricsCountsEvictions(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	key := dpKey("iface", "A:1", "LEVEL")

	c.Set(key, hmtypes.FloatValue(0.1), "src")
	if !c.Delete(key) {
		t.Fatal("delete should succeed")
	}
	if c.Delete(key) {
		t.Fatal("second delete should fail")
	}
	if got := c.MetricsDataCacheEvictions(); got != 1 {
		t.Errorf("evictions=%d, want 1 (failed delete must not bump)", got)
	}
}

func TestCacheCoordinatorMetricsRaceSafe(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	const goroutines = 8
	const ops = 100
	var wg sync.WaitGroup

	wg.Add(goroutines)
	for i := range goroutines {
		go func() {
			defer wg.Done()
			for j := range ops {
				key := dpKey("iface", "A:1", "P")
				if (i+j)%2 == 0 {
					c.Set(key, hmtypes.FloatValue(float64(j)), "src")
				}
				_, _ = c.Get(key)
			}
		}()
	}
	wg.Go(func() {
		for range goroutines * ops {
			_ = c.MetricsDataCacheHits()
			_ = c.MetricsDataCacheMisses()
			_ = c.MetricsDataCacheSize()
			_ = c.MetricsDataCacheEvictions()
		}
	})
	wg.Wait()

	total := c.MetricsDataCacheHits() + c.MetricsDataCacheMisses()
	if total != goroutines*ops {
		t.Errorf("hits+misses=%d, want %d", total, goroutines*ops)
	}
}

func TestCacheCoordinatorMetricsSizeProvidersOptional(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()
	if got := c.MetricsDeviceDescriptionsSize(); got != 0 {
		t.Errorf("unwired dd=%d, want 0", got)
	}
	if got := c.MetricsParamsetDescriptionsSize(); got != 0 {
		t.Errorf("unwired pd=%d, want 0", got)
	}
	if got := c.MetricsVisibilityCacheSize(); got != 0 {
		t.Errorf("unwired vis=%d, want 0", got)
	}

	c.SetSizeProviders(sizeStub{n: 7}, sizeStub{n: 11}, sizeStub{n: 3})
	if got := c.MetricsDeviceDescriptionsSize(); got != 7 {
		t.Errorf("dd=%d, want 7", got)
	}
	if got := c.MetricsParamsetDescriptionsSize(); got != 11 {
		t.Errorf("pd=%d, want 11", got)
	}
	if got := c.MetricsVisibilityCacheSize(); got != 3 {
		t.Errorf("vis=%d, want 3", got)
	}
}

// TestCacheCoordinatorMetricsVisibilityWithRealDecider verifies the end-to-end
// path from a real *visibility.ParameterDecider through SetSizeProviders to
// MetricsVisibilityCacheSize. Before wiring the decider the method must
// return 0; after the decider is used it must reflect the actual cache size.
func TestCacheCoordinatorMetricsVisibilityWithRealDecider(t *testing.T) {
	t.Parallel()

	c := NewCacheCoordinator()

	// Before wiring: must report 0 (nil provider).
	if got := c.MetricsVisibilityCacheSize(); got != 0 {
		t.Fatalf("unwired visibility size=%d, want 0", got)
	}

	decider := visibility.NewParameterDecider(nil)

	// Wire the real decider as the visibility provider.
	c.SetSizeProviders(nil, nil, decider)

	// Cache is still empty before any lookup.
	if got := c.MetricsVisibilityCacheSize(); got != 0 {
		t.Fatalf("visibility size before use=%d, want 0", got)
	}

	// Perform two distinct lookups to populate the decider's memoisation cache.
	_ = decider.IsParameterIgnored("HmIP-STH", "CLIMATECONTROL_RT_TRANSCEIVER", -1, hmenum.ParamsetKeyValues, hmenum.ParameterLevel)
	_ = decider.IsParameterIgnored("HmIP-STH", "CLIMATECONTROL_RT_TRANSCEIVER", -1, hmenum.ParamsetKeyValues, hmenum.ParameterState)

	// MetricsVisibilityCacheSize must now reflect the real decider cache.
	if got := c.MetricsVisibilityCacheSize(); got != 2 {
		t.Fatalf("visibility size after 2 lookups=%d, want 2", got)
	}
}
