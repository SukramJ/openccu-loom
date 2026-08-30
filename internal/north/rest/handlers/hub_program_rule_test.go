// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package handlers

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
)

// TestProgramSummaryExecuteAvailableFollowsTheModel pins the REST projection
// to the model's rule for whether a program may be run.
//
// The WebSocket frame was moved onto hub.ProgramExecuteAvailable in an earlier
// round and this copy was left behind — a half fix, which is worse than none:
// the two planes now looked consistent while only one of them tracked the
// domain. The rule is trivial today (a deactivated program cannot run), which
// is exactly why a restatement survives unnoticed until the day it stops being
// trivial.
func TestProgramSummaryExecuteAvailableFollowsTheModel(t *testing.T) {
	t.Parallel()
	for _, active := range []bool{true, false} {
		p := hub.NewProgram("home", "1234", "Test", "", false, nil)
		p.OnActive(active)
		got := toProgramSummary(p, "home", "11a0001234").ExecuteAvailable
		if want := hub.ProgramExecuteAvailable(active); got != want {
			t.Errorf("active=%v: execute_available = %v, the model says %v", active, got, want)
		}
	}
}

// TestProgramSummaryFailsOpenBeforeObservation keeps the other half: a program
// whose active flag has not been observed is runnable, so a consumer never
// greys out an action on missing information.
func TestProgramSummaryFailsOpenBeforeObservation(t *testing.T) {
	t.Parallel()
	p := hub.NewProgram("home", "1234", "Test", "", false, nil)
	if !toProgramSummary(p, "home", "11a0001234").ExecuteAvailable {
		t.Error("an unobserved program must stay runnable")
	}
}
