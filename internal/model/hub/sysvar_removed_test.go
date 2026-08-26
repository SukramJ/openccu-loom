// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHubRemoveSysvarFiresRemovalHooks pins the lifecycle half that keeps a
// deleted system variable from outliving its model entry on the north-bound
// planes: RemoveSysvar must fire the sysvar's removal hooks so subscribers can
// retract the retained MQTT discovery config they published for it.
func TestHubRemoveSysvarFiresRemovalHooks(t *testing.T) {
	t.Parallel()
	h := NewHub("ccu1")
	sv := NewSysvar("ccu1", "myvar", "desc", hmenum.HubValueTypeLogic, nil)
	h.PutSysvar(sv)

	var fired atomic.Int32
	sv.OnRemoved(func() { fired.Add(1) })

	if !h.RemoveSysvar("myvar") {
		t.Fatal("RemoveSysvar returned false for a registered sysvar")
	}
	if got := fired.Load(); got != 1 {
		t.Fatalf("removal hooks fired %d times, want 1", got)
	}
	// Removing an unknown name must not fire anything.
	if h.RemoveSysvar("myvar") {
		t.Fatal("RemoveSysvar returned true for an unknown sysvar")
	}
	if got := fired.Load(); got != 1 {
		t.Fatalf("removal hooks fired %d times after the second call, want 1", got)
	}
}

// TestSysvarOnRemovedUnsubscribe verifies the unsubscribe closure is honoured
// and idempotent, matching the Program hook semantics.
func TestSysvarOnRemovedUnsubscribe(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccu1", "myvar", "", hmenum.HubValueTypeLogic, nil)
	var fired atomic.Int32
	unsub := sv.OnRemoved(func() { fired.Add(1) })
	unsub()
	unsub() // idempotent
	sv.NotifyRemoved()
	if got := fired.Load(); got != 0 {
		t.Fatalf("unsubscribed handler fired %d times", got)
	}
}

// TestSysvarNotifyRemovedClearsHandlers verifies a second notification does not
// re-run the hooks, so a re-registered name cannot double-retract.
func TestSysvarNotifyRemovedClearsHandlers(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccu1", "myvar", "", hmenum.HubValueTypeLogic, nil)
	var fired atomic.Int32
	sv.OnRemoved(func() { fired.Add(1) })
	sv.NotifyRemoved()
	sv.NotifyRemoved()
	if got := fired.Load(); got != 1 {
		t.Fatalf("hooks fired %d times, want 1", got)
	}
}
