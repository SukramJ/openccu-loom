// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// --- SysvarDpSwitch ---

// --- SysvarDpBinarySensor ---

// --- SysvarDpText ---

// --- ProgramDpButton ---

// TestProgramDpButtonPress verifies that Press delegates to the
// program's writer via Execute.
func TestProgramDpButtonPress(t *testing.T) {
	w := &stubProgram{}
	pg := NewProgram("c1", "prog-42", "Lights Off", "", false, w)
	btn := &ProgramDpButton{Program: pg}
	if err := btn.Press(context.Background()); err != nil {
		t.Fatalf("Press() unexpected error: %v", err)
	}
	if got := w.lastID.Load(); got != "prog-42" {
		t.Errorf("Press() called writer with id=%v want %q", got, "prog-42")
	}
}

// TestSysvarInternalIsRaceFreeAgainstAHubScan pins that the internal flag
// is read and written through the sysvar's own lock.
//
// Every hub scan rewrites the flag on the live objects north-bound
// listings are walking at the same time, so a reader that took the field
// directly raced the refresh — visible only under -race, and only when a
// listing happened to overlap a scan.
func TestSysvarInternalIsRaceFreeAgainstAHubScan(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccu1", "Presence", "", hmenum.HubValueTypeLogic, nil)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 500 {
			sv.SetInternal(i%2 == 0)
		}
	}()
	for range 500 {
		_ = sv.Internal()
	}
	<-done

	sv.SetInternal(true)
	if !sv.Internal() {
		t.Error("Internal() did not report the value SetInternal wrote")
	}
}
