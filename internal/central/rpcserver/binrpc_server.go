// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// BINRPCServer hosts the shared BIN-RPC TCP callback listener. Routing
// uses the first argument of every call — the interface_id — to find
// the responsible [Handlers]. Registrations are keyed by interface_id
// and are scoped to one central each.
type BINRPCServer struct {
	logger   *slog.Logger
	listener net.Listener

	mu     sync.RWMutex
	routes map[string]Handlers // interface_id → handlers

	// allowlist is an optional source-IP filter. When non-empty, only
	// connections whose remote IP falls within one of the prefixes are
	// dispatched; others are closed immediately with a debug log.
	// Empty/nil means accept all peers (default: open — preserves
	// existing behaviour on home-LAN deployments).
	allowlist []netip.Prefix

	ioTimeout   time.Duration
	wg          sync.WaitGroup
	activeTasks atomic.Int64 // count of in-flight handleConn goroutines

	// closeOnce serialises listener.Close across Serve's ctx-cancel
	// shutdown path and the explicit [Close] method. Without it both
	// paths race on the listener's internal teardown state under -race.
	closeOnce sync.Once
	// closed flips to true after closeOnce fires so Serve's accept loop
	// can refuse to launch new handler goroutines once shutdown started
	// — closing the wg.Add / wg.Wait race window for late-arriving
	// connections.
	closed atomic.Bool
}

// BINRPCConfig configures the server.
type BINRPCConfig struct {
	Addr      string
	IOTimeout time.Duration
	Logger    *slog.Logger

	// PortRange, when non-nil and the port in Addr is 0, overrides
	// dynamic-port selection with a deterministic scan of [Lo, Hi].
	// Set via [config.ParsePortRange] and [NewPortRange]. Ignored when
	// Addr specifies a fixed non-zero port.
	PortRange *PortRange

	// PeerAllowlist, when non-empty, restricts accepted TCP connections
	// to source IPs covered by one of the listed CIDR prefixes. A
	// connection from an unlisted peer is closed immediately before any
	// BIN-RPC data is read. Nil or empty means accept all peers (the
	// default, preserving the current open-LAN behaviour).
	PeerAllowlist []netip.Prefix
}

// NewBINRPCServer binds a listener.
//
// loom:reachable:reason="constructed in daemon.go WireDeps setup for the BIN-RPC callback listener used by CUxD interfaces"
func NewBINRPCServer(cfg BINRPCConfig) (*BINRPCServer, error) {
	if cfg.Addr == "" {
		return nil, errors.New("rpcserver: BINRPCConfig.Addr is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ln, err := bindAddr(cfg.Addr, cfg.PortRange)
	if err != nil {
		return nil, fmt.Errorf("rpcserver: binrpc listen %s: %w", cfg.Addr, err)
	}
	ioTimeout := cfg.IOTimeout
	if ioTimeout <= 0 {
		ioTimeout = 15 * time.Second
	}
	return &BINRPCServer{
		logger:    logger,
		listener:  ln,
		routes:    make(map[string]Handlers),
		ioTimeout: ioTimeout,
		allowlist: cfg.PeerAllowlist,
	}, nil
}

// Addr returns the effective listener address.
func (s *BINRPCServer) Addr() net.Addr { return s.listener.Addr() }

// ActiveTasksCount returns the number of BIN-RPC connection handlers
// currently executing. Useful for diagnostics and graceful drain checks.
func (s *BINRPCServer) ActiveTasksCount() int64 { return s.activeTasks.Load() }

// Register binds handlers for the given interface_id. CUxD callback
// requests carry the interface_id as the first argument.
func (s *BINRPCServer) Register(interfaceID string, h Handlers) {
	s.mu.Lock()
	s.routes[interfaceID] = h
	s.mu.Unlock()
}

// Deregister removes the registration.
func (s *BINRPCServer) Deregister(interfaceID string) {
	s.mu.Lock()
	delete(s.routes, interfaceID)
	s.mu.Unlock()
}

// Serve blocks until ctx is cancelled.
func (s *BINRPCServer) Serve(ctx context.Context) error {
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			s.closeListener()
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
			s.wg.Wait()
			return fmt.Errorf("rpcserver: accept: %w", err)
		}
		// Refuse late-arriving connections that race the shutdown path.
		// Without this guard wg.Add can fire after Close has already
		// entered wg.Wait, which is a documented race.
		if s.closed.Load() {
			_ = conn.Close()
			continue
		}
		s.wg.Add(1)
		s.activeTasks.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.activeTasks.Add(-1)
			s.handleConn(ctx, conn)
		}()
	}
}

// closeListener serialises listener.Close + the close-flag flip across
// the Serve ctx-cancel goroutine and the explicit [Close] method.
func (s *BINRPCServer) closeListener() {
	s.closeOnce.Do(func() {
		s.closed.Store(true)
		_ = s.listener.Close()
	})
}

// Close stops accepting new connections and waits for in-flight
// handlers to finish. Idempotent — repeated calls return the same
// error nil after the first shutdown completes.
func (s *BINRPCServer) Close() error {
	s.closeListener()
	s.wg.Wait()
	return nil
}

