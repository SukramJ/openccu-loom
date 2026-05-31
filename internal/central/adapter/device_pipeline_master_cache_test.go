// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// openAdapterTestDB opens an in-memory SQLite database with all migrations
// applied. Registered as a t.Cleanup, so it is closed automatically when the
// test finishes.
func openAdapterTestDB(t *testing.T) *sqlite.MasterValuesStore {
	t.Helper()
	db, err := sqlite.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlite.NewMasterValuesStore(db)
}

// newMasterFloatDP builds a minimal generic.DataPoint[float64] keyed under
// ParamsetKeyMaster so it lands in ch.PutMaster.
func newMasterFloatDP(param hmenum.Parameter, channelAddr string) *generic.DataPoint[float64] {
	return generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "if1",
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
}

// buildChannelWithMasterParam creates a device registered on the central, adds
// one channel (address channelAddr, interface ifID), and installs a single
// MASTER float64 data point for the parameter name param.
func buildChannelWithMasterParam(
	t *testing.T,
	c *central.CentralUnit,
	ifID string,
	devAddr, channelAddr string,
	param hmenum.Parameter,
) *device.Channel {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID: ifID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     devAddr,
		Model:       "HmIP-PS",
	})
	c.ModelRegistry.Put(dev)
	ch := dev.AddChannel(channelAddr, 1, "SWITCH", hmenum.ParamsetKeyMaster)
	dp := newMasterFloatDP(param, channelAddr)
	ch.PutMaster(dp)
	return ch
}

// countingBackend is a fake backends.Operations that counts GetParamset calls
// and returns a fixed map on success.
type countingBackend struct {
	paramsetFakeOps
	calls  atomic.Int32
	result map[string]any
	err    error
}

func (b *countingBackend) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	b.calls.Add(1)
	if b.err != nil {
		return nil, b.err
	}
	out := make(map[string]any, len(b.result))
	for k, v := range b.result {
		out[k] = v
	}
	return out, nil
}

// TestSeedMasterValues_CacheHit_SkipsBackend verifies that when a warm cache
// entry exists for the channel, seedMasterValues applies the cached value to
// the MASTER DP without calling GetParamset on the backend.
func TestSeedMasterValues_CacheHit_SkipsBackend(t *testing.T) {
	t.Parallel()

	store := openAdapterTestDB(t)
	ctx := context.Background()

	// Pre-populate the cache with FOO=7.
	if err := store.SaveChannel(ctx, "c1", "if1", "DEV:1", map[string]any{"FOO": float64(7)}); err != nil {
		t.Fatalf("SaveChannel: %v", err)
	}

	c, _ := central.New(central.Config{Name: "c1"})
	ch := buildChannelWithMasterParam(t, c, "if1", "DEV", "DEV:1", "FOO")

	pipeline := NewDevicePipeline(c).WithMasterValuesStore(store, "c1")

	backend := &countingBackend{}
	pipeline.seedMasterValues(ctx, ch, backend, nil)

	if got := backend.calls.Load(); got != 0 {
		t.Errorf("backend.GetParamset called %d time(s), want 0 (cache hit)", got)
	}

	dp := ch.MasterParameter("FOO")
	if dp == nil {
		t.Fatal("MASTER DP 'FOO' is nil after seedMasterValues")
	}
	typed, ok := dp.(*generic.DataPoint[float64])
	if !ok {
		t.Fatalf("unexpected DP type %T", dp)
	}
	v, observed := typed.Value()
	if !observed {
		t.Fatal("MASTER DP 'FOO' has no observed value after cache hit")
	}
	if v != 7.0 {
		t.Errorf("MASTER DP 'FOO' = %v, want 7.0", v)
	}
}

// TestSeedMasterValues_CacheMiss_PersistsValuesFromBackend verifies that when
// no cache entry exists, seedMasterValues calls the backend's GetParamset,
// applies the result to the MASTER DP, and persists the values in the store.
func TestSeedMasterValues_CacheMiss_PersistsValuesFromBackend(t *testing.T) {
	t.Parallel()

	store := openAdapterTestDB(t)
	ctx := context.Background()

	c, _ := central.New(central.Config{Name: "c1"})
	ch := buildChannelWithMasterParam(t, c, "if1", "DEV", "DEV:1", "FOO")

	pipeline := NewDevicePipeline(c).WithMasterValuesStore(store, "c1")

	backend := &countingBackend{result: map[string]any{"FOO": float64(11)}}
	pipeline.seedMasterValues(ctx, ch, backend, nil)

	// Backend must have been called exactly once.
	if got := backend.calls.Load(); got != 1 {
		t.Errorf("backend.GetParamset called %d time(s), want 1", got)
	}

	// Channel DP must reflect the value from the backend.
	dp := ch.MasterParameter("FOO")
	if dp == nil {
		t.Fatal("MASTER DP 'FOO' is nil after seedMasterValues")
	}
	typed, ok := dp.(*generic.DataPoint[float64])
	if !ok {
		t.Fatalf("unexpected DP type %T", dp)
	}
	v, observed := typed.Value()
	if !observed {
		t.Fatal("MASTER DP 'FOO' has no observed value after cache miss + backend fetch")
	}
	if v != 11.0 {
		t.Errorf("MASTER DP 'FOO' = %v, want 11.0", v)
	}

	// The store must now hold the persisted value.
	cached, hit, err := store.LoadChannel(ctx, "c1", "if1", "DEV:1")
	if err != nil {
		t.Fatalf("LoadChannel: %v", err)
	}
	if !hit {
		t.Fatal("store.LoadChannel: ok = false, values should have been persisted")
	}
	if cached["FOO"] != float64(11) {
		t.Errorf("cached FOO = %v, want 11.0", cached["FOO"])
	}
}
