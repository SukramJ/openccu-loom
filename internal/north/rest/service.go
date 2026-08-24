// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// bindSettle is how long Start waits to catch a fast listener-bind failure
// before reporting success. Mirrors the previous serverGroup.startAll settle.
const bindSettle = 20 * time.Millisecond

// Service adapts the blocking [*Server] to the north-bound bridge.Service
// lifecycle contract: a non-blocking Start (spawns the serve goroutine and
// returns once the listener is bound) and an idempotent, context-bounded
// Stop. It is the PhaseLate REST/HTTP surface — the listener the SPA, REST
// API, WebSocket, MCP mount and diagnostics all ride on. See ADR 0047.
type Service struct {
	srv    *Server
	logger *slog.Logger

	mu      sync.Mutex
	started bool
	exit    chan error // receives Server.Start's terminal result
}

// NewService wraps srv as a bridge.Service named "rest".
func NewService(srv *Server, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{srv: srv, logger: logger}
}

// Name implements bridge.Service.
func (s *Service) Name() string { return "rest" }

// Start spawns the serve goroutine and returns once the listener is bound (or
// immediately with the bind error if it fails fast). Non-blocking. A terminal
// error after the settle window (rare) is logged from the goroutine.
func (s *Service) Start(_ context.Context) error {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	s.exit = make(chan error, 1)
	exit := s.exit
	s.mu.Unlock()

	//nolint:contextcheck // Server.Start has no ctx (it is the blocking serve loop with no cancellation point); shutdown is driven by Stop(ctx) below.
	go func() {
		err := s.srv.Start() // blocks until Shutdown; nil on clean stop
		if err != nil {
			s.logger.Error("rest.server.exit", slog.String("err", err.Error()))
		}
		exit <- err
	}()

	// Catch a fast bind failure (bad port / already in use) so boot can
	// surface it; otherwise report the listener as up.
	select {
	case err := <-exit:
		return err
	case <-time.After(bindSettle):
		s.mu.Lock()
		s.started = true
		s.mu.Unlock()
		return nil
	}
}

// Stop gracefully shuts the server down, honouring ctx. Idempotent.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		return nil
	}
	s.started = false
	s.mu.Unlock()
	return s.srv.Shutdown(ctx)
}

// Healthy implements bridge.HealthReporter: the REST surface is healthy while
// it is started (bound and serving).
//
// Nothing reads it. bridge.Registry.Health is its only consumer and has no
// caller, and the verdict could not travel anyway: the one state it reports as
// unhealthy is "not serving", which is also the state in which /health cannot
// be fetched. Kept as the interface's reference implementation, not as a live
// health source — the daemon's REST liveness is what the request you are
// answering already demonstrates.
func (s *Service) Healthy() (ok bool, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return true, ""
	}
	return false, "rest server not serving"
}
