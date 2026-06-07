// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package backends

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// MasterPoller polls a CCU's MASTER paramset shortly after a write because
// BidCos-RF interfaces do not echo MASTER changes back via the callback
// channel.
//
// The poller is *opportunistic*: callers schedule a poll via [SchedulePoll]
// after a successful `put_paramset(MASTER, …)` call; the poller runs on a
// separate goroutine and feeds the fresh paramset back into the cache via the
// `OnRefresh` callback.
//
// Per-(address, paramset) deduplication is built in: a second SchedulePoll
// for the same target overwrites the queued attempt rather than producing two
// parallel gets.
type MasterPoller struct {
	getter MasterGetter

	// Interval is how long the poller waits before issuing the read.
	// Defaults to 2 seconds when zero.
	Interval time.Duration

	// OnRefresh is called with the fresh paramset map. Errors are
	// logged by the caller. Setting nil disables the poller (the
	// schedule call becomes a no-op).
	OnRefresh func(address string, key hmenum.ParamsetKey, values map[string]any)

	// OnError, when set, is called when the underlying get fails.
	OnError func(address string, key hmenum.ParamsetKey, err error)

	mu        sync.Mutex
	scheduled map[pollKey]*pollEntry
	closed    bool
}

// MasterGetter is the read surface the poller depends on. The
// concrete CcuBackend satisfies it through its `GetParamset` method.
type MasterGetter interface {
	GetParamset(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error)
}

type pollKey struct {
	address string
	key     hmenum.ParamsetKey
}

type pollEntry struct {
	cancel context.CancelFunc
}

// NewMasterPoller constructs a poller that uses getter for reads.
// Pass nil for `OnRefresh` later to disable.
func NewMasterPoller(getter MasterGetter) *MasterPoller {
	return &MasterPoller{
		getter:    getter,
		Interval:  2 * time.Second,
		scheduled: make(map[pollKey]*pollEntry),
	}
}

// SchedulePoll queues a delayed get_paramset for (address, key).
// Subsequent calls before the timer fires are deduplicated — only
// one poll runs per (address, key) tuple.
//
// Returns silently when the poller is closed or `OnRefresh` is nil
// — those are not errors, just configuration choices.
func (p *MasterPoller) SchedulePoll(address string, key hmenum.ParamsetKey) {
	if p == nil || p.OnRefresh == nil {
		return
	}
	pk := pollKey{address: address, key: key}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	if existing, ok := p.scheduled[pk]; ok {
		existing.cancel()
	}
	// cancel is invoked via entry.cancel() either by a deduplicating
	// SchedulePoll call or by the deferred cleanup in run() — the
	// gosec G118 lint can't see the indirection so we annotate.
	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel called via entry.cancel in run()/SchedulePoll; see #20
	p.scheduled[pk] = &pollEntry{cancel: cancel}
	interval := p.Interval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	p.mu.Unlock()

	go p.run(ctx, pk, interval)
}

// run waits for the configured interval (or cancellation) and then
// performs the get + dispatch.
func (p *MasterPoller) run(ctx context.Context, pk pollKey, interval time.Duration) {
	defer func() {
		p.mu.Lock()
		if entry, ok := p.scheduled[pk]; ok && entry.cancel != nil {
			// Release the context resources even on natural
			// completion — keeps gosec G118 happy and avoids the
			// timer goroutine inside ctx lingering until GC.
			entry.cancel()
			delete(p.scheduled, pk)
		}
		p.mu.Unlock()
	}()
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return
	case <-timer.C:
	}

	values, err := p.getter.GetParamset(ctx, pk.address, pk.key)
	if err != nil {
		if !errors.Is(err, context.Canceled) && p.OnError != nil {
			p.OnError(pk.address, pk.key, err)
		}
		return
	}
	if p.OnRefresh != nil {
		p.OnRefresh(pk.address, pk.key, values)
	}
}

// Close cancels every pending poll and rejects further schedules.
func (p *MasterPoller) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.closed = true
	for _, entry := range p.scheduled {
		entry.cancel()
	}
	p.scheduled = nil
	p.mu.Unlock()
}
