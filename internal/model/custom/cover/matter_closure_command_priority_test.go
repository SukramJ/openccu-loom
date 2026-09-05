// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package cover

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	clusterwire "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// closurePriorityWriter records the CommandPriority of every southbound
// write. The package's recordWriter drops it, which is the value under
// test here.
type closurePriorityWriter struct {
	mu   sync.Mutex
	seen []hmenum.CommandPriority
}

func (w *closurePriorityWriter) SetValue(
	_ context.Context, _ string, _ hmenum.Parameter, _ any, priority hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = append(w.seen, priority)
	return nil
}

func (w *closurePriorityWriter) priorities() []hmenum.CommandPriority {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]hmenum.CommandPriority(nil), w.seen...)
}

// TestClosureCommandsReachTheWriterAtHighPriority pins the southbound
// urgency of a ClosureControl command on a garage drive.
//
// The cluster server used to hand the priority into the Move / Stop
// handlers as an argument. It no longer does — the cluster contract
// names no host enum at all — so the value is decided by the handlers in
// newGarageClosureServer, and only a test that drives MatterInvoke and
// reads the writer can still observe it.
//
// The regression is silent: nothing between the writer and the command
// queue branches on the priority. And CommandPriorityCritical is the
// ZERO value, so the natural regression — a dropped constant, an unset
// field — escalates every bridged drive command rather than degrading
// it.
func TestClosureCommandsReachTheWriterAtHighPriority(t *testing.T) {
	t.Parallel()
	open := clusterwire.ClosureTargetPositionMoveToFullyOpen
	closed := clusterwire.ClosureTargetPositionMoveToFullyClosed
	vent := clusterwire.ClosureTargetPositionMoveToVentilationPosition

	for _, c := range []struct {
		what   string
		cmdID  uint32
		fields any
	}{
		{"MoveTo fully open", clusterwire.ClosureControlCmdMoveTo, clusterwire.MoveToRequest{Position: &open}},
		{"MoveTo fully closed", clusterwire.ClosureControlCmdMoveTo, clusterwire.MoveToRequest{Position: &closed}},
		{"MoveTo ventilation", clusterwire.ClosureControlCmdMoveTo, clusterwire.MoveToRequest{Position: &vent}},
		{"Stop", clusterwire.ClosureControlCmdStop, nil},
	} {
		w := &closurePriorityWriter{}
		ch := newGarageChannel(t, "MOD0001:1", w)
		g := NewGarage(GarageConfig{
			Channel:      ch,
			Writer:       w,
			Capabilities: custom.CoverCapabilities{SupportsVent: true, SupportsStop: true},
		})
		srv := g.closure.get(g)
		if srv == nil {
			t.Fatal("no closure projection was built — the guard lost its subject")
		}

		if _, err := srv.MatterInvoke(context.Background(), c.cmdID, c.fields); err != nil {
			t.Fatalf("MatterInvoke(%s): %v", c.what, err)
		}
		got := w.priorities()
		if len(got) == 0 {
			t.Fatalf("%s reached the writer 0 times, want at least 1", c.what)
		}
		for i, p := range got {
			if p != hmenum.CommandPriorityHigh {
				t.Errorf("%s: write %d queued at %v, want %v (Critical is the zero value, so an unset priority lands there)",
					c.what, i, p, hmenum.CommandPriorityHigh)
			}
		}
	}
}
