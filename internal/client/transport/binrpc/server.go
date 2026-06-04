// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package binrpc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// DefaultServerIOTimeout caps read/write time for a single accepted
// connection; CUxD will always close after one request/response pair.
const DefaultServerIOTimeout = 15 * time.Second

// ServerConfig configures a [Server].
type ServerConfig struct {
	// Addr is the TCP listen address. Pass "host:0" for an
	// OS-assigned port; read the effective port via [Server.Addr].
	Addr string

	// Mux holds the registered method handlers. If nil, a fresh mux
	// is created and accessible via [Server.Mux].
	Mux *xmlrpc.Mux

	// Logger for structured events. If nil, [slog.Default].
	Logger *slog.Logger

	// IOTimeout bounds read/write on one accepted connection.
	// Zero = [DefaultServerIOTimeout].
	IOTimeout time.Duration
}

// Server is a BIN-RPC server. It accepts one request per connection
// (CUxD's convention) and dispatches through an [xmlrpc.Mux].
type Server struct {
	mux      *xmlrpc.Mux
	logger   *slog.Logger
	ioOut    time.Duration
	listener net.Listener
	wg       sync.WaitGroup
}

// NewServer constructs a Server bound to cfg.Addr. The listener is
// opened immediately so callers can query the effective port via Addr.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.Addr == "" {
		return nil, errors.New("binrpc: ServerConfig.Addr is required")
	}
	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("binrpc: listen %s: %w", cfg.Addr, err)
	}
	mux := cfg.Mux
	if mux == nil {
		mux = xmlrpc.NewMux()
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ioTO := cfg.IOTimeout
	if ioTO <= 0 {
		ioTO = DefaultServerIOTimeout
	}
	return &Server{
		mux:      mux,
		logger:   logger,
		ioOut:    ioTO,
		listener: ln,
	}, nil
}

// Mux returns the server's method mux. Register handlers on it at any
// time; Serve dispatches through the live map.
func (s *Server) Mux() *xmlrpc.Mux { return s.mux }

// Addr returns the listener's address (useful when a zero port was
// requested).
func (s *Server) Addr() net.Addr { return s.listener.Addr() }

// Serve accepts connections until ctx is cancelled or the listener
// returns a non-temporary error. It blocks the caller.
func (s *Server) Serve(ctx context.Context) error {
	// Close the listener when ctx is done so Accept unblocks.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = s.listener.Close()
		case <-stop:
		}
	}()

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				s.wg.Wait()
				return nil
			}
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				continue
			}
			s.wg.Wait()
			return fmt.Errorf("binrpc: accept: %w", err)
		}
		s.wg.Go(func() {
			s.handleConn(ctx, conn)
		})
	}
}

// Close stops accepting new connections and returns once in-flight
// handlers finish.
func (s *Server) Close() error {
	err := s.listener.Close()
	s.wg.Wait()
	return err
}

// handleConn reads exactly one request, dispatches it, and writes the
// response. Errors at any stage are logged and the connection is closed.
func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(s.ioOut))

	req, err := ReadRequest(io.LimitReader(conn, MaxMessageSize+8))
	if err != nil {
		s.logger.Debug(
			"binrpc server: decode request failed",
			slog.String("remote", conn.RemoteAddr().String()),
			slog.String("err", err.Error()),
		)
		return
	}

	result, err := s.mux.Dispatch(ctx, req.Method, req.Params)

	var buf bytes.Buffer
	if err != nil {
		fault := asFault(err)
		s.logger.Debug(
			"binrpc server: method returned fault",
			slog.String("method", req.Method),
			slog.Int("code", fault.Code),
			slog.String("message", fault.Message),
		)
		if err := WriteFault(&buf, fault); err != nil {
			s.logger.Error(
				"binrpc server: encode fault failed",
				slog.String("method", req.Method),
				slog.String("err", err.Error()),
			)
			return
		}
	} else {
		if result == nil {
			result = xmlrpc.NilValue{}
		}
		if err := WriteResponse(&buf, result); err != nil {
			s.logger.Error(
				"binrpc server: encode response failed",
				slog.String("method", req.Method),
				slog.String("err", err.Error()),
			)
			return
		}
	}

	if _, err := conn.Write(buf.Bytes()); err != nil {
		s.logger.Debug(
			"binrpc server: write response failed",
			slog.String("remote", conn.RemoteAddr().String()),
			slog.String("err", err.Error()),
		)
	}
}

// asFault collapses any error into an XMLRPCFault. Identical to the
// xmlrpc package's helper — duplicated here to avoid cross-package
// dependency creep for a four-line function.
func asFault(err error) *hmerr.XMLRPCFault {
	if fault, ok := errors.AsType[*hmerr.XMLRPCFault](err); ok {
		return fault
	}
	return &hmerr.XMLRPCFault{Code: -1, Message: err.Error()}
}
