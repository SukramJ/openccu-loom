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
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
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

	// MaxConnections caps the number of simultaneously-accepted TCP
	// connections. Accept blocks once the cap is reached and resumes as
	// connections close. <= 0 means uncapped; the daemon supplies a
	// secure default from cfg.Callback.MaxConnections.
	MaxConnections int
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
	ln = limitListener(ln, cfg.MaxConnections)
	return newBINRPCServerOn(ln, logger, cfg.IOTimeout, cfg.PeerAllowlist), nil
}

// newBINRPCServerOn assembles the server around an already-bound
// listener. Split out of [NewBINRPCServer] so the accept loop can be
// exercised against a listener that fails on demand, through the same
// field initialisation the daemon uses.
func newBINRPCServerOn(ln net.Listener, logger *slog.Logger, ioTimeout time.Duration, allow []netip.Prefix) *BINRPCServer {
	if logger == nil {
		logger = slog.Default()
	}
	if ioTimeout <= 0 {
		ioTimeout = 15 * time.Second
	}
	return &BINRPCServer{
		logger:    logger,
		listener:  ln,
		routes:    make(map[string]Handlers),
		ioTimeout: ioTimeout,
		allowlist: allow,
	}
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

// Serve blocks until ctx is cancelled. Recoverable accept failures are
// retried with backoff (see [isRecoverableAcceptError]); only a failure
// that leaves the socket unusable ends the loop, and then the listener
// is closed rather than left bound with no acceptor.
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

	var retryDelay time.Duration
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				s.wg.Wait()
				return nil
			}
			// A recoverable failure says nothing about the listening
			// socket, so give it up and the CUxD push channel is dead
			// until the daemon restarts — nothing here restarts Serve.
			// Back off and keep accepting instead.
			if isRecoverableAcceptError(err) {
				retryDelay = nextAcceptRetryDelay(retryDelay)
				s.logger.Warn("binrpc callback: accept failed, retrying",
					slog.String("err", err.Error()),
					slog.Duration("retry_in", retryDelay))
				timer := time.NewTimer(retryDelay)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					s.closeListener()
					s.wg.Wait()
					return nil
				}
				continue
			}
			// Genuinely fatal: unbind rather than leave the port held by
			// a process that no longer accepts on it.
			s.closeListener()
			s.wg.Wait()
			return fmt.Errorf("rpcserver: accept: %w", err)
		}
		retryDelay = 0
		// Refuse late-arriving connections that race the shutdown path.
		// Without this guard wg.Add can fire after Close has already
		// entered wg.Wait, which is a documented race.
		if s.closed.Load() {
			_ = conn.Close()
			continue
		}
		// Enforce the source-IP allowlist before spawning a handler
		// goroutine so a disallowed peer costs nothing but an immediate
		// close (defence in depth on top of the connection cap).
		if !peerAllowed(s.allowlist, conn.RemoteAddr()) {
			s.logger.Debug("binrpc callback: peer not in allowlist, closing",
				slog.String("remote", conn.RemoteAddr().String()))
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

// handleConn reads exactly one request, dispatches it, writes one
// response. The peer allowlist is enforced earlier, in the [Serve]
// accept loop, so a disallowed peer never reaches this goroutine.
func (s *BINRPCServer) handleConn(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()

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
		// Warn, not Debug: every fault here is a callback the daemon
		// received and threw away. A push channel that silently drops
		// its payload looks identical to a quiet one, and that is how
		// the missing system.multicall case stayed hidden — the CUxD
		// events arrived, faulted, and left no trace above debug.
		s.logger.Warn(
			"binrpc callback: method returned fault",
			slog.String("method", req.Method),
			slog.String("remote", conn.RemoteAddr().String()),
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

// BINRPCSupportedMethods lists every callback method the BIN-RPC listener
// dispatches. It is what `system.listMethods` answers, and the contract is
// that each entry is genuinely routable — a peer that trusts the list and
// then gets a fault has been misled. `TestBINRPCListedMethodsAreDispatchable`
// holds the two sides together.
func BINRPCSupportedMethods() []string {
	return []string{
		"event",
		"newDevices",
		"deleteDevices",
		"listDevices",
		"error",
		"system.multicall",
		"system.listMethods",
	}
}

// binrpcSupportedMethods renders [BINRPCSupportedMethods] as a wire array.
func binrpcSupportedMethods() xmlrpc.ArrayValue {
	names := BINRPCSupportedMethods()
	out := make(xmlrpc.ArrayValue, 0, len(names))
	for _, n := range names {
		out = append(out, xmlrpc.StringValue(n))
	}
	return out
}

// dispatchMulticall unwraps a system.multicall envelope and runs each
// sub-call through [dispatch], returning one result per sub-call.
//
// CUxD batches its callbacks this way: even a single event arrives as
// `system.multicall([{methodName: "event", params: [interface_id, address,
// parameter, value]}])`, never as a bare `event` call. Without this case the
// envelope reaches the interface_id lookup below, where params[0] is an array
// rather than a string — so every CUxD push event was rejected as malformed.
//
// Per the XML-RPC multicall convention a successful sub-call contributes a
// one-element array holding its result, and a failed one contributes a fault
// struct, so that one broken sub-call cannot discard the whole batch. Mirrors
// the XML-RPC side in [xmlrpc.Mux] (internal/client/transport/xmlrpc/mux.go).
func (s *BINRPCServer) dispatchMulticall(ctx context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
	if len(params) != 1 {
		return nil, fmt.Errorf("binrpc system.multicall: expected 1 param, got %d", len(params))
	}
	calls, err := xmlrpc.AsArray(params[0])
	if err != nil {
		return nil, fmt.Errorf("binrpc system.multicall: %w", err)
	}
	results := make(xmlrpc.ArrayValue, 0, len(calls))
	for i, call := range calls {
		st, err := xmlrpc.AsStruct(call)
		if err != nil {
			return nil, fmt.Errorf("binrpc system.multicall call %d: %w", i, err)
		}
		var (
			method    string
			subParams []xmlrpc.Value
			haveName  bool
		)
		for _, m := range st.Members {
			switch m.Name {
			case "methodName":
				name, err := xmlrpc.AsString(m.Value)
				if err != nil {
					return nil, fmt.Errorf("binrpc system.multicall call %d: methodName: %w", i, err)
				}
				method, haveName = name, true
			case "params":
				arr, err := xmlrpc.AsArray(m.Value)
				if err != nil {
					return nil, fmt.Errorf("binrpc system.multicall call %d: params: %w", i, err)
				}
				subParams = arr
			}
		}
		if !haveName {
			return nil, fmt.Errorf("binrpc system.multicall call %d: missing methodName", i)
		}
		res, err := s.dispatch(ctx, &binrpc.Request{Method: method, Params: subParams})
		if err != nil {
			s.logger.Warn("binrpc callback: multicall sub-call failed",
				slog.String("method", method),
				slog.Int("index", i),
				slog.String("err", err.Error()))
			results = append(results, faultStruct(asFault(err)))
			continue
		}
		if res == nil {
			res = xmlrpc.NilValue{}
		}
		results = append(results, xmlrpc.ArrayValue{res})
	}
	return results, nil
}

// faultStruct renders a fault as the struct the multicall convention places
// in the result array in place of a successful sub-call's value.
func faultStruct(f *hmerr.XMLRPCFault) xmlrpc.StructValue {
	return xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "faultCode", Value: xmlrpc.IntValue(f.Code)}, //nolint:gosec // fault codes are small constants

		{Name: "faultString", Value: xmlrpc.StringValue(f.Message)},
	}}
}

func (s *BINRPCServer) dispatch(ctx context.Context, req *binrpc.Request) (xmlrpc.Value, error) {
	// Unwrap batched callbacks before the interface_id lookup — a
	// multicall envelope carries the id inside each sub-call, not in
	// params[0].
	if req.Method == "system.multicall" {
		return s.dispatchMulticall(ctx, req.Params)
	}
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
		return binrpcSupportedMethods(), nil
	default:
		return nil, fmt.Errorf("binrpc: method not supported: %s", req.Method)
	}
}
