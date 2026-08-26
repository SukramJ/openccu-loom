// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge_test

// Black-box tests for Bridge hook setters / emitters and attachment methods:
// SetOnReassembled, SetOnFabricAdded / EmitFabricAdded,
// SetOnFabricRemoved / EmitFabricRemoved, AttachCommissioningWindow /
// CommissioningWindow, AttachRootClusters / AttachRootPartsListProvider,
// AttachAggregatorClusters / AttachAggregatorPartsListProvider.

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// ─── SetOnFabricAdded / EmitFabricAdded ──────────────────────────────────────

func TestBridge_EmitFabricAdded_CallsHook(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	var got atomic.Uint32
	b.SetOnFabricAdded(func(idx uint8) { got.Store(uint32(idx)) })
	b.EmitFabricAdded(3)
	if got.Load() != 3 {
		t.Errorf("EmitFabricAdded(3): hook received %d", got.Load())
	}
}

func TestBridge_EmitFabricAdded_NilHook_NoPanic(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	b.SetOnFabricAdded(nil) // explicit nil — must not panic
	b.EmitFabricAdded(1)    // must not panic
}

func TestBridge_SetOnFabricAdded_Replaces(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	var first, second atomic.Uint32
	b.SetOnFabricAdded(func(idx uint8) { first.Store(uint32(idx)) })
	b.SetOnFabricAdded(func(idx uint8) { second.Store(uint32(idx)) })
	b.EmitFabricAdded(7)
	if first.Load() != 0 {
		t.Errorf("first hook should not have been called after replace; got %d", first.Load())
	}
	if second.Load() != 7 {
		t.Errorf("second hook: want 7, got %d", second.Load())
	}
}

// ─── SetOnFabricRemoved / EmitFabricRemoved ──────────────────────────────────

func TestBridge_EmitFabricRemoved_CallsHook(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	var got atomic.Uint32
	b.SetOnFabricRemoved(func(idx uint8) { got.Store(uint32(idx)) })
	b.EmitFabricRemoved(5)
	if got.Load() != 5 {
		t.Errorf("EmitFabricRemoved(5): hook received %d", got.Load())
	}
}

func TestBridge_EmitFabricRemoved_NilHook_NoPanic(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	b.SetOnFabricRemoved(nil)
	b.EmitFabricRemoved(2) // must not panic
}

// ─── SetOnReassembled ────────────────────────────────────────────────────────

func TestBridge_SetOnReassembled_CalledAfterReassemble(t *testing.T) {
	t.Parallel()
	b, err := bridge.New(
		bridge.NewFakeStore(),
		emptySnapshotter,
		mdns.NewNoop(),
		bridge.Config{
			Listen:    ":0",
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "hooks-test",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})

	var called atomic.Bool
	b.SetOnReassembled(func(_ int) { called.Store(true) })
	if err := b.Reassemble(ctx); err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if !called.Load() {
		t.Error("SetOnReassembled hook was not called after Reassemble")
	}
}

func TestBridge_SetOnReassembled_NilClearsHook(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})

	var called atomic.Bool
	b.SetOnReassembled(func(_ int) { called.Store(true) })
	b.SetOnReassembled(nil) // clear
	if err := b.Reassemble(ctx); err != nil {
		t.Fatalf("Reassemble: %v", err)
	}
	if called.Load() {
		t.Error("hook should not fire after SetOnReassembled(nil)")
	}
}

// ─── AttachCommissioningWindow / CommissioningWindow ─────────────────────────

func TestBridge_CommissioningWindow_RoundTrip(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	if b.CommissioningWindow() != nil {
		t.Fatal("CommissioningWindow() should be nil before attach")
	}
	w := bridge.NewCommissioningWindow()
	b.AttachCommissioningWindow(w)
	if got := b.CommissioningWindow(); got != w {
		t.Errorf("CommissioningWindow(): want %p, got %p", w, got)
	}
}

