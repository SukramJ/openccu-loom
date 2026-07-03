// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ─────────────────────────────────────────────────────────────────────────────
// Test helpers
// ─────────────────────────────────────────────────────────────────────────────

// fakeLoader is a simple configurable ValueLoader for tests.
type fakeLoader struct {
	mu sync.Mutex

	// getValueCalls counts calls to GetValue.
	getValueCalls atomic.Int32 // atomic

	// getParamsetCalls counts calls to GetParamset.
	getParamsetCalls atomic.Int32 // atomic

	// getValueFn is called when set; otherwise getValueResult / getValueErr are used.
	getValueFn func(address string, parameter hmenum.Parameter) (any, error)

	// getParamsetFn is called when set; otherwise getParamsetResult / getParamsetErr are used.
	getParamsetFn func(address string, key hmenum.ParamsetKey) (map[string]any, error)

	getValueResult map[string]any // keyed "address:parameter"
	getValueErr    map[string]error

	getParamsetResult map[string]map[string]any // keyed "address:key"
	getParamsetErr    map[string]error

	// blockCh, when non-nil, blocks GetValue until it is closed.
	blockCh chan struct{}
}

func newFakeLoader() *fakeLoader {
	return &fakeLoader{
		getValueResult:    make(map[string]any),
		getValueErr:       make(map[string]error),
		getParamsetResult: make(map[string]map[string]any),
		getParamsetErr:    make(map[string]error),
	}
}

func (f *fakeLoader) setGetValue(address string, parameter hmenum.Parameter, value any, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := address + ":" + string(parameter)
	f.getValueResult[key] = value
	if err != nil {
		f.getValueErr[key] = err
	}
}

func (f *fakeLoader) setGetParamset(address string, key hmenum.ParamsetKey, result map[string]any, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := address + ":" + string(key)
	f.getParamsetResult[k] = result
	if err != nil {
		f.getParamsetErr[k] = err
	}
}

