// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package lock

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/wire"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// priorityWriter records the CommandPriority of every southbound write.
// The package's own stubWriter drops the priority, which is exactly the
// value under test here.
type priorityWriter struct {
	mu   sync.Mutex
	seen []hmenum.CommandPriority
}

func (w *priorityWriter) SetValue(
	_ context.Context, _ string, _ hmenum.Parameter, _ any, priority hmenum.CommandPriority,
) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = append(w.seen, priority)
	return nil
}

func (w *priorityWriter) priorities() []hmenum.CommandPriority {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]hmenum.CommandPriority(nil), w.seen...)
}

// TestMatterLockCommandsReachTheWriterAtHighPriority pins the southbound
// urgency of a bridged lock operation.
//
// The DoorLock cluster server used to hand the priority down as an
// argument on StateSource.LockInvoke. It no longer does — the cluster
// contract names no host enum at all — so the value is decided here, by
// matterDispatchPriority, and only a test that drives MatterInvoke and
// reads the writer can still observe it.
//
// The regression is silent: nothing between the writer and the command
// queue branches on the priority, so a wrong value changes queue
// ordering under load and nothing else. And CommandPriorityCritical is
// the ZERO value, so the natural regression — a dropped constant, an
// unset field — escalates every bridged lock command rather than
// degrading it.
func TestMatterLockCommandsReachTheWriterAtHighPriority(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		what  string
		cmdID uint32
	}{
		{"LockDoor", wire.DoorLockCmdLockDoor},
		{"UnlockDoor", wire.DoorLockCmdUnlockDoor},
		{"UnboltDoor", wire.DoorLockCmdUnboltDoor},
	} {
		w := &priorityWriter{}
		r := newRig(t, "HmIP-DLD:1", KindIP, w, custom.LockCapabilities{SupportsOpen: true})
		srv := r.lock.MatterClusterServers()[0]

		if _, err := srv.MatterInvoke(context.Background(), c.cmdID, nil); err != nil {
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