// peerAllowed reports whether the connection's remote address is
// permitted by the server's optional allowlist. When the allowlist is
// empty every peer is allowed.
func (s *BINRPCServer) peerAllowed(conn net.Conn) bool {
	if len(s.allowlist) == 0 {
		return true
	}
	remote := conn.RemoteAddr().String()
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	for _, prefix := range s.allowlist {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// handleConn reads exactly one request, dispatches it, writes one
// response.
func (s *BINRPCServer) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

	if !s.peerAllowed(conn) {
		s.logger.Debug("binrpc callback: peer not in allowlist, closing",
			slog.String("remote", conn.RemoteAddr().String()))
		return
	}

	_ = conn.SetDeadline(time.Now().Add(s.ioTimeout))

	req, err := binrpc.ReadRequest(io.LimitReader(conn, binrpc.MaxMessageSize+8))
	if err != nil {
		s.logger.Debug(
			"binrpc callback: decode request failed",
			slog.String("remote", conn.RemoteAddr().String()),
			slog.String("err", err.Error()),
		)
		return
	}

	result, dispatchErr := s.dispatch(ctx, req)

	var buf bytes.Buffer
	if dispatchErr != nil {
		fault := asFault(dispatchErr)
		s.logger.Debug(
			"binrpc callback: method returned fault",
			slog.String("method", req.Method),
			slog.Int("code", fault.Code),
			slog.String("message", fault.Message),
		)
		if err := binrpc.WriteFault(&buf, fault); err != nil {
			s.logger.Error("binrpc callback: encode fault failed",
				slog.String("method", req.Method), slog.String("err", err.Error()))
			return
		}
	} else {
		if result == nil {
			result = xmlrpc.NilValue{}
		}
		if err := binrpc.WriteResponse(&buf, result); err != nil {
			s.logger.Error("binrpc callback: encode response failed",
				slog.String("method", req.Method), slog.String("err", err.Error()))
			return
		}
	}

	if _, err := conn.Write(buf.Bytes()); err != nil {
		s.logger.Debug("binrpc callback: write response failed",
			slog.String("remote", conn.RemoteAddr().String()),
			slog.String("err", err.Error()))
	}
}

func (s *BINRPCServer) dispatch(ctx context.Context, req *binrpc.Request) (xmlrpc.Value, error) {
	if len(req.Params) == 0 {
		return nil, fmt.Errorf("binrpc %s: missing interface_id param", req.Method)
	}
	ifaceID, err := xmlrpc.AsString(req.Params[0])
	if err != nil {
		return nil, fmt.Errorf("binrpc %s: first arg must be interface_id: %w", req.Method, err)
	}

	s.mu.RLock()
	handlers := s.routes[ifaceID]
	s.mu.RUnlock()
	if handlers == nil {
		return nil, fmt.Errorf("binrpc %s: %w: %s", req.Method, ErrNoHandlers, ifaceID)
	}

	rest := req.Params[1:]
	switch req.Method {
	case "event":
		if len(rest) != 3 {
			return nil, fmt.Errorf("event: want 3 args after iface, got %d", len(rest))
		}
		addr, _ := xmlrpc.AsString(rest[0])
		param, _ := xmlrpc.AsString(rest[1])
		return xmlrpc.NilValue{}, handlers.Event(ctx, ifaceID, addr, param, rest[2])
	case "newDevices":
		if len(rest) != 1 {
			return nil, fmt.Errorf("newDevices: want 1 arg after iface, got %d", len(rest))
		}
		arr, err := xmlrpc.AsArray(rest[0])
		if err != nil {
			return nil, err
		}
		return xmlrpc.NilValue{}, handlers.NewDevices(ctx, ifaceID, arr)
	case "deleteDevices":
		if len(rest) != 1 {
			return nil, fmt.Errorf("deleteDevices: want 1 arg after iface, got %d", len(rest))
		}
		addrs, err := xmlrpc.AsStrings(rest[0])
		if err != nil {
			return nil, err
		}
		return xmlrpc.NilValue{}, handlers.DeleteDevices(ctx, ifaceID, addrs)
	case "listDevices":
		return handlers.ListDevices(ctx, ifaceID)
	case "error":
		// error(interface_id, error_code, msg). CUxD's BIN-RPC channel forwards
		// device-level wire failures here. Always return Nil so the CUxD connection
		// stays up regardless of what we did locally.
		if len(rest) != 2 {
			return nil, fmt.Errorf("error: want 2 args after iface, got %d", len(rest))
		}
		var code int
		if i, err := xmlrpc.AsInt(rest[0]); err == nil {
			code = i
		} else if s, err := xmlrpc.AsString(rest[0]); err == nil {
			_, _ = fmt.Sscanf(s, "%d", &code)
		}
		msg, _ := xmlrpc.AsString(rest[1])
		_ = handlers.Error(ctx, ifaceID, code, msg)
		return xmlrpc.NilValue{}, nil
	case "system.listMethods":
		return xmlrpc.ArrayValue{
			xmlrpc.StringValue("event"),
			xmlrpc.StringValue("newDevices"),
			xmlrpc.StringValue("deleteDevices"),
			xmlrpc.StringValue("listDevices"),
			xmlrpc.StringValue("error"),
			xmlrpc.StringValue("system.listMethods"),
		}, nil
	default:
		return nil, fmt.Errorf("binrpc: method not supported: %s", req.Method)
	}
}
