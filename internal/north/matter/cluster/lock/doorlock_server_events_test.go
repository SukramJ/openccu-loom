// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package lock_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/lock"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// eventTestSource is a StateSource whose LockInvoke outcome is
// configurable, so tests can exercise both the success path (event
// emitted) and the failure path (no event, no DataVersion bump).
type eventTestSource struct {
	locked    bool
	observed  bool
	invokeErr error
	invoked   []uint32
}

func (s *eventTestSource) IsJammed() bool                    { return false }
func (s *eventTestSource) IsLocked() (locked, observed bool) { return s.locked, s.observed }

func (s *eventTestSource) LockInvoke(_ context.Context, cmdID uint32, _ hmenum.CommandPriority) error {
	s.invoked = append(s.invoked, cmdID)
	return s.invokeErr
}

// emitCall captures a single MatterEmitEvent invocation.
type emitCall struct {
	endpoint uint16
	cluster  uint32
	event    uint32
	data     any
	priority interfaces.MatterEventPriority
}

// fakeEventEmitter implements [interfaces.MatterEventEmitter] and
// records every call for assertion.
type fakeEventEmitter struct {
	calls []emitCall
}

func (f *fakeEventEmitter) MatterEmitEvent(endpoint uint16, clusterID, eventID uint32, data any, priority interfaces.MatterEventPriority) {
	f.calls = append(f.calls, emitCall{endpoint: endpoint, cluster: clusterID, event: eventID, data: data, priority: priority})
}

var errInvokeFailed = errors.New("doorlock: invoke failed")

// newWiredServer returns a DoorLockServer with an emitter and endpoint
// already wired, plus the source and emitter for assertions.
func newWiredServer(t *testing.T, src *eventTestSource, endpoint uint16) (*lock.DoorLockServer, *fakeEventEmitter) {
	t.Helper()
	srv := lock.NewDoorLockServer(lock.DoorLockConfig{Source: src})
	emitter := &fakeEventEmitter{}
	srv.SetMatterEventEmitter(emitter)
	srv.SetEndpoint(endpoint)
	return srv, emitter
}

// TestDoorLockServer_EmitLockOperation_LockDoor verifies that a
// successful LockDoor invoke fires exactly one LockOperation event
// (0x02) with LockOperationType=Lock(0), OperationSource=Remote(7),
// and FabricIndex/SourceNode lifted from the invoking context. Mirrors
// matter.js DoorLockServer.ts:119-127 (#lockDoor ends in
// #emitLockOperation) and :911-939 (payload assembly).
func TestDoorLockServer_EmitLockOperation_LockDoor(t *testing.T) {
	t.Parallel()
	src := &eventTestSource{observed: true}
	srv, emitter := newWiredServer(t, src, 7)

	ctx := im.WithFabricFilter(context.Background(), false, 2)
	ctx = im.WithSubject(ctx, 0xAABB, nil)

	if _, err := srv.MatterInvoke(ctx, wire.DoorLockCmdLockDoor, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke(LockDoor): %v", err)
	}

	if len(emitter.calls) != 1 {
		t.Fatalf("emitter.calls = %d, want exactly 1", len(emitter.calls))
	}
	c := emitter.calls[0]
	if c.endpoint != 7 {
		t.Errorf("endpoint = %d, want 7", c.endpoint)
	}
	if c.cluster != wire.DoorLockClusterID {
		t.Errorf("cluster = 0x%04X, want 0x%04X", c.cluster, wire.DoorLockClusterID)
	}
	if c.event != wire.DoorLockEventLockOperation {
		t.Errorf("event = 0x%02X, want 0x%02X", c.event, wire.DoorLockEventLockOperation)
	}
	if c.priority != interfaces.MatterEventPriorityCritical {
		t.Errorf("priority = %v, want MatterEventPriorityCritical", c.priority)
	}

	payload, ok := c.data.(lock.LockOperationEvent)
	if !ok {
		t.Fatalf("data type = %T, want lock.LockOperationEvent", c.data)
	}
	if payload.LockOperationType != 0 {
		t.Errorf("LockOperationType = %d, want 0 (Lock)", payload.LockOperationType)
	}
	if payload.OperationSource != 7 {
		t.Errorf("OperationSource = %d, want 7 (Remote)", payload.OperationSource)
	}
	if payload.UserIndex != nil {
		t.Errorf("UserIndex = %v, want nil", *payload.UserIndex)
	}
	if payload.FabricIndex == nil {
		t.Error("FabricIndex = nil, want non-nil (2)")
	} else if *payload.FabricIndex != 2 {
		t.Errorf("FabricIndex = %d, want 2", *payload.FabricIndex)
	}
	if payload.SourceNode == nil {
		t.Error("SourceNode = nil, want non-nil (0xAABB)")
	} else if *payload.SourceNode != 0xAABB {
		t.Errorf("SourceNode = 0x%X, want 0xAABB", *payload.SourceNode)
	}
}