func (f *fakeLoader) GetValue(_ context.Context, address string, parameter hmenum.Parameter) (any, error) {
	f.getValueCalls.Add(1)

	// Block if requested (for singleflight tests).
	if f.blockCh != nil {
		<-f.blockCh
	}

	if f.getValueFn != nil {
		return f.getValueFn(address, parameter)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	key := address + ":" + string(parameter)
	if err, ok := f.getValueErr[key]; ok && err != nil {
		return nil, err
	}
	v, ok := f.getValueResult[key]
	if !ok {
		return nil, nil
	}
	return v, nil
}

func (f *fakeLoader) GetParamset(_ context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error) {
	f.getParamsetCalls.Add(1)

	// Block if requested (for singleflight tests — shared with GetValue).
	if f.blockCh != nil {
		<-f.blockCh
	}

	if f.getParamsetFn != nil {
		return f.getParamsetFn(address, key)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	k := address + ":" + string(key)
	if err, ok := f.getParamsetErr[k]; ok && err != nil {
		return nil, err
	}
	result, ok := f.getParamsetResult[k]
	if !ok {
		return nil, nil
	}
	return result, nil
}

// makeDPKey is a convenience constructor for DataPointKey.
func makeDPKey(iface, channelAddr string, paramset hmenum.ParamsetKey, parameter string) hmtypes.DataPointKey {
	return hmtypes.DataPointKey{
		InterfaceID:    iface,
		ChannelAddress: channelAddr,
		ParamsetKey:    paramset,
		Parameter:      parameter,
	}
}

// makeTestDevice constructs a minimal Device for testing.
func makeTestDevice() *Device {
	return New(Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "AABBCCDD",
		Model:       "HmIP-TEST",
		Name:        "Test Device",
	})
}

// makeFloatDP builds a *generic.Float for the VALUES paramset.
func makeFloatDP(channelAddr string, p hmenum.Parameter) *generic.Float {
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
}

// makeMasterFloatDP builds a *generic.Float for the MASTER paramset.
func makeMasterFloatDP(channelAddr, paramName string) *generic.Float {
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      paramName,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
}

// advancedClock returns a clock function that adds delta to a fixed base time.
func advancedClock(base time.Time, delta time.Duration) func() time.Time {
	advanced := base.Add(delta)
	return func() time.Time { return advanced }
}

// ─────────────────────────────────────────────────────────────────────────────
// Cluster A — Cache primitives (no LoadValue)
// ─────────────────────────────────────────────────────────────────────────────

func TestValueCacheHitMissBasic(t *testing.T) {
	t.Parallel()

	c := newValueCache()
	dpk := makeDPKey("iface", "ADDR:1", hmenum.ParamsetKeyValues, "LEVEL")
	unrelated := makeDPKey("iface", "ADDR:1", hmenum.ParamsetKeyValues, "STATE")

	c.put(dpk, 0.5, true)

	v, obs, hit := c.get(dpk)
	if !hit {
		t.Fatal("expected cache hit")
	}
	if !obs {
		t.Fatal("expected observed=true")
	}
	if v != 0.5 {
		t.Fatalf("got value %v, want 0.5", v)
	}

	_, _, hit2 := c.get(unrelated)
	if hit2 {
		t.Fatal("unrelated key must miss")
	}
}

func TestValueCacheSentinelExpiresAfterTTL(t *testing.T) {
	t.Parallel()

	c := newValueCache()
	dpk := makeDPKey("iface", "ADDR:1", hmenum.ParamsetKeyValues, "LEVEL")

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.clock = func() time.Time { return base }

	c.put(dpk, nil, false) // sentinel

	// Before expiry: hit.
	v, obs, hit := c.get(dpk)
	if !hit {
		t.Fatal("sentinel within TTL must hit")
	}
	if obs {
		t.Fatal("sentinel must have observed=false")
	}
	if v != nil {
		t.Fatalf("sentinel value must be nil, got %v", v)
	}

	// Advance past sentinelCacheTTL (5 minutes).
	c.clock = advancedClock(base, sentinelCacheTTL+time.Second)

	_, _, hit2 := c.get(dpk)
	if hit2 {
		t.Fatal("sentinel past TTL must miss")
	}
}

func TestValueCacheMasterEntryExpiresAfterTTL(t *testing.T) {
	t.Parallel()

	c := newValueCache()
	dpk := makeDPKey("iface", "ADDR:1", hmenum.ParamsetKeyMaster, "TRANSMIT_TRY_MAX")

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.clock = func() time.Time { return base }

	c.put(dpk, 10, true)

	// Before expiry.
	_, _, hit := c.get(dpk)
	if !hit {
		t.Fatal("MASTER entry within 30min TTL must hit")
	}

	// Advance past masterCacheTTL (30 minutes).
	c.clock = advancedClock(base, masterCacheTTL+time.Second)

	_, _, hit2 := c.get(dpk)
	if hit2 {
		t.Fatal("MASTER entry past 30min TTL must miss")
	}
}

func TestValueCacheValuesEntryNeverExpires(t *testing.T) {
	t.Parallel()

	c := newValueCache()
	dpk := makeDPKey("iface", "ADDR:1", hmenum.ParamsetKeyValues, "STATE")

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.clock = func() time.Time { return base }

	c.put(dpk, true, true)

	// Advance by 24 hours — TTL=0 means never expires.
	c.clock = advancedClock(base, 24*time.Hour)

	_, _, hit := c.get(dpk)
	if !hit {
		t.Fatal("VALUES entry with TTL=0 must never expire")
	}
}

func TestValueCacheInvalidateRemovesEntry(t *testing.T) {
	t.Parallel()

	c := newValueCache()
	dpk := makeDPKey("iface", "ADDR:1", hmenum.ParamsetKeyValues, "LEVEL")

	c.put(dpk, 0.75, true)

	_, _, hit := c.get(dpk)
	if !hit {
		t.Fatal("entry should exist before invalidate")
	}

	c.invalidate(dpk)

	_, _, hit2 := c.get(dpk)
	if hit2 {
		t.Fatal("invalidated entry must miss")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Cluster B — LoadValue end-to-end (with fake ValueLoader)
// ─────────────────────────────────────────────────────────────────────────────

func TestLoadValueReturnsErrNoLoaderWhenNotConfigured(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()
	dpk := makeDPKey("HmIP-RF", "AABBCCDD:1", hmenum.ParamsetKeyValues, "LEVEL")

	_, _, err := d.LoadValue(context.Background(), dpk, hmenum.CallSourceHMInit, false)
	if !errors.Is(err, ErrNoValueLoader) {
		t.Fatalf("expected ErrNoValueLoader, got %v", err)
	}
}

func TestLoadValueCacheHitSkipsLoader(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()
	fake := newFakeLoader()
	d.SetValueLoader(fake)

	dpk := makeDPKey("HmIP-RF", "AABBCCDD:1", hmenum.ParamsetKeyValues, "LEVEL")

	// Pre-populate cache via OnObservedValue.
	d.OnObservedValue(dpk, "x")

	v, obs, err := d.LoadValue(context.Background(), dpk, hmenum.CallSourceManualOrScheduled, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !obs {
		t.Fatal("expected observed=true from cache")
	}
	if v != "x" {
		t.Fatalf("expected value %q, got %v", "x", v)
	}
	if fake.getValueCalls.Load() != 0 {
		t.Fatal("loader must NOT be called on cache hit")
	}
}

func TestLoadValueCacheMissTriggersLoader(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()
	fake := newFakeLoader()

	const channelAddr = "AABBCCDD:1"
	const param = hmenum.Parameter("LEVEL")
	fake.setGetParamset(channelAddr, hmenum.ParamsetKeyValues, map[string]any{string(param): 0.8}, nil)
	d.SetValueLoader(fake)

	dpk := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyValues, string(param))

	v, obs, err := d.LoadValue(context.Background(), dpk, hmenum.CallSourceHMInit, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !obs {
		t.Fatal("expected observed=true")
	}
	if v != 0.8 {
		t.Fatalf("expected 0.8, got %v", v)
	}
	if fake.getParamsetCalls.Load() != 1 {
		t.Fatalf("GetParamset must be called exactly once, got %d", fake.getParamsetCalls.Load())
	}

	// Second call must hit cache — loader not called again.
	_, _, err2 := d.LoadValue(context.Background(), dpk, hmenum.CallSourceHMInit, false)
	if err2 != nil {
		t.Fatalf("second call: %v", err2)
	}
	if fake.getParamsetCalls.Load() != 1 {
		t.Fatalf("second call must use cache; GetParamset calls = %d", fake.getParamsetCalls.Load())
	}
}

func TestLoadValueDirectBypassesCache(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()
	fake := newFakeLoader()

	const channelAddr = "AABBCCDD:1"
	const param = hmenum.Parameter("STATE")
	fake.setGetParamset(channelAddr, hmenum.ParamsetKeyValues, map[string]any{string(param): "fresh"}, nil)
	d.SetValueLoader(fake)

	dpk := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyValues, string(param))

	// Populate cache with stale value.
	d.OnObservedValue(dpk, "stale")

	// direct=true bypasses cache read.
	v, obs, err := d.LoadValue(context.Background(), dpk, hmenum.CallSourceHMInit, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !obs {
		t.Fatal("expected observed=true")
	}
	if v != "fresh" {
		t.Fatalf("expected %q, got %v", "fresh", v)
	}
	if fake.getParamsetCalls.Load() != 1 {
		t.Fatal("loader must be called for direct=true")
	}
}

func TestLoadValueMasterBatchFillsAllParameters(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()
	fake := newFakeLoader()

	const channelAddr = "AABBCCDD:1"
	fake.setGetParamset(channelAddr, hmenum.ParamsetKeyMaster, map[string]any{
		"A": 1.0,
		"B": 2.0,
		"C": 3.0,
	}, nil)
	d.SetValueLoader(fake)

	dpkA := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyMaster, "A")
	dpkB := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyMaster, "B")
	dpkC := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyMaster, "C")

	v, obs, err := d.LoadValue(context.Background(), dpkA, hmenum.CallSourceHMInit, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !obs {
		t.Fatal("expected observed=true for A")
	}
	if v != 1.0 {
		t.Fatalf("expected 1.0, got %v", v)
	}

	// GetParamset should have been called exactly once.
	if fake.getParamsetCalls.Load() != 1 {
		t.Fatalf("GetParamset must be called once, got %d", fake.getParamsetCalls.Load())
	}

	// B and C should now be in the cache without any additional loader call.
	vB, obsB, hitB := d.cache.get(dpkB)
	if !hitB || !obsB || vB != 2.0 {
		t.Fatalf("B not in cache: hitB=%v obsB=%v vB=%v", hitB, obsB, vB)
	}
	vC, obsC, hitC := d.cache.get(dpkC)
	if !hitC || !obsC || vC != 3.0 {
		t.Fatalf("C not in cache: hitC=%v obsC=%v vC=%v", hitC, obsC, vC)
	}
}

func TestLoadValueMasterMissingParameterCachedAsSentinel(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()
	fake := newFakeLoader()

	const channelAddr = "AABBCCDD:1"
	// GetParamset only returns A; B is absent.
	fake.setGetParamset(channelAddr, hmenum.ParamsetKeyMaster, map[string]any{
		"A": 1.0,
	}, nil)
	d.SetValueLoader(fake)

	dpkB := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyMaster, "B")

	v, obs, err := d.LoadValue(context.Background(), dpkB, hmenum.CallSourceHMInit, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if obs {
		t.Fatal("missing parameter must return observed=false (sentinel)")
	}
	if v != nil {
		t.Fatalf("missing parameter must return nil value, got %v", v)
	}

	// The sentinel entry should be in the cache.
	_, sentinelObs, hit := d.cache.get(dpkB)
	if !hit {
		t.Fatal("sentinel entry must be cached")
	}
	if sentinelObs {
		t.Fatal("cached sentinel must have observed=false")
	}
}

func TestLoadValueValuesParamsetErrorCachesSentinel(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()
	fake := newFakeLoader()

	const channelAddr = "AABBCCDD:1"
	const param = hmenum.Parameter("LEVEL")
	loaderErr := errors.New("transport error")
	fake.setGetParamset(channelAddr, hmenum.ParamsetKeyValues, nil, loaderErr)
	d.SetValueLoader(fake)

	dpk := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyValues, string(param))

	// First call: GetParamset returns error → sentinel cached for requested param.
	_, _, err := d.LoadValue(context.Background(), dpk, hmenum.CallSourceHMInit, false)
	if err == nil {
		t.Fatal("expected error from loader")
	}

	firstCalls := fake.getParamsetCalls.Load()
	if firstCalls != 1 {
		t.Fatalf("expected 1 loader call, got %d", firstCalls)
	}

	// Sentinel should be in cache — change loader to succeed for the next call.
	fake.setGetParamset(channelAddr, hmenum.ParamsetKeyValues, map[string]any{string(param): 42.0}, nil)

	// Second call within sentinelCacheTTL: must hit sentinel cache, not call loader.
	v2, obs2, err2 := d.LoadValue(context.Background(), dpk, hmenum.CallSourceHMInit, false)
	if err2 != nil {
		t.Fatalf("second call should use sentinel, got error: %v", err2)
	}
	if obs2 {
		t.Fatal("sentinel hit must return observed=false")
	}
	if v2 != nil {
		t.Fatalf("sentinel hit must return nil value, got %v", v2)
	}
	if fake.getParamsetCalls.Load() != firstCalls {
		t.Fatal("second call within sentinel TTL must NOT invoke loader again")
	}
}

func TestLoadValueSingleflightDeduplicatesConcurrentLoads(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()
	fake := newFakeLoader()

	const channelAddr = "AABBCCDD:1"
	const param = hmenum.Parameter("LEVEL")

	// blockCh blocks GetParamset until we release it (VALUES path uses GetParamset).
	blockCh := make(chan struct{})
	fake.blockCh = blockCh
	fake.setGetParamset(channelAddr, hmenum.ParamsetKeyValues, map[string]any{string(param): 99.0}, nil)
	d.SetValueLoader(fake)

	dpk := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyValues, string(param))

	const n = 50
	results := make([]any, n)
	var wg sync.WaitGroup
	wg.Add(n)

	// Ensure all goroutines are blocked inside singleflight before releasing.
	var started atomic.Int32
	for i := range n {
		go func(idx int) {
			defer wg.Done()
			started.Add(1)
			v, _, _ := d.LoadValue(context.Background(), dpk, hmenum.CallSourceHMInit, false)
			results[idx] = v
		}(i)
	}

	// Wait until all goroutines have incremented the started counter, then
	// give them a moment to enter singleflight.Do before unblocking.
	for started.Load() < n {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(5 * time.Millisecond)

	close(blockCh) // release the single loader call
	wg.Wait()

	// GetParamset must have been called exactly once — singleflight deduplicated all 50 goroutines.
	if calls := fake.getParamsetCalls.Load(); calls != 1 {
		t.Fatalf("GetParamset must be called exactly once under singleflight, got %d", calls)
	}

	// Every goroutine must have gotten 99.0.
	for i, v := range results {
		if v != 99.0 {
			t.Fatalf("goroutine %d got %v, want 99.0", i, v)
		}
	}
}

func TestLoadValueSingleflightSeparatesByChannel(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()
	fake := newFakeLoader()

	const param = hmenum.Parameter("LEVEL")
	// VALUES singleflight key is per-parameter (channel+":V:"+param), so each
	// channel address produces its own key → two independent GetParamset calls.
	fake.setGetParamset("AABBCCDD:1", hmenum.ParamsetKeyValues, map[string]any{string(param): 1.0}, nil)
	fake.setGetParamset("AABBCCDD:2", hmenum.ParamsetKeyValues, map[string]any{string(param): 2.0}, nil)
	d.SetValueLoader(fake)

	dpk1 := makeDPKey("HmIP-RF", "AABBCCDD:1", hmenum.ParamsetKeyValues, string(param))
	dpk2 := makeDPKey("HmIP-RF", "AABBCCDD:2", hmenum.ParamsetKeyValues, string(param))

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		d.LoadValue(context.Background(), dpk1, hmenum.CallSourceHMInit, false) //nolint:errcheck // race test: error not relevant, only call count matters
	}()
	go func() {
		defer wg.Done()
		d.LoadValue(context.Background(), dpk2, hmenum.CallSourceHMInit, false) //nolint:errcheck // race test: error not relevant, only call count matters
	}()

	wg.Wait()

	// Each (channel, param) pair has a distinct singleflight key → 2 GetParamset calls.
	if calls := fake.getParamsetCalls.Load(); calls != 2 {
		t.Fatalf("expected 2 GetParamset calls (one per channel), got %d", calls)
	}
}

