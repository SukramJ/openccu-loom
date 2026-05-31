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
