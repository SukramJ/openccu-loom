// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"errors"
	"log/slog"
	"sync"
)

// Registry holds the registered north-bound Services and drives their
// lifecycle as a group. It is safe for concurrent use; in practice all
// registration happens during daemon bring-up (single goroutine) and
// StartAll/StopAll are called once each.
type Registry struct {
	logger *slog.Logger

	mu       sync.Mutex
	services []Service
	started  []Service // services successfully started, in start order
}

// NewRegistry returns an empty Registry. A nil logger falls back to the
// slog default so callers need not guard it.
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{logger: logger}
}

// Register adds s to the Registry. Registration order is preserved: StartAll
// starts in registration order, StopAll stops in reverse. A nil Service is
// ignored.
func (r *Registry) Register(s Service) {
	if s == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.services = append(r.services, s)
}

// Services returns a snapshot of the registered Services in registration
// order. The returned slice is a copy; mutating it does not affect the
// Registry.
func (r *Registry) Services() []Service {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Service, len(r.services))
	copy(out, r.services)
	return out
}

// StartAll starts every registered Service in registration order. On the
// first Start error it stops the Services already started (reverse order,
// best-effort) and returns the error, so a partial start never leaves
// dangling goroutines.
func (r *Registry) StartAll(ctx context.Context) error {
	r.mu.Lock()
	services := make([]Service, len(r.services))
	copy(services, r.services)
	r.mu.Unlock()

	for _, s := range services {
		if err := s.Start(ctx); err != nil {
			r.logger.Error("north bridge start failed",
				slog.String("service", s.Name()),
				slog.String("err", err.Error()))
			r.stopStarted(ctx)
			return err
		}
		r.mu.Lock()
		r.started = append(r.started, s)
		r.mu.Unlock()
		r.logger.Debug("north bridge started", slog.String("service", s.Name()))
	}
	return nil
}

// StopAll stops every started Service in reverse start order, best-effort.
// Stop errors are logged, not returned, so one failing Service never blocks
// the others from shutting down. Safe to call even if StartAll was never
// called or already partially rolled back.
func (r *Registry) StopAll(ctx context.Context) {
	r.stopStarted(ctx)
}

// stopStarted stops the currently-started Services in reverse order and
// clears the started set. It is the shared teardown path for both StopAll
// and the StartAll rollback.
func (r *Registry) stopStarted(ctx context.Context) {
	r.mu.Lock()
	started := r.started
	r.started = nil
	r.mu.Unlock()

	for i := len(started) - 1; i >= 0; i-- {
		s := started[i]
		if err := s.Stop(ctx); err != nil {
			r.logger.Warn("north bridge stop failed",
				slog.String("service", s.Name()),
				slog.String("err", err.Error()))
			continue
		}
		r.logger.Debug("north bridge stopped", slog.String("service", s.Name()))
	}
}

// Health aggregates the liveness of every registered Service that
// implements HealthReporter. ok is false if any reporter is unhealthy;
// detail then names the first unhealthy Service and its reason. Services
// that do not implement HealthReporter are treated as healthy.
func (r *Registry) Health() (ok bool, detail string) {
	r.mu.Lock()
	services := make([]Service, len(r.services))
	copy(services, r.services)
	r.mu.Unlock()

	var unhealthy []string
	for _, s := range services {
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
