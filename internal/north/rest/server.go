// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rest

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// Server is a small wrapper around [*http.Server] with a blocking
// Start plus a Shutdown that honors the supplied context.
type Server struct {
	srv    *http.Server
	logger *slog.Logger
	listen string
	// addr captures the bound listener address (after Start picks the
	// real port when listen was `:0`). atomic so Addr() can race-safely
	// observe the assignment from a different goroutine.
	addr atomic.Value
}

// NewServer constructs a Server bound to listen. handler is typically
// the router from [NewRouter], but any [http.Handler] works.
func NewServer(listen string, handler http.Handler, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		listen: listen,
		logger: logger,
		srv: &http.Server{
			Addr:              listen,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
		},
	}
}

// Start listens on the configured address and blocks until the
// server is shut down. Returns [http.ErrServerClosed] on a clean
// shutdown.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}
	bound := ln.Addr().String()
	s.addr.Store(bound)
	s.srv.Addr = bound
	s.logger.Info("rest.listen", slog.String("addr", bound))
	if err := s.srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Addr returns the bound address after Start has assigned it. Useful
// when ":0" was passed as listen. Returns the configured listen string
// before Start completes.
func (s *Server) Addr() string {
	if v, ok := s.addr.Load().(string); ok && v != "" {
		return v
	}
	return s.listen
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("rest.shutdown")
	return s.srv.Shutdown(ctx)
}
