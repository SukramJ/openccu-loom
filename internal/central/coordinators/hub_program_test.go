// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// HubCoordinator tests covering Program.Execute behaviour without a
// writer, GetHubDataPoints aggregation, and the IsRegistered/Marked
// semantics that callers use to filter hub DPs after fetching the
// full list (Go does not accept a `registered` parameter inline —
// the flag lives on the embedded BaseDataPointFields).

package coordinators

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// newHubCoordinatorWithModel builds a HubCoordinator wired with a hub
// model for tests.
func newHubCoordinatorWithModel(t *testing.T) (*HubCoordinator, *hub.Hub) {
	t.Helper()
	bus := events.NewBus()
	hc := NewHubCoordinator("test-central", bus)
	m := hub.NewHub("test-central")
	hc.SetHubModel(m)
	return hc, m
}

// ── ported tests ──────────────────────────────────────────────────────────────

// TestHubExecuteProgramNoWriterReturnsError mirrors test_execute_program_no_client:
// when no primary client (writer) is configured, executing a program must return an
// error rather than silently succeeding.
//
// Python test: TestHubCoordinatorProgramOperations.test_execute_program_no_client
// (test_central_hub_coordinator.py:309).
//
// Shape note: Python returns False (bool); Go returns a non-nil error. Both signal
// "could not execute — no downstream connection". The Go sentinel error is
// "program … no writer configured" from program.go:178.
func TestHubExecuteProgramNoWriterReturnsError(t *testing.T) {
	t.Parallel()

	// NewProgram with writer=nil simulates the "no primary client" scenario.
	p := hub.NewProgram("test-central", "prog-1", "Test Program", "", false, nil)

	err := p.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute with nil writer must return an error; got nil")
	}
}

// TestHubExecuteProgramWithWriterSucceeds is the positive complement:
// a wired writer is called and its result is returned.
//
// Python test: TestHubCoordinatorProgramOperations.test_execute_program_success
// (test_central_hub_coordinator.py:332).
func TestHubExecuteProgramWithWriterSucceeds(t *testing.T) {
	t.Parallel()

	var called bool
	writer := &stubProgramWriter{executeFn: func(_ context.Context, _ string) error {
		called = true
		return nil
	}}
	p := hub.NewProgram("test-central", "prog-1", "Test Program", "", false, writer)

	if err := p.Execute(context.Background()); err != nil {
		t.Fatalf("Execute with working writer must return nil; got %v", err)
	}
	if !called {
		t.Fatal("writer.ExecuteProgram was not called")
	}
}

// TestHubGetHubDataPointsReturnsAllDPs mirrors test_get_hub_data_points_no_filter:
// GetHubDataPoints() with no arguments must return all registered programs (as
// []any entries) plus all sysvars.
//
// Python test: TestHubCoordinatorGetHubDataPoints.test_get_hub_data_points_no_filter
// (test_central_hub_coordinator.py:854).
func TestHubGetHubDataPointsReturnsAllDPs(t *testing.T) {
	t.Parallel()

	hc, _ := newHubCoordinatorWithModel(t)

	p := hub.NewProgram("test-central", "prog-1", "Program 1", "", false, nil)
	hc.AddProgramDP(p)

	s := hub.NewSysvar("test-central", "sysvar-1", "", hmenum.HubValueTypeFloat, nil)
	hc.AddSysvarDP(s)

	all := hc.GetHubDataPoints()
	// Expect 1 program + 1 sysvar = 2 entries.
	if len(all) != 2 {
		t.Fatalf("GetHubDataPoints: want 2 entries, got %d", len(all))
	}
}

// TestHubGetHubDataPointsFilterByRegisteredManual mirrors the spirit of
// test_get_hub_data_points_filter_by_registered: programs and sysvars expose
// IsRegistered() (via embedded BaseDataPointFields), so callers CAN filter by
// registration status.
//
// Python test: TestHubCoordinatorGetHubDataPoints.test_get_hub_data_points_filter_by_registered
// (test_central_hub_coordinator.py:820).
//
// Shape note: Python's get_hub_data_points(registered=True) is a built-in parameter;
// Go callers must filter the []any slice themselves using type assertions + IsRegistered().
// This test verifies the building blocks are correct (MarkRegistered / IsRegistered on
// hub.Program and hub.Sysvar), not that GetHubDataPoints accepts a registered parameter.
func TestHubGetHubDataPointsFilterByRegisteredManual(t *testing.T) {
	t.Parallel()

	hc, _ := newHubCoordinatorWithModel(t)

	// Two programs: p1 registered, p2 unregistered.
	p1 := hub.NewProgram("test-central", "prog-1", "Registered Program", "", false, nil)
	p1.MarkRegistered()
	hc.AddProgramDP(p1)

	p2 := hub.NewProgram("test-central", "prog-2", "Unregistered Program", "", false, nil)
	// p2 is not registered — default is false.
	hc.AddProgramDP(p2)

	// One sysvar registered.
	s := hub.NewSysvar("test-central", "sysvar-1", "", hmenum.HubValueTypeFloat, nil)
	s.MarkRegistered()
	hc.AddSysvarDP(s)

	all := hc.GetHubDataPoints()
	if len(all) != 3 {
		t.Fatalf("GetHubDataPoints: want 3 entries total, got %d", len(all))
	}

	// Manual filter: collect registered items.
	type registeredChecker interface {
		IsRegistered() bool
	}
	var registered []any
	for _, dp := range all {
		if rc, ok := dp.(registeredChecker); ok && rc.IsRegistered() {
			registered = append(registered, dp)
		}
	}
	// Expect p1 + sysvar-1 = 2 registered entries.
	if len(registered) != 2 {
		t.Fatalf("manual filter(registered=true): want 2, got %d", len(registered))
	}

	// Verify p2 is NOT in the registered set.
	for _, dp := range registered {
		if prog, ok := dp.(*hub.Program); ok {
			if prog.ID == p2.ID {
				t.Fatal("unregistered program p2 must not appear in registered filter result")
			}
		}
	}
}

// TestHubGetHubDataPointsEmptyWhenNoModel mirrors a structural invariant:
// GetHubDataPoints returns an empty slice (not nil-panic) when no hub model
// is wired.
//
// Python analog: coordinator initialized without hub — the property returns
// empty sequences.
func TestHubGetHubDataPointsEmptyWhenNoModel(t *testing.T) {
	t.Parallel()

	bus := events.NewBus()
	hc := NewHubCoordinator("test-central", bus)
	// No hub model wired.

	all := hc.GetHubDataPoints()
	if len(all) != 0 {
		t.Fatalf("GetHubDataPoints with no hub model: want 0 entries, got %d", len(all))
	}
}

// ── stubs ─────────────────────────────────────────────────────────────────────

// stubProgramWriter is a minimal ProgramWriter for these tests.
type stubProgramWriter struct {
	executeFn    func(ctx context.Context, pid string) error
	setEnabledFn func(ctx context.Context, pid string, enabled bool) error
}

func (s *stubProgramWriter) ExecuteProgram(ctx context.Context, pid string) error {
	if s.executeFn != nil {
		return s.executeFn(ctx, pid)
	}
	return errors.New("stubProgramWriter: ExecuteProgram not configured")
}

func (s *stubProgramWriter) SetProgramEnabled(ctx context.Context, pid string, enabled bool) error {
	if s.setEnabledFn != nil {
		return s.setEnabledFn(ctx, pid, enabled)
	}
	return nil
}
