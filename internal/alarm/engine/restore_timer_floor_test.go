// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// implausibleRestoreEpoch is far enough behind the harness clock that
// clockPlausible refuses the wall-clock arithmetic, which is the branch
// that resumes the persisted relative remaining duration verbatim.
var implausibleRestoreEpoch = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// rewriteTimerRemaining stops the engine and rewrites the persisted
// timer tuple of kind so it carries remainingMS, reproducing a state row
// written the instant a countdown reached zero.
func rewriteTimerRemaining(h *harness, zoneID, kind string, remainingMS int64) {
	h.t.Helper()
	h.eng.Stop(h.ctx)
	row, ok, err := h.states.Get(h.ctx, zoneID)
	if err != nil || !ok {
		h.t.Fatalf("state row for %q: ok=%v err=%v", zoneID, ok, err)
	}
	var tuples []map[string]any
	if err := json.Unmarshal([]byte(row.TimersJSON), &tuples); err != nil {
		h.t.Fatalf("timers json: %v", err)
	}
	found := false
	for _, tu := range tuples {
		if tu["kind"] == kind {
			tu["remaining_ms"] = remainingMS
			found = true
		}
	}
	if !found {
		h.t.Fatalf("no %q timer tuple persisted; got %s", kind, row.TimersJSON)
	}
	raw, err := json.Marshal(tuples)
	if err != nil {
		h.t.Fatalf("marshal timers: %v", err)
	}
	row.TimersJSON = string(raw)
	if err := h.states.Upsert(h.ctx, row); err != nil {
		h.t.Fatalf("upsert state row: %v", err)
	}
}

// resumedRemainingMS returns the remaining_ms detail of the first
// journal entry with event.
func resumedRemainingMS(t *testing.T, h *harness, event string) int64 {
	t.Helper()
	entry := mustJournalEntry(t, h.journal, event)
	raw, ok := entry.Details["remaining_ms"]
	if !ok {
		t.Fatalf("%q entry carries no remaining_ms: %+v", event, entry.Details)
	}
	got, ok := raw.(int64)
	if !ok {
		t.Fatalf("%q remaining_ms is %T, want int64", event, raw)
	}
	return got
}

// A countdown resumed at boot with an already-elapsed remaining
// duration must be rescheduled at the floor, not at zero: a zero-length
// timer is a countdown that never visibly runs. Both resuming restore
// paths share the floor, so both are measured here.
func TestRestore_ElapsedExitDelayResumesAtTheTimerFloor(t *testing.T) {
	h := newHarness(t)
	h.seedStandardZone()
	h.start()
	if _, err := h.eng.Arm(h.ctx, "eg", engine.ArmRequest{Mode: hmenum.AlarmModeFull, By: "tester"}); err != nil {
		t.Fatalf("arm: %v", err)
	}
	h.advance(10 * time.Second)

	rewriteTimerRemaining(h, "eg", "exit_delay", 0)
	h.freshPorts(implausibleRestoreEpoch)
	h.start()

	h.wantState("eg", hmenum.AlarmZoneStateArming)
	if got := resumedRemainingMS(t, h, "arming_resumed"); got != time.Second.Milliseconds() {
		t.Errorf("resumed exit delay = %d ms, want %d (the floor)", got, time.Second.Milliseconds())
	}
}

func TestRestore_ElapsedAutoRearmResumesAtTheTimerFloor(t *testing.T) {
	h := newHarness(t)
	seedAutoRearmZone(h)
	h.start()
	triggerAndDisarm(h)

	rewriteTimerRemaining(h, "eg", "auto_rearm", 0)
	h.freshPorts(implausibleRestoreEpoch)
	h.start()

	if got := resumedRemainingMS(t, h, "auto_rearm_resumed"); got != time.Second.Milliseconds() {
		t.Errorf("resumed auto-rearm = %d ms, want %d (the floor)", got, time.Second.Milliseconds())
	}
}
