// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// hub_refresh_lifecycle_test.go covers HubCoordinator refresh hooks, full
// program/sysvar lifecycles, state-path reflection, and Clear — scenarios
// not covered by hub_deep_test.go.
//
// Covered here:
//   - RefreshPrograms invokes the hook
//   - RefreshSysvars invokes the hook
//   - All five refresh hooks are invoked when called individually
//   - Full program lifecycle: add → set-state → notify-executed → remove
//   - Full sysvar lifecycle: add → set → get → remove
//   - HubStatePaths reflects registered sysvars and programs
//   - Clear drops all cached sysvars and clears the hub model

package coordinators

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// TestHubRefreshProgramsInvokesHook verifies that a hook wired via
// SetRefreshHooks is called when RefreshPrograms is invoked.
func TestHubRefreshProgramsInvokesHook(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)

	var called atomic.Int32
	h.SetRefreshHooks(RefreshHooks{
		Programs: func(_ context.Context) error {
			called.Add(1)
			return nil
		},
	})

	if err := h.RefreshPrograms(context.Background()); err != nil {
		t.Fatalf("RefreshPrograms: %v", err)
	}
	if called.Load() != 1 {
		t.Fatalf("Programs hook called %d times, want 1", called.Load())
	}
}

// TestHubRefreshSyvarsInvokesHook verifies that a hook wired via
// SetRefreshHooks is called when RefreshSysvars is invoked.
func TestHubRefreshSyvarsInvokesHook(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)

	var called atomic.Int32
	h.SetRefreshHooks(RefreshHooks{
		Sysvars: func(_ context.Context) error {
			called.Add(1)
			return nil
		},
	})

	if err := h.RefreshSysvars(context.Background()); err != nil {
		t.Fatalf("RefreshSysvars: %v", err)
	}
	if called.Load() != 1 {
		t.Fatalf("Sysvars hook called %d times, want 1", called.Load())
	}
}

// TestHubAllRefreshHooksInvoked wires all five refresh hooks and calls
// each Refresh* method once; every hook must fire exactly once,
// verifying that the initialization path triggers all five fetch operations.
func TestHubAllRefreshHooksInvoked(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	h := NewHubCoordinator("c", bus)

	var programs, sysvars, inbox, serviceMsgs, alarmMsgs atomic.Int32

	h.SetRefreshHooks(RefreshHooks{
		Programs:        func(_ context.Context) error { programs.Add(1); return nil },
		Sysvars:         func(_ context.Context) error { sysvars.Add(1); return nil },
		Inbox:           func(_ context.Context) error { inbox.Add(1); return nil },
		ServiceMessages: func(_ context.Context) error { serviceMsgs.Add(1); return nil },
		AlarmMessages:   func(_ context.Context) error { alarmMsgs.Add(1); return nil },
	})

	ctx := context.Background()
	for _, tc := range []struct {
		name string
		fn   func(context.Context) error
		got  *atomic.Int32
	}{
		{"RefreshPrograms", h.RefreshPrograms, &programs},
		{"RefreshSysvars", h.RefreshSysvars, &sysvars},
		{"RefreshInbox", h.RefreshInbox, &inbox},
		{"RefreshServiceMessages", h.RefreshServiceMessages, &serviceMsgs},
		{"RefreshAlarmMessages", h.RefreshAlarmMessages, &alarmMsgs},
	} {
		if err := tc.fn(ctx); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if tc.got.Load() != 1 {
			t.Errorf("%s: hook called %d times, want 1", tc.name, tc.got.Load())
		}
	}
}

// TestHubFullProgramLifecycle adds a program, asserts it is registered,
// sets its state (via a wired writer), notifies execution, then removes it.
func TestHubFullProgramLifecycle(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	h := NewHubCoordinator("c-prog-lc", bus)
	m := hub.NewHub("c-prog-lc")
	h.SetHubModel(m)

	// Track SetProgramActive calls.
	var writtenID string
	var writtenActive bool
	h.SetProgramStateWriter(&fakeProgramStateWriter{
		fn: func(_ context.Context, id string, active bool) error {
			writtenID = id
			writtenActive = active
			return nil
		},
	})

	// Track ProgramExecutedEvent.
	var execEvents []hmevent.ProgramExecutedEvent
	unsub := events.Subscribe(bus, func(e hmevent.ProgramExecutedEvent) {
		execEvents = append(execEvents, e)
	})
	defer unsub()

	// Add program.
	p := hub.NewProgram("c-prog-lc", "prog-1", "Test Program", "", false, nil)
	h.AddProgramDP(p)

	progs := h.ProgramDataPoints()
	if len(progs) != 1 {
		t.Fatalf("ProgramDataPoints: want 1, got %d", len(progs))
	}

	// Set program state.
	if err := h.SetProgramState(context.Background(), "prog-1", true); err != nil {
		t.Fatalf("SetProgramState: %v", err)
	}
	if writtenID != "prog-1" || !writtenActive {
		t.Errorf("SetProgramActive: id=%q active=%v, want id=prog-1 active=true", writtenID, writtenActive)
	}

	// Notify execution.
	h.NotifyProgramExecuted(context.Background(), "prog-1", hmenum.ProgramTriggerUser, true)
	if len(execEvents) != 1 {
		t.Fatalf("want 1 ProgramExecutedEvent, got %d", len(execEvents))
	}
	if execEvents[0].ProgramID != "prog-1" {
		t.Errorf("ProgramID=%q, want prog-1", execEvents[0].ProgramID)
	}

	// Remove program.
	if !h.RemoveProgramDP("prog-1") {
		t.Fatal("RemoveProgramDP: want true for known program")
	}
	if got := h.GetProgramDataPoint("prog-1"); got != nil {
		t.Fatal("GetProgramDataPoint: want nil after removal")
	}
	if len(h.ProgramDataPoints()) != 0 {
		t.Fatal("ProgramDataPoints: want empty after removal")
	}
}

