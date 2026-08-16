// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// entry is one registered Service plus its start Phase and whether it is
// currently started. Pointer-held so start state mutates in place and the
// start-order slice can reference the same value.
type entry struct {
	svc     Service
	phase   Phase
	started bool
}

// Registry holds the registered north-bound Services and drives their
// lifecycle as a group. Services are started per Phase (StartPhase) or all at
// once (StartAll) and stopped together in reverse start order (StopAll). It is
// safe for concurrent use; in practice registration happens during daemon
// bring-up (single goroutine) and the start/stop calls are made once each.
type Registry struct {
	logger *slog.Logger

	mu      sync.Mutex
	entries []*entry // registration order
	order   []*entry // successfully-started, in start order (for reverse stop)
}

// NewRegistry returns an empty Registry. A nil logger falls back to the
// slog default so callers need not guard it.
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{logger: logger}
}

// Register adds s to the Registry in the default late phase. Registration
// order is preserved within a phase: a phase starts in registration order and
// StopAll stops in reverse start order. A nil Service is ignored.
func (r *Registry) Register(s Service) {
	r.RegisterPhase(s, PhaseLate)
}

// RegisterPhase adds s to the Registry with an explicit start Phase. A nil
// Service is ignored.
func (r *Registry) RegisterPhase(s Service, phase Phase) {
	if s == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, &entry{svc: s, phase: phase})
}

// Services returns a snapshot of the registered Services in registration
// order. The returned slice is a copy; mutating it does not affect the
// Registry.
func (r *Registry) Services() []Service {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Service, len(r.entries))
	for i, e := range r.entries {
		out[i] = e.svc
	}
	return out
}

// StartAll starts every not-yet-started registered Service, in registration
// order across all phases. Suitable for the single-phase case. On the first
// Start error it stops the Services already started (reverse order,
// best-effort) and returns the error.
func (r *Registry) StartAll(ctx context.Context) error {
	return r.startMatching(ctx, func(*entry) bool { return true })
}

// StartPhase starts the not-yet-started registered Services whose Phase equals
// phase, in registration order. Same rollback semantics as StartAll: a failed
// Start rolls back every Service started so far (across all phases), so a
// partial bring-up never leaves dangling goroutines.
func (r *Registry) StartPhase(ctx context.Context, phase Phase) error {
	return r.startMatching(ctx, func(e *entry) bool { return e.phase == phase })
}

// startMatching starts each not-yet-started entry matching want, in
// registration order, rolling back all started Services on the first error.
func (r *Registry) startMatching(ctx context.Context, want func(*entry) bool) error {
	r.mu.Lock()
	todo := make([]*entry, 0, len(r.entries))
	for _, e := range r.entries {
		if !e.started && want(e) {
			todo = append(todo, e)
		}
	}
	r.mu.Unlock()

	for _, e := range todo {
		if err := e.svc.Start(ctx); err != nil {
			r.logger.Error("north bridge start failed",
				slog.String("service", e.svc.Name()),
				slog.String("err", err.Error()))
			r.stopStarted(ctx)
			return err
		}
		r.mu.Lock()
		e.started = true
		r.order = append(r.order, e)
		r.mu.Unlock()
		r.logger.Debug("north bridge started", slog.String("service", e.svc.Name()))
	}
	return nil
}

// StopAll stops every started Service in reverse start order, best-effort.
// Stop errors are logged, not returned, so one failing Service never blocks
// the others from shutting down. Safe to call even if nothing was started or
// after a partial rollback.
func (r *Registry) StopAll(ctx context.Context) {
	r.stopStarted(ctx)
}

// stopStarted stops the currently-started Services in reverse start order and
// clears the started set. Shared by StopAll and the start rollback.
func (r *Registry) stopStarted(ctx context.Context) {
	r.mu.Lock()
	order := r.order
	r.order = nil
	for _, e := range order {
		e.started = false
	}
	r.mu.Unlock()

	for i := len(order) - 1; i >= 0; i-- {
		s := order[i].svc
		if err := s.Stop(ctx); err != nil {
			r.logger.Warn("north bridge stop failed",
				slog.String("service", s.Name()),
				slog.String("err", err.Error()))
			continue
		}
		r.logger.Debug("north bridge stopped", slog.String("service", s.Name()))
	}
}

// Health aggregates the liveness of every registered Service that implements
// HealthReporter. ok is false if any reporter is unhealthy; detail then names
// the unhealthy Services and their reasons. Services that do not implement
// HealthReporter are treated as healthy.
//
// A HealthReporter's detail is carried ONLY when it reports unhealthy: a
// healthy reporter's own detail (e.g. a webhook's dropped/failed counters) is
// intentionally not surfaced here — this aggregate is a red/green liveness
// summary, not a metrics channel. Reporters that want their healthy counters
// observed expose them directly (Outbound.Dropped/Failed) or through the log.
func (r *Registry) Health() (ok bool, detail string) {
	r.mu.Lock()
	svcs := make([]Service, len(r.entries))
	for i, e := range r.entries {
		svcs[i] = e.svc
	}
	r.mu.Unlock()

	var unhealthy []string
	for _, s := range svcs {
		hr, isReporter := s.(HealthReporter)
		if !isReporter {
			continue
		}
		if healthy, reason := hr.Healthy(); !healthy {
			unhealthy = append(unhealthy, s.Name()+": "+reason)
		}
	}
	if len(unhealthy) == 0 {
		return true, ""
	}
	return false, errors.Join(toErrors(unhealthy)...).Error()
}

// toErrors converts detail strings to errors so errors.Join can render a
// stable, newline-joined summary without us hand-rolling the separator.
func toErrors(msgs []string) []error {
	out := make([]error, len(msgs))
	for i, m := range msgs {
		out[i] = errors.New(m)
	}
	return out
}
