// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"context"
	"sync"
	"time"
)

// PerExchangePaseProvider is a [PaseHandlerProvider] that lazily
// allocates a fresh [PaseAdapter] per inbound PASE exchange ID. It
// enforces the Matter §11.19.7 single-active-PASE-session invariant
// and the 60 s per-exchange hard timeout (Matter §4.13.1.7).
//
// The factory MUST return a fully-configured PaseAdapter (PBKDF
// params + random source + onSessionEstablished callback already
// wired) — the provider only handles the per-exchange isolation.
//
// Memory model: every Resolve stamps the entry's `lastTouched` so
// stale entries (no Resolve hit within `ttl`) get reaped by the
// optional [PerExchangePaseProvider.StartReaper] background goroutine.
// Operators pairing thousands of distinct commissioners over a
// daemon's lifetime should always wire the reaper; without it the
// adapters map grows unboundedly.
type PerExchangePaseProvider struct {
	mu       sync.Mutex
	entries  map[uint16]*paseEntry
	factory  func() *PaseAdapter
	cancel   context.CancelFunc
	reaperWG sync.WaitGroup
}

type paseEntry struct {
	adapter     *PaseAdapter
	lastTouched time.Time
	// expireTimer is the per-exchange 60 s hard-timeout timer started at
	// Resolve-time. Fires Forget(exchangeID) when the PASE exchange does
	// not complete within 60 s per Matter §4.13.1.7.
	// Mirrors matter.js packages/protocol/src/session/pase/PaseServer.ts:37
	// `PASE_PAIRING_TIMEOUT = Seconds(60)`.
	expireTimer *time.Timer
}

// NewPerExchangePaseProvider constructs a provider backed by factory.
// A nil factory produces a provider that always returns nil from
// Resolve — equivalent to "no PASE handler", which the SC router
// degrades to a debug-logged drop.
func NewPerExchangePaseProvider(factory func() *PaseAdapter) *PerExchangePaseProvider {
	return &PerExchangePaseProvider{
		entries: make(map[uint16]*paseEntry),
		factory: factory,
	}
}

// Resolve implements [PaseHandlerProvider]. Returns the existing
// adapter for exchangeID, allocating a fresh one via the factory on
// first sighting. Returns nil when the factory is nil or returned
// nil; callers (the SC router) drop the datagram on nil. Every
// successful Resolve refreshes the entry's `lastTouched`.
//
// Concurrency: the Matter §11.19.7 single-active-PASE invariant is
// enforced one layer up by CommissioningWindow.open — only one window
// is open at a time, and PASE traffic outside that window is dropped
// at the SC router. The per-exchange routing here only multiplexes
// datagrams of the SAME active window across retransmits.
//
// 60 s hard timeout per Matter §4.13.1.7: every new exchange starts a
// 60 s timer that calls Forget(exchangeID) on expiry. Mirrors
// matter.js PaseServer.ts:37 `PASE_PAIRING_TIMEOUT = Seconds(60)`.
func (p *PerExchangePaseProvider) Resolve(exchangeID uint16) PaseHandler {
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
	eid := exchangeID // capture for closure
	// Enforce 60 s per-exchange hard timeout per Matter §4.13.1.7.
	expireTimer := time.AfterFunc(60*time.Second, func() {
		p.Forget(eid)
	})
	p.entries[exchangeID] = &paseEntry{
		adapter:     a,
		lastTouched: time.Now(),
		expireTimer: expireTimer,
	}
	return a
}

// Forget removes the adapter for exchangeID so the next
// [Resolve] call allocates a fresh one. Operators wire this from a
// post-Pake3 hook when they want immediate cleanup; production
// daemons typically rely on the TTL reaper instead.
// Also cancels the entry's 60 s hard-timeout timer.
func (p *PerExchangePaseProvider) Forget(exchangeID uint16) {
	p.mu.Lock()
	e := p.entries[exchangeID]
	delete(p.entries, exchangeID)
	p.mu.Unlock()
	if e != nil && e.expireTimer != nil {
		e.expireTimer.Stop()
	}
}

// Reset drops every per-exchange adapter. Operators call this when
// rotating the underlying PBKDF params so an in-flight commissioner
// cannot complete against the stale transcript.
// Also cancels all pending 60 s hard-timeout timers.
func (p *PerExchangePaseProvider) Reset() {
	p.mu.Lock()
	old := p.entries
	p.entries = make(map[uint16]*paseEntry)
	p.mu.Unlock()
	for _, e := range old {
		if e.expireTimer != nil {
			e.expireTimer.Stop()
		}
	}
}

// Active reports the count of in-flight per-exchange adapters. Test
// helper.
func (p *PerExchangePaseProvider) Active() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// StartReaper launches a background goroutine that ticks every
// `interval` and evicts any adapter whose `lastTouched` is older
// than `ttl`. Idempotent — a second StartReaper call cancels the
// previous goroutine before launching a fresh one.
//
// Recommended values: interval = ttl/4, ttl = 60s. PASE exchanges
// that complete cleanly take < 5s; a 60s window comfortably covers
// chip-tool retransmits while keeping the map size bounded.
//
// Pass `ctx.Done()`-cancellation OR call [Stop] to terminate the
// reaper.
func (p *PerExchangePaseProvider) StartReaper(parent context.Context, interval, ttl time.Duration) {
	if interval <= 0 || ttl <= 0 {
		return
	}
	p.Stop() // cancel any prior reaper

	ctx, cancel := context.WithCancel(parent)
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()

	p.reaperWG.Go(func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				p.reapBefore(now.Add(-ttl))
			}
		}
	})
}

// Stop cancels the background reaper started by [StartReaper] and
// blocks until the goroutine exits. Safe to call when no reaper is
// running. Idempotent.
func (p *PerExchangePaseProvider) Stop() {
	p.mu.Lock()
	cancel := p.cancel
	p.cancel = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	p.reaperWG.Wait()
}

// reapBefore evicts every entry whose lastTouched is at or before
// the supplied cutoff. Exposed at the package boundary so tests can
// drive eviction without spinning a real ticker.
// Also cancels the 60 s hard-timeout timer for each reaped entry.
func (p *PerExchangePaseProvider) reapBefore(cutoff time.Time) {
	p.mu.Lock()
	var reaped []*paseEntry
	for k, e := range p.entries {
		if !e.lastTouched.After(cutoff) {
			reaped = append(reaped, e)
			delete(p.entries, k)
		}
	}
	p.mu.Unlock()
	for _, e := range reaped {
		if e.expireTimer != nil {
			e.expireTimer.Stop()
		}
	}
}