// TestDoorLockServer_EmitLockOperation_OperationTypeMapping verifies
// the LockOperationType wire value per command: LockDoor→Lock(0),
// UnlockDoor→Unlock(1), UnboltDoor→Unlatch(4). UnboltDoor reporting
// Unlatch rather than Unlock mirrors matter.js
// DoorLockServer.ts:140-142.
func TestDoorLockServer_EmitLockOperation_OperationTypeMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		cmdID  uint32
		wantOp uint8
	}{
		{"UnlockDoor", wire.DoorLockCmdUnlockDoor, 1},
		{"UnboltDoor", wire.DoorLockCmdUnboltDoor, 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := &eventTestSource{observed: true}
			srv, emitter := newWiredServer(t, src, 1)

			if _, err := srv.MatterInvoke(context.Background(), tc.cmdID, nil, hmenum.CommandPriorityHigh); err != nil {
				t.Fatalf("MatterInvoke(0x%02X): %v", tc.cmdID, err)
			}
			if len(emitter.calls) != 1 {
				t.Fatalf("emitter.calls = %d, want exactly 1", len(emitter.calls))
			}
			payload, ok := emitter.calls[0].data.(lock.LockOperationEvent)
			if !ok {
				t.Fatalf("data type = %T, want lock.LockOperationEvent", emitter.calls[0].data)
			}
			if payload.LockOperationType != tc.wantOp {
				t.Errorf("LockOperationType = %d, want %d", payload.LockOperationType, tc.wantOp)
			}
		})
	}
}

// TestDoorLockServer_EmitLockOperation_UnstampedContext verifies that
// a plain context.Background() (the PASE case, no fabric/subject
// stamped) yields FabricIndex=nil and SourceNode=nil — matching
// matter.js's null fallback for pre-fabric sessions
// (DoorLockServer.ts:911-939).
func TestDoorLockServer_EmitLockOperation_UnstampedContext(t *testing.T) {
	t.Parallel()
	src := &eventTestSource{observed: true}
	srv, emitter := newWiredServer(t, src, 3)

	if _, err := srv.MatterInvoke(context.Background(), wire.DoorLockCmdLockDoor, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke: %v", err)
	}
	if len(emitter.calls) != 1 {
		t.Fatalf("emitter.calls = %d, want exactly 1", len(emitter.calls))
	}
	payload, ok := emitter.calls[0].data.(lock.LockOperationEvent)
	if !ok {
		t.Fatalf("data type = %T, want lock.LockOperationEvent", emitter.calls[0].data)
	}
	if payload.FabricIndex != nil {
		t.Errorf("FabricIndex = %d, want nil", *payload.FabricIndex)
	}
	if payload.SourceNode != nil {
		t.Errorf("SourceNode = 0x%X, want nil", *payload.SourceNode)
	}
}

// TestDoorLockServer_EmitLockOperation_InvokeErrorSuppressesEvent
// verifies that a failing LockInvoke neither emits an event nor bumps
// the DataVersion tracker.
func TestDoorLockServer_EmitLockOperation_InvokeErrorSuppressesEvent(t *testing.T) {
	t.Parallel()
	src := &eventTestSource{observed: true, invokeErr: errInvokeFailed}
	srv, emitter := newWiredServer(t, src, 4)
	dvBefore := srv.MatterDataVersion()

	_, err := srv.MatterInvoke(context.Background(), wire.DoorLockCmdLockDoor, nil, hmenum.CommandPriorityHigh)
	if !errors.Is(err, errInvokeFailed) {
		t.Fatalf("MatterInvoke error = %v, want errInvokeFailed", err)
	}
	if len(emitter.calls) != 0 {
		t.Errorf("emitter.calls = %d, want 0 on failed invoke", len(emitter.calls))
	}
	if srv.MatterDataVersion() != dvBefore {
		t.Error("DataVersion bumped despite failed invoke")
	}
}

// TestDoorLockServer_EmitLockOperation_NoEmitterWired verifies that
// MatterInvoke succeeds without panicking when no emitter has been
// wired via [lock.DoorLockServer.SetMatterEventEmitter] — the
// no-op default before the bridge assembles the topology.
func TestDoorLockServer_EmitLockOperation_NoEmitterWired(t *testing.T) {
	t.Parallel()
	src := &eventTestSource{observed: true}
	srv := lock.NewDoorLockServer(lock.DoorLockConfig{Source: src})

	if _, err := srv.MatterInvoke(context.Background(), wire.DoorLockCmdLockDoor, nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterInvoke without emitter: %v", err)
	}
}

// TestDoorLockServer_MatterEvents_AdvertisesThreeEvents verifies that
// MatterEvents() returns exactly the three conformance-M DoorLock
// event IDs (DoorLockAlarm 0x00, LockOperation 0x02,
// LockOperationError 0x03).
func TestDoorLockServer_MatterEvents_AdvertisesThreeEvents(t *testing.T) {
	t.Parallel()
	srv := lock.NewDoorLockServer(lock.DoorLockConfig{Source: &eventTestSource{observed: true}})

	got := srv.MatterEvents()
	want := map[uint32]bool{
		wire.DoorLockEventDoorLockAlarm:      true,
		wire.DoorLockEventLockOperation:      true,
		wire.DoorLockEventLockOperationError: true,
	}
	if len(got) != len(want) {
		t.Fatalf("MatterEvents() = %v (len %d), want %d distinct ids", got, len(got), len(want))
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("MatterEvents() contains unexpected id 0x%02X", id)
		}
		delete(want, id)
	}
	if len(want) != 0 {
		t.Errorf("MatterEvents() missing ids: %v", want)
	}
}
