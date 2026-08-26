// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package events

import "sync"

// SubscriptionGroup bundles several Subscribe results so callers can drop all
// of them at once. The zero value is ready to use; the nil-receiver and
// empty-group paths are no-ops.
//
// Use it from any caller that owns more than one subscription with the same
// lifetime (typically a coordinator wiring up several hmevent types). Each
// Subscribe call hands its returned unsubscribe closure to [Add]; the
// eventual [Close] runs every closure exactly once and clears the group so a
// subsequent Close is idempotent.
type SubscriptionGroup struct {
	mu     sync.Mutex
	unsubs []func()
}

// Add records an unsubscribe closure. nil closures are ignored so
// callers can pass through a Subscribe return value without checking.
func (g *SubscriptionGroup) Add(unsubs ...func()) {
	if g == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, u := range unsubs {
		if u != nil {
			g.unsubs = append(g.unsubs, u)
		}
	}
}

// Len returns the number of subscriptions currently held.
func (g *SubscriptionGroup) Len() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.unsubs)
}

// Close runs every recorded unsubscribe closure and clears the group.
// Idempotent — a second Close is a no-op.
func (g *SubscriptionGroup) Close() {
	if g == nil {
		return
	}
	g.mu.Lock()
	unsubs := g.unsubs
	g.unsubs = nil
	g.mu.Unlock()
	for _, u := range unsubs {
		u()
	}
}
