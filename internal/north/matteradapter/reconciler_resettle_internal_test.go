// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	fabricdevice "thingfabric.dev/go-fabric/device"
	"thingfabric.dev/go-fabric/fabrictest"
	"thingfabric.dev/go-fabric/node"

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

// TestReconcilerKeepsTheReplacedDeviceABatchUndid pins the resettle of a
// Replace the batch refused after fn returned: the new device gained a part
// whose key another partition serves, so the batch puts the replaced device
// back (node.BatchError.Calls, not applied), and the reconciler holds the
// device held before — not the refused one, and not nothing, which would
// make the next sync Add a key the partition still serves.
func TestReconcilerKeepsTheReplacedDeviceABatchUndid(t *testing.T) {
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
	partKey := matterendpoint.SourceKey{
		CentralName: "ccu1", DeviceAddress: "PLG0001", ChannelNo: 2,
		DPKind: matterendpoint.DPKindCustom, DPKey: "STATE",
	}
	available := func() bool { return true }

	first := plugDevice(t, key)
	r.mu.Lock()
	err := r.syncCentral(ctx, Described{Scope: "ccu1", Endpoints: []Endpoint{{Device: first, Available: available}}})
	r.mu.Unlock()
	if err != nil {
		t.Fatalf("first sync: %v", err)
	}
	if err := agg.Partition("elsewhere").Add(ctx, plugDevice(t, partKey)); err != nil {
		t.Fatalf("serve the part key elsewhere: %v", err)
	}

	// A bridged node takes a part of any device type, as go-fabric's own
	// batch tests compose one (node/forget_served_test.go bridgedWithParts).
	bridged, err := fabricdevice.NewBridgedNode(fabricdevice.Options{StableKey: key, Name: "Plug", DeviceAddress: key.DeviceAddress})
	if err != nil {
		t.Fatalf("build the bridged node: %v", err)
	}
	if err := bridged.AddPart(plugDevice(t, partKey)); err != nil {
		t.Fatalf("add the part: %v", err)
	}
	next := bridged.Device
	r.mu.Lock()
	err = r.syncCentral(ctx, Described{Scope: "ccu1", Endpoints: []Endpoint{{Device: next, Available: available}}})
	h := r.held["ccu1"][key.String()]
	r.mu.Unlock()
	if !errors.Is(err, node.ErrDuplicateKey) {
		t.Fatalf("the batch took a part key another partition serves (err %v); the check measures nothing", err)
	}
	switch {
	case h == nil:
		t.Errorf("the reconciler dropped %s although the partition still serves the replaced device", key)
	case h.dev != first:
		t.Errorf("the reconciler holds the refused device under %s, not the one the partition serves", key)
	}
}

// failingKeys is a DeviceKeyLister whose store cannot be read.
type failingKeys struct{}

func (failingKeys) DeviceKeys(context.Context, string, string) ([]matterendpoint.SourceKey, error) {
	return nil, errors.New("store unavailable")
}

// TestReconcilerQueuesAForgetItCouldNotLookUpAgain pins that a deleted
// device whose stored keys could not be read stays queued: the queue is
// taken at the start of a sync, so a device dropped from it would keep its
// endpoint numbers until the next restart.
func TestReconcilerQueuesAForgetItCouldNotLookUpAgain(t *testing.T) {
	t.Parallel()
	site := fabrictest.NewSite(t, fabrictest.Options{})
	pair := site.CommissionedPair(fabrictest.PairOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	agg := pair.Device.Node().Aggregator()
	r := NewReconciler(agg, New(Config{}, slog.New(slog.DiscardHandler)), failingKeys{}, slog.New(slog.DiscardHandler))

	r.QueueForget("ccu1", "PLG0001")
	if err := r.Sync(ctx, nil); err == nil {
		t.Fatal("the sync read keys from a store that cannot be read; the check measures nothing")
	}
	r.forgetMu.Lock()
	_, queued := r.forget["ccu1"]["PLG0001"]
	r.forgetMu.Unlock()
	if !queued {
		t.Error("a device whose stored keys could not be read left the forget queue")
	}
}

// TestBatchFailureNamesWhatTheNodeDoesNotServe pins the classification a
// sync's caller reads: a failed reassembly or a stopped node leaves the
// described topology unserved (ErrNotServed), a call the batch refused does
// not — the rest of the batch is served.
func TestBatchFailureNamesWhatTheNodeDoesNotServe(t *testing.T) {
	t.Parallel()
	refused := &node.BatchCallError{Op: node.BatchAdd, Err: node.ErrDuplicateKey}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"reassembly", &node.BatchError{Reassembly: errors.New("bridge refused")}, true},
		{"reassembly beside a refused call", &node.BatchError{Calls: []*node.BatchCallError{refused}, Reassembly: errors.New("bridge refused")}, true},
		{"stopped node", node.ErrStopped, true},
		{"refused call", &node.BatchError{Calls: []*node.BatchCallError{refused}}, false},
		{"fn's own error", node.ErrUnknownKey, false},
	} {
		got := errors.Is(batchFailure(tc.err), ErrNotServed)
		if got != tc.want {
			t.Errorf("%s: ErrNotServed = %v, want %v", tc.name, got, tc.want)
		}
		if !errors.Is(batchFailure(tc.err), tc.err) {
			t.Errorf("%s: the classification hides the batch's error", tc.name)
		}
	}
}

// TestUnfinishedForgetsAreTheAppliedCallsThatFailed pins which calls a
// batch result names: an applied call whose store work failed is
// unfinished — for a Forget go-fabric says to make it again — and a refused
// or undone one is undone; a failed reassembly, whose erasures go-fabric
// records and runs itself, names neither, and fn's own error names nothing.
func TestUnfinishedForgetsAreTheAppliedCallsThatFailed(t *testing.T) {
	t.Parallel()
	applied := matterendpoint.SourceKey{CentralName: "ccu1", DeviceAddress: "A", ChannelNo: 1, DPKind: matterendpoint.DPKindCustom, DPKey: "STATE"}
	refused := matterendpoint.SourceKey{CentralName: "ccu1", DeviceAddress: "B", ChannelNo: 1, DPKind: matterendpoint.DPKindCustom, DPKey: "STATE"}
	err := &node.BatchError{
		Calls: []*node.BatchCallError{
			{Op: node.BatchForget, Key: applied, Applied: true, Err: errors.New("erasure not recorded")},
			{Op: node.BatchForget, Key: refused, Err: node.ErrUnknownKey},
		},
		Reassembly: errors.New("bridge refused"),
	}
	undone, unfinished := batchCalls(err)
	if _, ok := unfinished[applied.String()]; !ok || len(unfinished) != 1 {
		t.Errorf("unfinished = %v, want only %s", unfinished, applied)
	}
	if _, ok := undone[refused.String()]; !ok || len(undone) != 1 {
		t.Errorf("undone = %v, want only %s", undone, refused)
	}
	if u, f := batchCalls(node.ErrUnknownKey); u != nil || f != nil {
		t.Errorf("fn's own error names calls: undone %v, unfinished %v", u, f)
	}
}
