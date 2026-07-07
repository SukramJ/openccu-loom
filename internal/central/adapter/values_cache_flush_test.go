// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// fakeSaver is a spy [valuesCacheSaver] that records every batch passed to
// SaveBatch so tests can assert exactly which (channel, parameter) keys
// were written without touching SQLite.
type fakeSaver struct {
	mu      sync.Mutex
	batches [][]sqlite.SaveEntry
}

func (f *fakeSaver) SaveBatch(_ context.Context, entries []sqlite.SaveEntry) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]sqlite.SaveEntry, len(entries))
	copy(cp, entries)
	f.batches = append(f.batches, cp)
	return nil
}

// writtenKeys flattens every recorded batch into the set of
// "channelAddress|parameter" keys that were ever passed to SaveBatch.
func (f *fakeSaver) writtenKeys() map[string]struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]struct{})
	for _, batch := range f.batches {
		for i := range batch {
			out[batch[i].ChannelAddress+"|"+batch[i].ParameterName] = struct{}{}
		}
	}
	return out
}

// buildTwoLiveDPCentral registers one central with one device/channel that
// carries two VALUES data points ("STATE" and "LEVEL"), both driven live via
// OnWireValue. A dirty-tracked flush of a single key must only ever touch
// one of the two, even though both currently qualify as persistable.
func buildTwoLiveDPCentral(t *testing.T) (reg *central.Registry, centralName string) {
	t.Helper()
	centralName = "flush-central"
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: "if1",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "DEV",
		Model:       "HmIP-PS",
	})
	c.ModelRegistry.Put(dev)
	ch := dev.AddChannel("DEV:1", 1, "SWITCH", hmenum.ParamsetKeyValues)

	state := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID: "if1", ChannelAddress: "DEV:1",
			ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	state.OnWireValue(true)
	ch.Put(state)

	level := generic.NewDataPoint[float64](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID: "if1", ChannelAddress: "DEV:1",
			ParamsetKey: hmenum.ParamsetKeyValues, Parameter: "LEVEL",
		},
		Descriptor: hmproto.ParameterData{
			Type: hmenum.ParameterTypeFloat, Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	level.OnWireValue(float64(50))
	ch.Put(level)

	if state.Source() != hmenum.ValueSourceLive || level.Source() != hmenum.ValueSourceLive {
		t.Fatalf("setup: both DPs must be live, got STATE=%s LEVEL=%s", state.Source(), level.Source())
	}

	reg = central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	return reg, centralName
}

// TestFlushOnce_DirtyKey_OnlyPersistsChangedKey is the failing-first
// reproducer for narrowing dirty tracking from a whole-central boolean to
// a per-(channel, parameter) key set: even though both STATE and LEVEL are
// live and therefore persistable, a tick that only saw STATE change must
// UPSERT only STATE — not re-serialise every live/stale DP of the central.
func TestFlushOnce_DirtyKey_OnlyPersistsChangedKey(t *testing.T) {
	t.Parallel()

	reg, centralName := buildTwoLiveDPCentral(t)

	tracker := newDirtyTracker()
	tracker.Register(centralName)
	// Consume the initial post-Register "walk everything" claim so the
	// test starts from a clean slate, mirroring the flusher's first tick
	// having already run once before the DP change under test happens.
	if _, ok := tracker.SwapClean(centralName); !ok {
		t.Fatal("SwapClean immediately after Register returned ok=false; want true (initial claim)")
	}
	tracker.Mark(centralName, "DEV:1", "STATE")

	saver := &fakeSaver{}
	flushOnce(context.Background(), reg, saver, tracker, nil, "tick")

	got := saver.writtenKeys()
	if len(got) != 1 {
		t.Fatalf("writtenKeys = %v, want exactly 1 key", got)
	}
	if _, ok := got["DEV:1|STATE"]; !ok {
		t.Fatalf("writtenKeys = %v, want to contain DEV:1|STATE", got)
	}
	if _, ok := got["DEV:1|LEVEL"]; ok {
		t.Fatalf("writtenKeys = %v, LEVEL must not have been written — it was never marked dirty", got)
	}
}

// TestFlushOnce_InvalidateAll_PersistsEveryLiveDP pins the fallback path:
// a central still flagged for a full walk (fresh Register, never swapped)
// must flush every persistable DP, matching the pre-P2 whole-central
// behaviour. This is the safety net for DP changes that raced the
// flusher's subscription setup.
func TestFlushOnce_InvalidateAll_PersistsEveryLiveDP(t *testing.T) {
	t.Parallel()

	reg, centralName := buildTwoLiveDPCentral(t)

	tracker := newDirtyTracker()
	tracker.Register(centralName)

	saver := &fakeSaver{}
	flushOnce(context.Background(), reg, saver, tracker, nil, "tick")

	got := saver.writtenKeys()
	if len(got) != 2 {
		t.Fatalf("writtenKeys = %v, want exactly 2 keys (full walk)", got)
	}
	for _, want := range []string{"DEV:1|STATE", "DEV:1|LEVEL"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("writtenKeys = %v, want to contain %s", got, want)
		}
	}
}

// TestFlushOnce_ShutdownWalksEveryCentralRegardlessOfTracker verifies the
// nil-tracker shutdown path still performs a full walk without consulting
// any dirty state at all.
func TestFlushOnce_ShutdownWalksEveryCentralRegardlessOfTracker(t *testing.T) {
	t.Parallel()

	reg, _ := buildTwoLiveDPCentral(t)

	saver := &fakeSaver{}
	flushOnce(context.Background(), reg, saver, nil, nil, "shutdown")

	got := saver.writtenKeys()
	if len(got) != 2 {
		t.Fatalf("writtenKeys = %v, want exactly 2 keys (shutdown full walk)", got)
	}
}
