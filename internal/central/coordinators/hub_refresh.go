// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/observability"
)

// refreshSlot owns one refresh type's hook and its two concurrency controls:
// sema serialises concurrent calls to the same refresh type; mu guards the hook
// field so set and get are race-free.
type refreshSlot struct {
	sema sync.Mutex   // serialises concurrent runs of this refresh type
	mu   sync.RWMutex // guards hook
	hook func(context.Context) error
}

// set stores fn as the active hook. A nil fn is silently ignored so that
// partial SetRefreshHooks calls do not clobber a previously wired hook.
func (s *refreshSlot) set(fn func(context.Context) error) {
	if fn == nil {
		return
	}
	s.mu.Lock()
	s.hook = fn
	s.mu.Unlock()
}

// get returns the current hook under the read lock. May return nil when no
// hook has been wired.
func (s *refreshSlot) get() func(context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.hook
}

// run serialises on sema, reads the hook, and runs it through
// observability.Instrument under the given op name. A nil hook is a
// no-op that returns nil without recording any latency observation.
func (s *refreshSlot) run(ctx context.Context, rec observability.Recorder, op string) error {
	s.sema.Lock()
	defer s.sema.Unlock()
	fn := s.get()
	if fn == nil {
		return nil
	}
	return observability.Instrument(ctx, rec, "hub_coordinator."+op, observability.ScopeCoordinator, fn)
}

// hubRefreshSet bundles the nine per-type refresh slots for HubCoordinator.
// Each field corresponds to one CCU hub data category.
type hubRefreshSet struct {
	programs        refreshSlot
	sysvars         refreshSlot
	inbox           refreshSlot
	serviceMessages refreshSlot
	alarmMessages   refreshSlot
	systemUpdate    refreshSlot
	installMode     refreshSlot
	metrics         refreshSlot
	connectivity    refreshSlot
}