func TestBridge_AttachCommissioningWindow_Nil_Detaches(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	w := bridge.NewCommissioningWindow()
	b.AttachCommissioningWindow(w)
	b.AttachCommissioningWindow(nil)
	if got := b.CommissioningWindow(); got != nil {
		t.Errorf("after nil attach, CommissioningWindow should be nil, got %p", got)
	}
}

// ─── AttachRootClusters / AttachRootPartsListProvider ────────────────────────

// fakeMatterClusterServer is a minimal implementation for Attach* tests.
type fakeMatterClusterServer struct {
	id uint32
}

func (f *fakeMatterClusterServer) MatterClusterID() uint32 { return f.id }

func (f *fakeMatterClusterServer) MatterRead(_ uint32) (any, bool) {
	return nil, false
}

func (f *fakeMatterClusterServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return nil
}

func (f *fakeMatterClusterServer) MatterInvoke(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, nil
}

func (f *fakeMatterClusterServer) MatterReportable() []uint32 { return nil }

// fakeDescriptor adds SetPartsListProvider to satisfy PartsListProviderSetter.
type fakeDescriptor struct {
	fakeMatterClusterServer
	setProviderCalled bool
	currentProvider   func() []uint16
}

func (d *fakeDescriptor) SetPartsListProvider(fn func() []uint16) {
	d.setProviderCalled = true
	d.currentProvider = fn
}

func TestBridge_AttachRootClusters_NoDescriptor_ReturnsFalse(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	c := &fakeMatterClusterServer{id: 0x0006} // OnOff, not Descriptor
	b.AttachRootClusters([]interfaces.MatterClusterServer{c})
	if b.AttachRootPartsListProvider(func() []uint16 { return nil }) {
		t.Error("AttachRootPartsListProvider returned true but no Descriptor cluster is mounted")
	}
}

func TestBridge_AttachRootClusters_WithDescriptor_ReturnsTrue(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	desc := &fakeDescriptor{
		fakeMatterClusterServer: fakeMatterClusterServer{id: 0x001D},
	}
	b.AttachRootClusters([]interfaces.MatterClusterServer{desc})
	if !b.AttachRootPartsListProvider(func() []uint16 { return []uint16{2, 3} }) {
		t.Error("AttachRootPartsListProvider returned false but Descriptor cluster is mounted")
	}
	if !desc.setProviderCalled {
		t.Error("SetPartsListProvider was not called on the Descriptor cluster")
	}
}

func TestBridge_AttachAggregatorClusters_NoDescriptor_ReturnsFalse(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	c := &fakeMatterClusterServer{id: 0x0003} // Identify
	b.AttachAggregatorClusters([]interfaces.MatterClusterServer{c})
	if b.AttachAggregatorPartsListProvider(func() []uint16 { return nil }) {
		t.Error("AttachAggregatorPartsListProvider returned true but no Descriptor is mounted")
	}
}

func TestBridge_AttachAggregatorClusters_WithDescriptor_ReturnsTrue(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	desc := &fakeDescriptor{
		fakeMatterClusterServer: fakeMatterClusterServer{id: 0x001D},
	}
	b.AttachAggregatorClusters([]interfaces.MatterClusterServer{desc})
	if !b.AttachAggregatorPartsListProvider(func() []uint16 { return []uint16{2} }) {
		t.Error("AttachAggregatorPartsListProvider returned false but Descriptor is mounted")
	}
	if !desc.setProviderCalled {
		t.Error("SetPartsListProvider was not called on the aggregator Descriptor")
	}
}

func TestBridge_AttachRootClusters_NilClearsServers(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	desc := &fakeDescriptor{
		fakeMatterClusterServer: fakeMatterClusterServer{id: 0x001D},
	}
	b.AttachRootClusters([]interfaces.MatterClusterServer{desc})
	b.AttachRootClusters(nil)
	// After clearing, no Descriptor should be reachable.
	if b.AttachRootPartsListProvider(func() []uint16 { return nil }) {
		t.Error("AttachRootPartsListProvider should return false after clearing root clusters")
	}
}
