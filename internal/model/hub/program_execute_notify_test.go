// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

type stubProgramWriter struct {
	returnErr error
}

func (s *stubProgramWriter) ExecuteProgram(_ context.Context, _ string) error { return s.returnErr }
func (s *stubProgramWriter) SetProgramEnabled(_ context.Context, _ string, _ bool) error {
	return nil
}

// TestProgramExecute_NotifierFiredOnSuccess verifies that Execute calls the
// ExecuteNotifier with success=true after a clean writer round-trip.
func TestProgramExecute_NotifierFiredOnSuccess(t *testing.T) {
	t.Parallel()
	p := NewProgram("main", "42", "TestProg", "", false, &stubProgramWriter{})

	var notifiedID string
	var notifiedTrigger hmenum.ProgramTrigger
	var notifiedSuccess bool
	var calls int
	p.ExecuteNotifier = func(_ context.Context, id string, tr hmenum.ProgramTrigger, ok bool) {
		calls++
		notifiedID = id
		notifiedTrigger = tr
		notifiedSuccess = ok
	}

	if err := p.Execute(t.Context()); err != nil {
		t.Fatalf("Execute: unexpected error: %v", err)
	}
	if calls != 1 {
		t.Fatalf("notifier called %d times, want 1", calls)
	}
	if notifiedID != "42" {
		t.Errorf("notifiedID = %q, want %q", notifiedID, "42")
	}
	if notifiedTrigger != hmenum.ProgramTriggerAPI {
		t.Errorf("notifiedTrigger = %v, want API", notifiedTrigger)
	}
	if !notifiedSuccess {
		t.Error("notifiedSuccess = false, want true")
	}
}

// TestProgramExecute_NotifierFiredOnError verifies that Execute calls the
// ExecuteNotifier with success=false when the writer returns an error, and
// that the original error is still propagated to the caller.
func TestProgramExecute_NotifierFiredOnError(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("rpc timeout")
	p := NewProgram("main", "99", "ErrorProg", "", false, &stubProgramWriter{returnErr: wantErr})

	notifiedSuccess := true // initialise to wrong value
	p.ExecuteNotifier = func(_ context.Context, _ string, _ hmenum.ProgramTrigger, ok bool) {
		notifiedSuccess = ok
	}

	err := p.Execute(t.Context())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Execute error = %v, want %v", err, wantErr)
	}
	if notifiedSuccess {
		t.Error("notifiedSuccess = true on error path, want false")
	}
}

// TestProgramExecute_NoNotifierNoPanic verifies that Execute works without
// a notifier (nil ExecuteNotifier) and does not panic.
func TestProgramExecute_NoNotifierNoPanic(t *testing.T) {
	t.Parallel()
	p := NewProgram("main", "7", "NilNotifier", "", false, &stubProgramWriter{})
	if err := p.Execute(t.Context()); err != nil {
		t.Fatalf("Execute with nil notifier: %v", err)
	}
}

// stubConditionalProgramWriter implements ConditionalProgramWriter so the
// condition-checked execution path can be exercised.
type stubConditionalProgramWriter struct {
	stubProgramWriter
	executed  bool
	condErr   error
	condID    string
	condCalls int
}

func (s *stubConditionalProgramWriter) ExecuteProgramConditional(_ context.Context, id string) (bool, error) {
	s.condCalls++
	s.condID = id
	if s.condErr != nil {
		return false, s.condErr
	}
	return s.executed, nil
}

// TestProgramExecuteWithConditionCheck_ConditionMet verifies the conditional
// path routes through the writer, reports executed=true, and fires the
// notifier with success=true when the condition is satisfied.
func TestProgramExecuteWithConditionCheck_ConditionMet(t *testing.T) {
	t.Parallel()
	w := &stubConditionalProgramWriter{executed: true}
	p := NewProgram("main", "42", "Cond", "", false, w)

	var calls int
	var notifiedSuccess bool
	p.ExecuteNotifier = func(_ context.Context, _ string, _ hmenum.ProgramTrigger, ok bool) {
		calls++
		notifiedSuccess = ok
	}

	executed, err := p.ExecuteWithConditionCheck(t.Context())
	if err != nil {
		t.Fatalf("ExecuteWithConditionCheck: %v", err)
	}
	if !executed {
		t.Fatal("executed=false, want true (condition met)")
	}
	if w.condCalls != 1 || w.condID != "42" {
		t.Fatalf("conditional writer calls=%d id=%q, want 1/42", w.condCalls, w.condID)
	}
	if calls != 1 || !notifiedSuccess {
		t.Fatalf("notifier calls=%d success=%v, want 1/true", calls, notifiedSuccess)
	}
}