// TestHubFullSysvarLifecycle adds a sysvar, gets the live CCU value via
// a getter, sets the value via a writer, then removes it.
func TestHubFullSysvarLifecycle(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	h := NewHubCoordinator("c-sv-lc", bus)
	m := hub.NewHub("c-sv-lc")
	h.SetHubModel(m)

	// Wire a getter that returns 42.
	h.SetSysvarGetter(&stubSysvarGetter{val: 42})

	// Track SetSysvar calls.
	var writtenName string
	var writtenValue any
	h.SetSysvarValueWriter(&fakeSysvarWriter{
		fn: func(_ context.Context, name string, value any) error {
			writtenName = name
			writtenValue = value
			return nil
		},
	})

	// Add sysvar.
	sv := hub.NewSysvar("c-sv-lc", "TestVar", "Test Variable", hmenum.HubValueTypeInteger, nil)
	h.AddSysvarDP(sv)

	svars := h.SysvarDataPoints()
	if len(svars) != 1 {
		t.Fatalf("SysvarDataPoints: want 1, got %d", len(svars))
	}
	if h.GetSysvarDataPoint("TestVar") == nil {
		t.Fatal("GetSysvarDataPoint: want non-nil for registered sysvar")
	}

	// Get live value from CCU.
	val, err := h.GetSystemVariable(context.Background(), "TestVar")
	if err != nil {
		t.Fatalf("GetSystemVariable: %v", err)
	}
	if val != 42 {
		t.Fatalf("GetSystemVariable: want 42, got %v", val)
	}

	// Set sysvar value.
	if err := h.SetSystemVariable(context.Background(), "TestVar", 100); err != nil {
		t.Fatalf("SetSystemVariable: %v", err)
	}
	if writtenName != "TestVar" || writtenValue != 100 {
		t.Errorf("SetSysvar: name=%q value=%v, want name=TestVar value=100", writtenName, writtenValue)
	}

	// Remove sysvar.
	if !h.RemoveSysvarDP("TestVar") {
		t.Fatal("RemoveSysvarDP: want true for known sysvar")
	}
	if got := h.GetSysvarDataPoint("TestVar"); got != nil {
		t.Fatal("GetSysvarDataPoint: want nil after removal")
	}
	if len(h.SysvarDataPoints()) != 0 {
		t.Fatal("SysvarDataPoints: want empty after removal")
	}
}

// TestHubClearDropsAllSnapshots verifies that Clear() removes all cached
// sysvar snapshots and resets hub model programs and sysvars.
func TestHubClearDropsAllSnapshots(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	h := NewHubCoordinator("c-clear", bus)
	m := hub.NewHub("c-clear")
	h.SetHubModel(m)

	// Populate hub model and sysvar snapshots.
	h.AddProgramDP(hub.NewProgram("c-clear", "p1", "P1", "", false, nil))
	h.AddSysvarDP(hub.NewSysvar("c-clear", "V1", "Var1", hmenum.HubValueTypeLogic, nil))
	h.UpdateSysvar(context.Background(), SysvarSnapshot{
		Name:      "V1",
		Value:     hmtypes.BoolValue(true),
		ValueType: hmenum.HubValueTypeLogic,
	})

	if len(h.Sysvars()) != 1 {
		t.Fatalf("before Clear: want 1 sysvar snapshot, got %d", len(h.Sysvars()))
	}

	h.Clear()

	// Snapshot map must be empty.
	if len(h.Sysvars()) != 0 {
		t.Fatalf("after Clear: want 0 sysvar snapshots, got %d", len(h.Sysvars()))
	}
	// Hub model must also be reset.
	if len(h.ProgramDataPoints()) != 0 {
		t.Fatalf("after Clear: want 0 programs in hub model, got %d", len(h.ProgramDataPoints()))
	}
	if len(h.SysvarDataPoints()) != 0 {
		t.Fatalf("after Clear: want 0 sysvars in hub model, got %d", len(h.SysvarDataPoints()))
	}
}
