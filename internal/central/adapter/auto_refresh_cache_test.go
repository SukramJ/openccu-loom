// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
)

// perChannelGetter is a fake backends.MasterGetter that returns a distinct map
// for each channel address and records how many times GetParamset was called.
type perChannelGetter struct {
	calls   atomic.Int32
	results map[string]map[string]any // addr → values
	err     map[string]error          // addr → error (nil = success)
}

func (g *perChannelGetter) GetParamset(_ context.Context, addr string, _ hmenum.ParamsetKey) (map[string]any, error) {
	g.calls.Add(1)
	if g.err != nil {
		if e, ok := g.err[addr]; ok && e != nil {
			return nil, e
		}
	}
	if g.results != nil {
		if m, ok := g.results[addr]; ok {
			out := make(map[string]any, len(m))
			for k, v := range m {
				out[k] = v
			}
			return out, nil
		}
	}
	return map[string]any{}, nil
}

// buildDeviceWithMasterChannels creates a device with n channels.
// Each channel gets a single MASTER float64 DP named "K".
func buildDeviceWithMasterChannels(n int, devAddr, ifID string) *device.Device {
	dev := device.New(device.Config{
		InterfaceID: ifID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     devAddr,
		Model:       "HmIP-PS",
	})
	for i := range n {
		chAddr := hmtypes.ChannelAddress(devAddr, i)
		ch := dev.AddChannel(chAddr, i, "SWITCH", hmenum.ParamsetKeyMaster)
		dp := generic.NewDataPoint[float64](generic.Spec{
			Key: hmtypes.DataPointKey{
				InterfaceID:    ifID,
				ChannelAddress: chAddr,
				ParamsetKey:    hmenum.ParamsetKeyMaster,
				Parameter:      "K",
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeFloat,
				Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			},
		})
		ch.PutMaster(dp)
	}
	return dev
}

// TestRefreshDeviceMasterCache_PerChannelGetAndPersist verifies that
// refreshDeviceMasterCache calls GetParamset for every channel of the device
// and persists the results in the store.
func TestRefreshDeviceMasterCache_PerChannelGetAndPersist(t *testing.T) {
	t.Parallel()

	store := openAdapterTestDB(t)
	ctx := context.Background()

	dev := buildDeviceWithMasterChannels(3, "DEV", "if1")

	getter := &perChannelGetter{
		results: map[string]map[string]any{
			"DEV:0": {"K": float64(100)},
			"DEV:1": {"K": float64(101)},
			"DEV:2": {"K": float64(102)},
		},
	}

	refreshDeviceMasterCache(ctx, dev, "if1", "c1", getter, store, nil)

	if got := getter.calls.Load(); got != 3 {
		t.Errorf("getter.GetParamset called %d time(s), want 3", got)
	}

	for i, want := range []float64{100, 101, 102} {
		chAddr := hmtypes.ChannelAddress("DEV", i)
		cached, hit, err := store.LoadChannel(ctx, "c1", "if1", chAddr)
		if err != nil {
			t.Fatalf("LoadChannel %s: %v", chAddr, err)
		}
		if !hit {
			t.Errorf("store miss for channel %s", chAddr)
			continue
		}
		if cached["K"] != want {
			t.Errorf("channel %s: K = %v, want %v", chAddr, cached["K"], want)
		}
	}
}

// TestRefreshDeviceMasterCache_OneChannelErrorSkipsButContinues verifies that
// a per-channel GetParamset error is tolerated: the failing channel is not
// persisted, but all other channels are still attempted and persisted.
func TestRefreshDeviceMasterCache_OneChannelErrorSkipsButContinues(t *testing.T) {
	t.Parallel()

	store := openAdapterTestDB(t)
	ctx := context.Background()

	dev := buildDeviceWithMasterChannels(3, "DEV2", "if1")

	getter := &perChannelGetter{
		results: map[string]map[string]any{
			"DEV2:0": {"K": float64(200)},
			"DEV2:1": {"K": float64(201)}, // will be overridden by error
			"DEV2:2": {"K": float64(202)},
		},
		err: map[string]error{
			"DEV2:1": errors.New("radio timeout"),
		},
	}

	refreshDeviceMasterCache(ctx, dev, "if1", "c1", getter, store, nil)

	// All three channels were attempted.
	if got := getter.calls.Load(); got != 3 {
		t.Errorf("getter.GetParamset called %d time(s), want 3", got)
	}

	// Channel 0 and 2 must be in the store.
	for _, addr := range []string{"DEV2:0", "DEV2:2"} {
		_, hit, err := store.LoadChannel(ctx, "c1", "if1", addr)
		if err != nil {
			t.Fatalf("LoadChannel %s: %v", addr, err)
		}
		if !hit {
			t.Errorf("channel %s not persisted, want it in store", addr)
		}
	}

	// Channel 1 must NOT be in the store (error path skips persist).
	_, hit, err := store.LoadChannel(ctx, "c1", "if1", "DEV2:1")
	if err != nil {
		t.Fatalf("LoadChannel DEV2:1: %v", err)
	}
	if hit {
		t.Error("channel DEV2:1 was persisted despite GetParamset error, want cache miss")
	}
}