// TestProgramExecuteWithConditionCheck_ConditionNotMet verifies that when the
// condition is not satisfied the program reports executed=false and does NOT
// fire the notifier (nothing ran).
func TestProgramExecuteWithConditionCheck_ConditionNotMet(t *testing.T) {
	t.Parallel()
	w := &stubConditionalProgramWriter{executed: false}
	p := NewProgram("main", "42", "Cond", "", false, w)

	var calls int
	p.ExecuteNotifier = func(_ context.Context, _ string, _ hmenum.ProgramTrigger, _ bool) {
		calls++
	}

	executed, err := p.ExecuteWithConditionCheck(t.Context())
	if err != nil {
		t.Fatalf("ExecuteWithConditionCheck: %v", err)
	}
	if executed {
		t.Fatal("executed=true, want false (condition not met)")
	}
	if calls != 0 {
		t.Fatalf("notifier called %d times, want 0 (nothing executed)", calls)
	}
}

// TestProgramExecuteWithConditionCheck_FallbackWhenNotConditional verifies
// that a plain ProgramWriter (no ConditionalProgramWriter) falls back to the
// unconditional Execute path and reports executed=true.
func TestProgramExecuteWithConditionCheck_FallbackWhenNotConditional(t *testing.T) {
	t.Parallel()
	p := NewProgram("main", "7", "Plain", "", false, &stubProgramWriter{})

	executed, err := p.ExecuteWithConditionCheck(t.Context())
	if err != nil {
		t.Fatalf("ExecuteWithConditionCheck: %v", err)
	}
	if !executed {
		t.Fatal("executed=false, want true (unconditional fallback)")
	}
}

// TestProgramExecuteWithConditionCheck_FallbackNotifierFiresOnce verifies
// that the unconditional-fallback path (Execute called from inside
// ExecuteWithConditionCheck) fires the notifier exactly once — Execute
// already notifies internally, so ExecuteWithConditionCheck must not notify
// a second time for the same round-trip.
func TestProgramExecuteWithConditionCheck_FallbackNotifierFiresOnce(t *testing.T) {
	t.Parallel()
	p := NewProgram("main", "7", "Plain", "", false, &stubProgramWriter{})

	var calls int
	p.ExecuteNotifier = func(_ context.Context, _ string, _ hmenum.ProgramTrigger, _ bool) {
		calls++
	}

	if _, err := p.ExecuteWithConditionCheck(t.Context()); err != nil {
		t.Fatalf("ExecuteWithConditionCheck: %v", err)
	}
	if calls != 1 {
		t.Fatalf("notifier called %d times, want exactly 1 (no double-notify)", calls)
	}
}

// TestProgramExecuteWithConditionCheck_NoWriterConfigured verifies that a
// Program with no writer at all reports executed=false and a descriptive
// error, mirroring the guard in [Program.Execute].
func TestProgramExecuteWithConditionCheck_NoWriterConfigured(t *testing.T) {
	t.Parallel()
	p := NewProgram("main", "1", "NoWriter", "", false, nil)

	executed, err := p.ExecuteWithConditionCheck(t.Context())
	if err == nil {
		t.Fatal("expected error for a program with no writer configured")
	}
	if executed {
		t.Fatal("executed=true on no-writer path, want false")
	}
}

// TestProgramExecuteWithConditionCheck_ErrorNotifies verifies the notifier
// fires with success=false and the error propagates when the conditional
// writer fails.
func TestProgramExecuteWithConditionCheck_ErrorNotifies(t *testing.T) {
	t.Parallel()
	wantErr := errors.New("rega failed")
	w := &stubConditionalProgramWriter{condErr: wantErr}
	p := NewProgram("main", "42", "Cond", "", false, w)

	var calls int
	notifiedSuccess := true
	p.ExecuteNotifier = func(_ context.Context, _ string, _ hmenum.ProgramTrigger, ok bool) {
		calls++
		notifiedSuccess = ok
	}

	executed, err := p.ExecuteWithConditionCheck(t.Context())
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if executed {
		t.Fatal("executed=true on error path, want false")
	}
	if calls != 1 || notifiedSuccess {
		t.Fatalf("notifier calls=%d success=%v, want 1/false", calls, notifiedSuccess)
	}
}
