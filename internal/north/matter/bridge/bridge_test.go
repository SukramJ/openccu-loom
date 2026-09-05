// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
)

// ─── in-memory store fake ─────────────────────────────────────────────
// Shared with white-box tests via [bridge.FakeStore]; see
// fakestore_helpers_test.go for the implementation.

// ─── snapshotters ─────────────────────────────────────────────────────

// emptySnapshotter produces a topology with no bridged endpoints. A fresh
// assembler per call keeps the parallel tests that share this function off
// one another's endpoint-id store.
func emptySnapshotter(ctx context.Context) (*endpoint.Topology, error) {
	return bridge.NewEmptySnapshotter()(ctx)
}

// countingSnapshotter bumps an atomic counter on each call.
type countingSnapshotter struct {
	count atomic.Int32
}

func (c *countingSnapshotter) snap(ctx context.Context) (*endpoint.Topology, error) {
	c.count.Add(1)
	return emptySnapshotter(ctx)
}

// ─── helper ───────────────────────────────────────────────────────────

// newTestBridge returns a *Bridge with a valid Config, noop advertiser,
// and empty snapshotter. Tests that need different options should call
// bridge.New directly.
func newTestBridge(t *testing.T) *bridge.Bridge {
	t.Helper()
	b, err := bridge.New(
		bridge.NewFakeStore(),
		emptySnapshotter,
		mdns.NewNoop(),
		bridge.Config{
			Listen:    ":0",
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "test-bridge",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("newTestBridge: unexpected error: %v", err)
	}
	return b
}

// ─── validation tests ─────────────────────────────────────────────────

// TestNew_RejectsNilStore verifies that New returns an error when store is nil.
func TestNew_RejectsNilStore(t *testing.T) {
	t.Parallel()
	_, err := bridge.New(nil, emptySnapshotter, mdns.NewNoop(), bridge.Config{
		VendorID:  1,
		ProductID: 2,
		NodeLabel: "x",
	}, nil)
	if err == nil {
		t.Fatal("expected non-nil error for nil store, got nil")
	}
}

// TestNew_RejectsNilSnapshotter verifies that New returns an error when snap is nil.
func TestNew_RejectsNilSnapshotter(t *testing.T) {
	t.Parallel()
	_, err := bridge.New(bridge.NewFakeStore(), nil, mdns.NewNoop(), bridge.Config{
		VendorID:  1,
		ProductID: 2,
		NodeLabel: "x",
	}, nil)
	if err == nil {
		t.Fatal("expected non-nil error for nil snapshotter, got nil")
	}
}

// TestNew_RejectsZeroVendorID verifies that New returns an error mentioning VendorID when VendorID is 0.
func TestNew_RejectsZeroVendorID(t *testing.T) {
	t.Parallel()
	_, err := bridge.New(bridge.NewFakeStore(), emptySnapshotter, mdns.NewNoop(), bridge.Config{
		VendorID:  0,
		ProductID: 2,
		NodeLabel: "x",
	}, nil)
	if err == nil {
		t.Fatal("expected non-nil error for zero VendorID, got nil")
	}
	if !strings.Contains(err.Error(), "VendorID") {
		t.Errorf("error %q does not mention VendorID", err.Error())
	}
}

// TestNew_RejectsZeroProductID verifies that New returns an error when ProductID is 0.
func TestNew_RejectsZeroProductID(t *testing.T) {
	t.Parallel()
	_, err := bridge.New(bridge.NewFakeStore(), emptySnapshotter, mdns.NewNoop(), bridge.Config{
		VendorID:  1,
		ProductID: 0,
		NodeLabel: "x",
	}, nil)
	if err == nil {
		t.Fatal("expected non-nil error for zero ProductID, got nil")
	}
}

// TestNew_RejectsEmptyNodeLabel verifies that New returns an error when NodeLabel is empty.
func TestNew_RejectsEmptyNodeLabel(t *testing.T) {
	t.Parallel()
	_, err := bridge.New(bridge.NewFakeStore(), emptySnapshotter, mdns.NewNoop(), bridge.Config{
		VendorID:  1,
		ProductID: 2,
		NodeLabel: "",
	}, nil)
	if err == nil {
		t.Fatal("expected non-nil error for empty NodeLabel, got nil")
	}
}

// TestNew_NilAdvertiserOK verifies that New succeeds with a nil advertiser and Start works.
func TestNew_NilAdvertiserOK(t *testing.T) {
	t.Parallel()
	b, err := bridge.New(bridge.NewFakeStore(), emptySnapshotter, nil, bridge.Config{
		Listen:    ":0",
		VendorID:  0x1234,
		ProductID: 0x5678,
		NodeLabel: "test-nil-adv",
	}, nil)
	if err != nil {
		t.Fatalf("New with nil advertiser: unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start with nil advertiser: unexpected error: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})
}

// TestNew_NilLoggerOK verifies that New succeeds when logger is nil.
func TestNew_NilLoggerOK(t *testing.T) {
	t.Parallel()
	_, err := bridge.New(bridge.NewFakeStore(), emptySnapshotter, mdns.NewNoop(), bridge.Config{
		VendorID:  1,
		ProductID: 2,
		NodeLabel: "x",
	}, nil)
	if err != nil {
		t.Fatalf("New with nil logger: unexpected error: %v", err)
	}
}

// ─── lifecycle tests ──────────────────────────────────────────────────

// TestStart_AssemblesTopology verifies Start returns a non-nil topology with at least the root endpoint.
func TestStart_AssemblesTopology(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})

	topo := b.Topology()
	if topo == nil {
		t.Fatal("Topology() returned nil after Start")
	}
	if len(topo.Endpoints) == 0 {
		t.Fatal("Topology has no endpoints; expected at least root (ID=0)")
	}
	root := topo.FindByID(0)
	if root == nil {
		t.Fatal("root endpoint (ID=0) not found in topology")
	}

	if d := b.Dispatcher(); d == nil {
		t.Fatal("Dispatcher() returned nil after Start")
	}
}

// TestStart_BindsEphemeralPort verifies LocalAddr returns a non-empty address when Listen=":0".
func TestStart_BindsEphemeralPort(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t) // uses Listen=":0"

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})

	addr := b.LocalAddr()
	if addr == "" {
		t.Fatal("LocalAddr() returned empty string after Start with ':0'")
	}
}

