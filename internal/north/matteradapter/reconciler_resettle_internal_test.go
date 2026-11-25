// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"context"
	"log/slog"
	"testing"
	"time"

	fabricdevice "thingfabric.dev/go-fabric/device"
	"thingfabric.dev/go-fabric/fabrictest"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// plugDevice builds an OnOffPlugInUnit under key, as the assembler builds
// a device the walk owns.
func plugDevice(t *testing.T, key matterendpoint.SourceKey) *fabricdevice.Device {
	t.Helper()
	typ, ok := fabricdevice.Lookup(uint32(fabricdevice.OnOffPlugInUnitID))
	if !ok {
		t.Fatal("OnOffPlugInUnit is not in the device layer")
	}
	d, err := fabricdevice.New(typ, fabricdevice.Options{
		StableKey: key, Name: "Plug", DeviceAddress: key.DeviceAddress,
	})
	if err != nil {
		t.Fatalf("build %s: %v", key, err)
	}
	return d
}

// TestReconcilerHoldsOnlyWhatABatchServed pins the resettle after a batch
// error: a device whose key another partition already serves is refused
// by the batch, and the reconciler neither holds it nor forwards a
// reachability flip to it, so the next sync tries the key again instead of
// treating it as served.
func TestReconcilerHoldsOnlyWhatABatchServed(t *testing.T) {
	t.Parallel()
	site := fabrictest.NewSite(t, fabrictest.Options{})
	pair := site.CommissionedPair(fabrictest.PairOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	agg := pair.Device.Node().Aggregator()
	r := NewReconciler(agg, New(Config{}, slog.New(slog.DiscardHandler)), nil, slog.New(slog.DiscardHandler))

	key := matterendpoint.SourceKey{
		CentralName: "ccu1", DeviceAddress: "PLG0001", ChannelNo: 1,
		DPKind: matterendpoint.DPKindCustom, DPKey: "STATE",
	}
	if err := agg.Partition("elsewhere").Add(ctx, plugDevice(t, key)); err != nil {
		t.Fatalf("serve the key elsewhere: %v", err)
	}

	refused := plugDevice(t, key)
	r.mu.Lock()
	err := r.syncCentral(ctx, Described{Scope: "ccu1", Endpoints: []Endpoint{{
		Device: refused, Available: func() bool { return true },
	}}})
	_, held := r.held["ccu1"][key.String()]
	r.mu.Unlock()
	if err == nil {
		t.Fatal("the batch took a key another partition serves; the check measures nothing")
	}
	if held {
		t.Errorf("the reconciler holds %s although the batch refused it (%v)", key, err)
	}
	r.reachMu.Lock()
	_, indexed := r.reach["ccu1"][key.DeviceAddress][refused]
	r.reachMu.Unlock()
	if indexed {
		t.Errorf("a refused device is indexed for reachability flips")
	}
}
