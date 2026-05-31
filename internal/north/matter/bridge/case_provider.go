// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"sync"
	"time"
)

// PerExchangeCaseProvider is a [CaseHandlerProvider] that lazily
// allocates a fresh [CaseAdapter] per inbound CASE exchange ID. It
// supports concurrent operational sessions by isolating responder
// state per exchange — Apple Home opens parallel CASE sessions from
// the iPhone (IPv6) and HomePod (IPv4); a singleton CaseAdapter
// gets stuck in `Finished` after the first successful Sigma3 and
// rejects every subsequent Sigma1 with `ProcessSigma1 already called`.
//
// The factory MUST return a fully-configured CaseAdapter (sigma
// responder seeded from the current operational identity, fabric
// verifier, OnSessionEstablished callback wired). Operators rotate
// the underlying identity by replacing the factory closure.
//
// Memory model: every Resolve stamps the entry's `lastTouched` so
// stale entries (no Resolve hit within `ttl`) get reaped by the
// optional [PerExchangeCaseProvider.StartReaper] background goroutine.
// Daemons handling many concurrent commissioners over time should
// always wire the reaper; without it the adapters map grows
// unboundedly.
//
// The optional onEvict callback (set via [PerExchangeCaseProvider.SetOnEvict])
// is called on TTL-eviction of each stale entry — the bridge wires
// [Bridge.forgetSigma1Replied] here so aborted CASE exchanges
// (Sigma1 arrived, Sigma3 never came) do not leak entries in the
// sigma1Replied dedupe map — see [Bridge.forgetSigma1Replied].
type PerExchangeCaseProvider struct {
	mu       sync.Mutex
	entries  map[uint16]*caseEntry
	factory  func() *CaseAdapter
	onEvict  func(exchangeID uint16) // optional; called when an entry is TTL-reaped
	cancel   context.CancelFunc
	reaperWG sync.WaitGroup
}

type caseEntry struct {
	adapter     *CaseAdapter
	lastTouched time.Time
}

// NewPerExchangeCaseProvider constructs a provider backed by factory.
// A nil factory produces a provider that always returns nil from
// Resolve — equivalent to "no CASE handler", which the SC router
// degrades to a debug-logged drop.
func NewPerExchangeCaseProvider(factory func() *CaseAdapter) *PerExchangeCaseProvider {
	return &PerExchangeCaseProvider{
		entries: make(map[uint16]*caseEntry),
		factory: factory,
	}
}

// Resolve implements [CaseHandlerProvider]. Returns the existing
// adapter for exchangeID, allocating a fresh one via the factory on
// first sighting. Returns nil when the factory is nil or returned
// nil; callers (the SC router) drop the datagram on nil. Every
// successful Resolve refreshes the entry's `lastTouched`.
func (p *PerExchangeCaseProvider) Resolve(exchangeID uint16) CaseHandler {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.entries[exchangeID]; ok {
		e.lastTouched = time.Now()
		return e.adapter
	}
	if p.factory == nil {
		return nil
	}
	a := p.factory()
	if a == nil {
		return nil
	}
	p.entries[exchangeID] = &caseEntry{adapter: a, lastTouched: time.Now()}
	return a
}

// SetOnEvict wires the TTL-eviction callback. The bridge sets this to
// [Bridge.forgetSigma1Replied] so aborted CASE exchanges (Sigma1
// arrived, Sigma3 never completed) do not leak entries in the
// sigma1Replied dedupe map.
// Mirrors matter.js packages/protocol/src/session/case/CaseServer.ts
// per-exchange handler GC: TypeScript's GC reclaims the per-exchange
// handler automatically on exchange.close(); Go needs an explicit hook
// because the dedupe map is Bridge-owned, not handler-owned.
// Pass nil to detach.
func (p *PerExchangeCaseProvider) SetOnEvict(fn func(exchangeID uint16)) {
	p.mu.Lock()
	p.onEvict = fn
	p.mu.Unlock()
}

// Forget removes the adapter for exchangeID so the next [Resolve]
// allocates a fresh one. Operators wire this from a post-Sigma3
// hook for immediate cleanup; production daemons typically rely on
// the TTL reaper instead.
func (p *PerExchangeCaseProvider) Forget(exchangeID uint16) {
	p.mu.Lock()
	delete(p.entries, exchangeID)
	p.mu.Unlock()
}

// Reset drops every per-exchange adapter. Operators call this when
// rotating the underlying identity so an in-flight commissioner
// cannot complete against the stale identity. The onEvict hook is
// called for every dropped entry so the Bridge's sigma1Replied map
// is also cleared.
func (p *PerExchangeCaseProvider) Reset() {
	p.mu.Lock()
	evicted := make([]uint16, 0, len(p.entries))
	for k := range p.entries {
		delete(p.entries, k)
		evicted = append(evicted, k)
	}
	onEvict := p.onEvict
	p.mu.Unlock()
	if onEvict != nil {
		for _, id := range evicted {
			onEvict(id)
		}
	}
}

// Active reports the count of in-flight per-exchange adapters. Test
// helper.
func (p *PerExchangeCaseProvider) Active() int {
	p.mu.Lock()
	n := len(p.entries)
	p.mu.Unlock()
	return n
}

// StartReaper spawns a background goroutine that drops adapters
// untouched for `ttl`, scanning every `interval`. Cancel via
// [PerExchangeCaseProvider.StopReaper]. Calling twice without an
// intervening Stop is a no-op.
func (p *PerExchangeCaseProvider) StartReaper(parent context.Context, interval, ttl time.Duration) {
	p.mu.Lock()
	if p.cancel != nil {
		p.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	p.cancel = cancel
	p.mu.Unlock()

	p.reaperWG.Add(1)
	go func() {
		defer p.reaperWG.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				p.reapLocked(ttl)
			}
		}
	}()
}

// StopReaper stops the reaper goroutine and waits for it to exit.
func (p *PerExchangeCaseProvider) StopReaper() {
	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	p.reaperWG.Wait()
}

func (p *PerExchangeCaseProvider) reapLocked(ttl time.Duration) {
	cutoff := time.Now().Add(-ttl)
	p.mu.Lock()
	var evicted []uint16
	for k, e := range p.entries {
		if e.lastTouched.Before(cutoff) {
			delete(p.entries, k)
			evicted = append(evicted, k)
		}
	}
	onEvict := p.onEvict
	p.mu.Unlock()
	// Call onEvict outside the lock; the callback (forgetSigma1Replied)
	// acquires the bridge's write lock and must not nest inside our mutex.
	if onEvict != nil {
		for _, id := range evicted {
			onEvict(id)
		}
	}
}