func TestLoadValueMasterBatchSingleflightCoalescesAcrossParameters(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()
	fake := newFakeLoader()

	const channelAddr = "AABBCCDD:1"

	// Block GetParamset so both goroutines enter singleflight before first completes.
	blockCh := make(chan struct{})
	fake.getParamsetFn = func(address string, _ hmenum.ParamsetKey) (map[string]any, error) {
		<-blockCh
		return map[string]any{"PARAM_A": 10.0, "PARAM_B": 20.0}, nil
	}
	d.SetValueLoader(fake)

	dpkA := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyMaster, "PARAM_A")
	dpkB := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyMaster, "PARAM_B")

	var wg sync.WaitGroup
	wg.Add(2)

	var errA, errB error
	var valA, valB any

	go func() {
		defer wg.Done()
		valA, _, errA = d.LoadValue(context.Background(), dpkA, hmenum.CallSourceHMInit, false)
	}()
	go func() {
		defer wg.Done()
		valB, _, errB = d.LoadValue(context.Background(), dpkB, hmenum.CallSourceHMInit, false)
	}()

	// Give goroutines time to enter singleflight.Do before releasing.
	time.Sleep(10 * time.Millisecond)
	close(blockCh)
	wg.Wait()

	if errA != nil || errB != nil {
		t.Fatalf("unexpected errors: A=%v B=%v", errA, errB)
	}

	// GetParamset called exactly once — singleflight coalesced both.
	if calls := fake.getParamsetCalls.Load(); calls != 1 {
		t.Fatalf("GetParamset must be called exactly once, got %d", calls)
	}

	if valA != 10.0 {
		t.Fatalf("PARAM_A: got %v, want 10.0", valA)
	}
	if valB != 20.0 {
		t.Fatalf("PARAM_B: got %v, want 20.0", valB)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Cluster C — DataPoint propagation
// ─────────────────────────────────────────────────────────────────────────────

func TestLoadValueValuesPushesToDataPoint(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()

	const channelAddr = "AABBCCDD:1"
	ch := d.AddChannel(channelAddr, 1, "", hmenum.ParamsetKeyValues)

	const param = hmenum.Parameter("LEVEL")
	floatDP := makeFloatDP(channelAddr, param)
	ch.Put(floatDP)

	fake := newFakeLoader()
	fake.setGetParamset(channelAddr, hmenum.ParamsetKeyValues, map[string]any{string(param): 42.5}, nil)
	d.SetValueLoader(fake)

	dpk := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyValues, string(param))

	_, _, err := d.LoadValue(context.Background(), dpk, hmenum.CallSourceHMInit, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, observed := floatDP.RawValue()
	if !observed {
		t.Fatal("Float DP must have observed=true after LoadValue")
	}
	if raw != 42.5 {
		t.Fatalf("Float DP RawValue = %v, want 42.5", raw)
	}
}

func TestLoadValueMasterPushesToMasterDataPoint(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()

	const channelAddr = "AABBCCDD:1"
	ch := d.AddChannel(channelAddr, 1, "", hmenum.ParamsetKeyMaster)

	const paramName = "TEMPERATURE_OFFSET"
	masterDP := makeMasterFloatDP(channelAddr, paramName)
	ch.PutMaster(masterDP)

	fake := newFakeLoader()
	fake.setGetParamset(channelAddr, hmenum.ParamsetKeyMaster, map[string]any{
		paramName: 2.5,
	}, nil)
	d.SetValueLoader(fake)

	dpk := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyMaster, paramName)

	_, _, err := d.LoadValue(context.Background(), dpk, hmenum.CallSourceHMInit, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	raw, observed := masterDP.RawValue()
	if !observed {
		t.Fatal("Master Float DP must have observed=true after LoadValue")
	}
	if raw != 2.5 {
		t.Fatalf("Master Float DP RawValue = %v, want 2.5", raw)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Cluster D — VALUES sibling-guard (bulk fill must not clobber observed DPs)
// ─────────────────────────────────────────────────────────────────────────────

// TestLoadValueValuesParamsetSiblingGuard verifies that runLoadValuesParamset
// respects the not-yet-observed gate when bulk-filling siblings from a single
// GetParamset(VALUES) call:
//
//   - REQUESTED param is always applied (even when previously observed).
//   - SIBLING_A, which was already observed with a known value, is NOT
//     overwritten by the bulk response.
//   - SIBLING_B, which has never been observed, is filled from the bulk
//     response (gap-fill behaviour).
//   - The entire operation issues exactly one GetParamset call.
func TestLoadValueValuesParamsetSiblingGuard(t *testing.T) {
	t.Parallel()

	d := makeTestDevice()

	const channelAddr = "AABBCCDD:1"
	ch := d.AddChannel(channelAddr, 1, "", hmenum.ParamsetKeyValues)

	// Register three DPs on the channel so the production code can look them
	// up via ch.Parameter and apply OnWireValue / read RawValue.
	const (
		requested = hmenum.Parameter("LEVEL")
		siblingA  = hmenum.Parameter("STATE")
		siblingB  = hmenum.Parameter("LOWBAT")
	)
	reqDP := makeFloatDP(channelAddr, requested)
	sibADP := makeFloatDP(channelAddr, siblingA)
	sibBDP := makeFloatDP(channelAddr, siblingB)
	ch.Put(reqDP)
	ch.Put(sibADP)
	ch.Put(sibBDP)

	// Mark SIBLING_A as already-observed with value 7.0 by calling OnWireValue
	// directly on the DP (same path the event-bus uses on a push event).
	sibADP.OnWireValue(7.0)
	if _, obs := sibADP.RawValue(); !obs {
		t.Fatal("test setup: sibADP must be observed before the loader call")
	}

	fake := newFakeLoader()
	// GetParamset(VALUES) returns all three params; SIBLING_A carries a
	// different value (99.0) that must NOT be written into the DP.
	fake.setGetParamset(channelAddr, hmenum.ParamsetKeyValues, map[string]any{
		string(requested): 0.5,
		string(siblingA):  99.0,
		string(siblingB):  1.0,
	}, nil)
	d.SetValueLoader(fake)

	dpk := makeDPKey("HmIP-RF", channelAddr, hmenum.ParamsetKeyValues, string(requested))

	_, _, err := d.LoadValue(context.Background(), dpk, hmenum.CallSourceHMInit, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// (a) REQUESTED must be applied and observed.
	rawReq, obsReq := reqDP.RawValue()
	if !obsReq {
		t.Fatal("(a) REQUESTED DP must be observed after LoadValue")
	}
	if rawReq != 0.5 {
		t.Fatalf("(a) REQUESTED DP value = %v, want 0.5", rawReq)
	}

	// (b) SIBLING_A must retain its pre-existing observed value (7.0), not be
	// overwritten by the bulk response value (99.0).
	rawA, obsA := sibADP.RawValue()
	if !obsA {
		t.Fatal("(b) SIBLING_A must still be observed after bulk fill")
	}
	if rawA != 7.0 {
		t.Fatalf("(b) SIBLING_A value = %v, want 7.0 (must not be overwritten)", rawA)
	}

	// (c) SIBLING_B was not observed before the call; the bulk fill must
	// have applied the value from the paramset response.
	rawB, obsB := sibBDP.RawValue()
	if !obsB {
		t.Fatal("(c) SIBLING_B must be observed after gap-fill")
	}
	if rawB != 1.0 {
		t.Fatalf("(c) SIBLING_B value = %v, want 1.0", rawB)
	}

	// (d) Exactly one GetParamset call for the whole operation.
	if calls := fake.getParamsetCalls.Load(); calls != 1 {
		t.Fatalf("(d) expected exactly 1 GetParamset call, got %d", calls)
	}
}

// TestLoadValueValuesParamsetSkippedForVirtualDevices pins the #3228 fix: a
// VirtualDevice (heating group) has no physical device behind it, so a VALUES
// fallback can only return the CCU-internal default (e.g. 0 for a not-yet-measured
// ACTUAL_TEMPERATURE right after a CCU restart). The fallback must be skipped so
// the data point stays unobserved until a real value arrives via event — instead
// of being written with a spurious 0.
// TestLoadValueValuesParamsetSkippedForUnqueryableInterfaces pins that the
// per-parameter VALUES fallback is skipped for interfaces whose GetParamset can
// only return a CCU-internal placeholder rather than a device-fresh reading:
// VirtualDevices (aggregated, no physical device) and BidCos-RF (passive/battery
// devices that cannot be actively queried). The data point must stay unobserved
// until a real value arrives via event (#3228, #3260).
func TestLoadValueValuesParamsetSkippedForUnqueryableInterfaces(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		interfaceID string
		iface       hmenum.Interface
		address     string
		model       string
	}{
		{
			name:        "VirtualDevices",
			interfaceID: "VirtualDevices",
			iface:       hmenum.InterfaceVirtualDevices,
			address:     "INT0000001",
			model:       "HmIP-HEATING",
		},
		{
			name:        "BidCosRF",
			interfaceID: "BidCos-RF",
			iface:       hmenum.InterfaceBidCosRF,
			address:     "LEQ0000001",
			model:       "HM-CC-RT-DN",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			d := New(Config{
				InterfaceID: tc.interfaceID,
				Interface:   tc.iface,
				Address:     tc.address,
				Model:       tc.model,
				Name:        tc.name,
			})

			channelAddr := tc.address + ":1"
			ch := d.AddChannel(channelAddr, 1, "", hmenum.ParamsetKeyValues)
			const actualTemp = hmenum.Parameter("ACTUAL_TEMPERATURE")
			dp := makeFloatDP(channelAddr, actualTemp)
			ch.Put(dp)

			fake := newFakeLoader()
			// The CCU returns the default 0 for an ACTUAL_TEMPERATURE that has not
			// been measured yet. This must never reach the data point.
			fake.setGetParamset(channelAddr, hmenum.ParamsetKeyValues, map[string]any{
				string(actualTemp): 0.0,
			}, nil)
			d.SetValueLoader(fake)

			dpk := makeDPKey(tc.interfaceID, channelAddr, hmenum.ParamsetKeyValues, string(actualTemp))

			_, obs, err := d.LoadValue(context.Background(), dpk, hmenum.CallSourceHMInit, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// (a) No fallback RPC is issued for an unqueryable interface's VALUES.
			if calls := fake.getParamsetCalls.Load(); calls != 0 {
				t.Fatalf("(a) expected 0 GetParamset calls for %s, got %d", tc.name, calls)
			}
			// (b) LoadValue returns the sentinel (observed=false).
			if obs {
				t.Fatalf("(b) expected observed=false (sentinel) for %s VALUES fallback", tc.name)
			}
			// (c) The data point stays unobserved — no spurious 0 placeholder is written.
			if _, dpObs := dp.RawValue(); dpObs {
				t.Fatalf("(c) ACTUAL_TEMPERATURE must stay unobserved for %s (no 0 placeholder)", tc.name)
			}
		})
	}
}