// TestStart_TwiceReturnsAlreadyStarted verifies a second Start returns ErrAlreadyStarted.
func TestStart_TwiceReturnsAlreadyStarted(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("first Start: unexpected error: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})

	err := b.Start(ctx)
	if !errors.Is(err, bridge.ErrAlreadyStarted) {
		t.Errorf("second Start: want ErrAlreadyStarted, got %v", err)
	}
}

// TestStart_DefersOperationalRecord verifies that Start does NOT
// publish an operational `_matter._tcp` record before any fabric is
// installed. Apple's MatterSupport reads operational records to detect
// already-paired bridges; emitting a `0000…0000` placeholder makes it
// silently abort the new pairing handshake. The post-AddNOC
// AnnounceFabric path publishes the record once a real
// CompressedFabricID + NodeID exists.
func TestStart_DefersOperationalRecord(t *testing.T) {
	t.Parallel()
	noop := mdns.NewNoop()
	b, err := bridge.New(bridge.NewFakeStore(), emptySnapshotter, noop, bridge.Config{
		Listen:    ":0",
		VendorID:  0x1234,
		ProductID: 0x5678,
		NodeLabel: "test-mdns",
	}, nil)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})

	active := noop.Active()
	for _, svc := range active {
		if svc.ServiceType == mdns.ServiceTypeOperational {
			t.Errorf("operational record published pre-fabric: %+v", svc)
		}
	}
}

// TestReassemble_BeforeStartReturnsErrNotStarted verifies Reassemble before Start returns ErrNotStarted.
func TestReassemble_BeforeStartReturnsErrNotStarted(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := b.Reassemble(ctx)
	if !errors.Is(err, bridge.ErrNotStarted) {
		t.Errorf("Reassemble before Start: want ErrNotStarted, got %v", err)
	}
}

// TestReassemble_InvokesSnapshotterAgain verifies the snapshotter call count increments and topology identity differs.
func TestReassemble_InvokesSnapshotterAgain(t *testing.T) {
	t.Parallel()
	cs := &countingSnapshotter{}
	b, err := bridge.New(bridge.NewFakeStore(), cs.snap, mdns.NewNoop(), bridge.Config{
		Listen:    ":0",
		VendorID:  0x1234,
		ProductID: 0x5678,
		NodeLabel: "test-reassemble",
	}, nil)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})

	countAfterStart := cs.count.Load()
	if countAfterStart < 1 {
		t.Errorf("snapshotter not called during Start; count=%d", countAfterStart)
	}

	topoBeforeReassemble := b.Topology()

	if err := b.Reassemble(ctx); err != nil {
		t.Fatalf("Reassemble: unexpected error: %v", err)
	}

	countAfterReassemble := cs.count.Load()
	if countAfterReassemble <= countAfterStart {
		t.Errorf("snapshotter not called during Reassemble; count before=%d after=%d",
			countAfterStart, countAfterReassemble)
	}

	topoAfterReassemble := b.Topology()
	if topoBeforeReassemble == topoAfterReassemble {
		t.Error("Topology() pointer is identical before and after Reassemble; expected a new allocation")
	}
}

// TestStop_IsIdempotent verifies that Stop may be called twice without error and services are withdrawn.
func TestStop_IsIdempotent(t *testing.T) {
	t.Parallel()
	noop := mdns.NewNoop()
	b, err := bridge.New(bridge.NewFakeStore(), emptySnapshotter, noop, bridge.Config{
		Listen:    ":0",
		VendorID:  0x1234,
		ProductID: 0x5678,
		NodeLabel: "test-stop-idempotent",
	}, nil)
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: unexpected error: %v", err)
	}

	stop1Ctx, stop1Cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop1Cancel()
	if err := b.Stop(stop1Ctx); err != nil {
		t.Errorf("first Stop: unexpected error: %v", err)
	}

	stop2Ctx, stop2Cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer stop2Cancel()
	if err := b.Stop(stop2Ctx); err != nil {
		t.Errorf("second Stop: unexpected error: %v", err)
	}

	// All advertised services should be withdrawn after Stop.
	if active := noop.Active(); len(active) != 0 {
		t.Errorf("noop.Active() should be empty after Stop; got %v", active)
	}
}
