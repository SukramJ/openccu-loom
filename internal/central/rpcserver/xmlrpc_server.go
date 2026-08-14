// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// XMLRPCServer hosts the shared XML-RPC HTTP callback listener. One
// listener per daemon — multiple centrals register themselves by name
// and receive callbacks on `/RPC2/<central_name>`.
//
// A GET /health route is also served and mirrors
// health endpoint (`central/rpc_server.py:697-714`).
type XMLRPCServer struct {
	logger *slog.Logger

	mu       sync.RWMutex
	routes   map[string]Handlers // central_name → handlers
	listener net.Listener
	srv      *http.Server
	shutdown time.Duration

	// counters for the /health endpoint — incremented atomically.
	requestCount atomic.Int64
	errorCount   atomic.Int64
	started      atomic.Bool
}

// XMLRPCConfig configures the server.
type XMLRPCConfig struct {
	// Addr to bind. ":8120" is the daemon default; use "host:0" for
	// an OS-assigned port in tests.
	Addr string

	// PortRange, when non-nil and Port is 0 (i.e. Addr ends in ":0"),
	// overrides dynamic-port selection with a deterministic scan of
	// [Lo, Hi]. Set via [config.ParsePortRange]. Ignored when Addr
	// specifies a fixed non-zero port.
	PortRange *PortRange

	// ShutdownTimeout bounds graceful shutdown.
	ShutdownTimeout time.Duration

	// Logger for slog events. Defaults to slog.Default().
	Logger *slog.Logger

	// PeerAllowlist, when non-empty, restricts accepted TCP connections
	// to source IPs covered by one of the listed CIDR prefixes. A
	// connection from an unlisted peer is closed at Accept time, before
	// the HTTP server reads it. Nil or empty means accept all peers (the
	// default, preserving the current open-LAN behaviour).
	PeerAllowlist []netip.Prefix

	// MaxConnections caps the number of simultaneously-accepted TCP
	// connections. Accept blocks once the cap is reached and resumes as
	// connections close. <= 0 means uncapped; the daemon supplies a
	// secure default from cfg.Callback.MaxConnections.
	MaxConnections int
}

// NewXMLRPCServer binds a listener immediately so callers can read
// [Addr] before starting.
func NewXMLRPCServer(cfg XMLRPCConfig) (*XMLRPCServer, error) {
	if cfg.Addr == "" {
		return nil, errors.New("rpcserver: XMLRPCConfig.Addr is required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	ln, err := bindAddr(cfg.Addr, cfg.PortRange)
	if err != nil {
		return nil, fmt.Errorf("rpcserver: xmlrpc listen %s: %w", cfg.Addr, err)
	}
	// Reject disallowed peers first, then cap concurrency: the limit
	// listener wraps the filter so rejected peers never consume a slot.
	ln = newPeerFilterListener(ln, cfg.PeerAllowlist, logger)
	ln = limitListener(ln, cfg.MaxConnections)
	s := &XMLRPCServer{
		logger:   logger,
		routes:   make(map[string]Handlers),
		listener: ln,
		shutdown: cfg.ShutdownTimeout,
	}
	if s.shutdown <= 0 {
		s.shutdown = 5 * time.Second
	}
	// Finite timeouts so a slow or idle peer cannot pin a callback connection
	// indefinitely. ReadHeaderTimeout alone bounds only the header phase; a
	// slow request body or an idle keep-alive would otherwise hold the socket
	// forever. Values are generous relative to LAN event callbacks.
	s.srv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return s, nil
}

// Addr returns the effective listener address (interesting when a zero
// port was requested).
func (s *XMLRPCServer) Addr() net.Addr { return s.listener.Addr() }

// NoCentralAssigned reports whether the server currently has no centrals
// registered. When true the server can safely be stopped without losing
// any pending callbacks — no central will lose its callback endpoint.
// Useful as a diagnostic guard before calling Deregister on the last central.
func (s *XMLRPCServer) NoCentralAssigned() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.routes) == 0
}

// Register binds the given handlers under centralName. Overwrites a
// previous registration for the same name.
func (s *XMLRPCServer) Register(centralName string, h Handlers) {
	s.mu.Lock()
	s.routes[centralName] = h
	s.mu.Unlock()
}

// Deregister removes the registration.
func (s *XMLRPCServer) Deregister(centralName string) {
	s.mu.Lock()
	delete(s.routes, centralName)
	s.mu.Unlock()
}

// Serve blocks until ctx is cancelled.
func (s *XMLRPCServer) Serve(ctx context.Context) error {
	s.started.Store(true)
	defer s.started.Store(false)
	errCh := make(chan error, 1)
	go func() {
		err := s.srv.Serve(s.listener)
		if errors.Is(err, http.ErrServerClosed) {
			errCh <- nil
		} else {
			errCh <- err
		}
	}()
	select {
	case <-ctx.Done():
		//nolint:contextcheck // shutdown path must not inherit the cancelled serve ctx
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdown)
		defer cancel()
		//nolint:contextcheck // shutdown path: Shutdown runs on the fresh timeout ctx, not the cancelled serve ctx
		_ = s.srv.Shutdown(shutdownCtx)
		return <-errCh
	case err := <-errCh:
		return err
	}
}

// ServeHTTP implements http.Handler. Recognises four path shapes:
//
// - `GET /health` — liveness probe. - `/RPC2/<central_name>` — explicit
// per-central routing (preferred, used by every modern firmware revision). -
// `/` or empty — legacy compatibility shape: older CCU firmware issues
// callbacks on the bare root path. We fall back to the single registered
// central when there is exactly one; with multiple centrals, the bare-root
// request is rejected because there is no way to disambiguate the target. -
// Anything else — 404.
func (s *XMLRPCServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Health probe — GET /health only, no central routing needed.
	if r.URL.Path == "/health" && r.Method == http.MethodGet {
		s.serveHealth(w)
		return
	}

	s.requestCount.Add(1)
	centralName, ok := s.resolveCentralForPath(r.URL.Path)
	if !ok {
		s.errorCount.Add(1)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.mu.RLock()
	handlers := s.routes[centralName]
	s.mu.RUnlock()
	if handlers == nil {
		s.errorCount.Add(1)
		http.Error(w, "unknown central", http.StatusNotFound)
		return
	}

	// Build a scoped xmlrpc.Handler that dispatches through handlers.
	h := xmlrpc.NewHandler()
	h.Logger = s.logger
	bindXMLRPCMethods(h.Mux, handlers)
	h.ServeHTTP(w, r)
}

// serveHealth responds to GET /health with a JSON body that.
//
// Example response:
//
//	{"status":"healthy","started":true,"centrals_count":1,
//	 "centrals":["ccu-01"],"request_count":42,"error_count":0,
//	 "listen_address":"[::]:8120"}
func (s *XMLRPCServer) serveHealth(w http.ResponseWriter) {
	s.mu.RLock()
	centrals := make([]string, 0, len(s.routes))
	for name := range s.routes {
		centrals = append(centrals, name)
	}
	s.mu.RUnlock()

	started := s.started.Load()
	status := "healthy"
	if !started {
		status = "stopped"
	}
	body := map[string]any{
		"status":         status,
		"started":        started,
		"centrals_count": len(centrals),
		"centrals":       centrals,
		"request_count":  s.requestCount.Load(),
		"error_count":    s.errorCount.Load(),
		"listen_address": s.listener.Addr().String(),
	}
	data, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_, _ = w.Write(data)
}

// resolveCentralForPath maps an incoming request path to the central
// name to dispatch through. Returns (name, true) on a routable path,
// ("", false) otherwise.
func (s *XMLRPCServer) resolveCentralForPath(p string) (string, bool) {
	if name, ok := routeFromPath(p); ok {
		return name, true
	}
	// Legacy fallback: bare-root callback. Only meaningful when
	// exactly one central is registered.
	if p == "/" || p == "" {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if len(s.routes) == 1 {
			for name := range s.routes {
				return name, true
			}
		}
	}
	return "", false
}

// routeFromPath extracts `<central_name>` from `/RPC2/<central_name>`.
// Returns ("", false) for any path shape that is not exactly one
// segment after the `/RPC2/` prefix or that contains characters outside
// the [hmtypes.IsValidCentralName] allowlist. This is the gate that keeps
// path-traversal (`/RPC2/..`, `/RPC2/%2e%2e`) and encoded-slash
// segments (`/RPC2/ccu1%2fother`) from misrouting between centrals.
func routeFromPath(p string) (string, bool) {
	const prefix = "/RPC2/"
	if !strings.HasPrefix(p, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(p, prefix)
	if rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	// The allowlist is shared with every boundary that accepts a central
	// name ([hmtypes.ValidateCentralName]), so the segment announced in the
	// callback URL and the segment matched here cannot drift apart.
	if !hmtypes.IsValidCentralName(rest) {
		return "", false
	}
	return rest, true
}

// bindXMLRPCMethods wires the seven CCU callback methods into mux.
// Every method extracts its positional arguments from the XML-RPC
// params array and delegates to the central's [Handlers].
func bindXMLRPCMethods(mux *xmlrpc.Mux, h Handlers) { //nolint:gocognit,funlen // wire/dispatch table over many attribute/opcode cases
	mux.Handle("event", func(ctx context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		if len(params) != 4 {
			return nil, fmt.Errorf("event: want 4 params, got %d", len(params))
		}
		iface, err := xmlrpc.AsString(params[0])
		if err != nil {
			return nil, err
		}
		addr, err := xmlrpc.AsString(params[1])
		if err != nil {
			return nil, err
		}
		param, err := xmlrpc.AsString(params[2])
		if err != nil {
			return nil, err
		}
		if err := h.Event(ctx, iface, addr, param, params[3]); err != nil {
			return nil, err
		}
		return xmlrpc.NilValue{}, nil
	})

	mux.Handle("newDevices", func(ctx context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		if len(params) != 2 {
			return nil, fmt.Errorf("newDevices: want 2 params, got %d", len(params))
		}
		iface, err := xmlrpc.AsString(params[0])
		if err != nil {
			return nil, err
		}
		descs, err := xmlrpc.AsArray(params[1])
		if err != nil {
			return nil, err
		}
		if err := h.NewDevices(ctx, iface, descs); err != nil {
			return nil, err
		}
		return xmlrpc.NilValue{}, nil
	})

	mux.Handle("deleteDevices", func(ctx context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		if len(params) != 2 {
			return nil, fmt.Errorf("deleteDevices: want 2 params, got %d", len(params))
		}
		iface, err := xmlrpc.AsString(params[0])
		if err != nil {
			return nil, err
		}
		addrs, err := xmlrpc.AsStrings(params[1])
		if err != nil {
			return nil, err
		}
		if err := h.DeleteDevices(ctx, iface, addrs); err != nil {
			return nil, err
		}
		return xmlrpc.NilValue{}, nil
	})

	mux.Handle("updateDevice", func(ctx context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		if len(params) != 3 {
			return nil, fmt.Errorf("updateDevice: want 3 params, got %d", len(params))
		}
		iface, _ := xmlrpc.AsString(params[0])
		addr, _ := xmlrpc.AsString(params[1])
		hint, err := xmlrpc.AsInt(params[2])
		if err != nil {
			return nil, err
		}
		if err := h.UpdateDevice(ctx, iface, addr, hint); err != nil {
			return nil, err
		}
		return xmlrpc.NilValue{}, nil
	})

	mux.Handle("replaceDevice", func(ctx context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		if len(params) != 3 {
			return nil, fmt.Errorf("replaceDevice: want 3 params, got %d", len(params))
		}
		iface, _ := xmlrpc.AsString(params[0])
		oldAddr, _ := xmlrpc.AsString(params[1])
		newAddr, _ := xmlrpc.AsString(params[2])
		if err := h.ReplaceDevice(ctx, iface, oldAddr, newAddr); err != nil {
			return nil, err
		}
		return xmlrpc.NilValue{}, nil
	})

	mux.Handle("readdedDevice", func(ctx context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		if len(params) != 2 {
			return nil, fmt.Errorf("readdedDevice: want 2 params, got %d", len(params))
		}
		iface, _ := xmlrpc.AsString(params[0])
		addrs, err := xmlrpc.AsStrings(params[1])
		if err != nil {
			return nil, err
		}
		if err := h.ReaddedDevice(ctx, iface, addrs); err != nil {
			return nil, err
		}
		return xmlrpc.NilValue{}, nil
	})

	// error(interface_id, error_code, msg) — CCU notifies us of a wire- level
	// failure. The CCU does not expect a response payload; we always return Nil
	// so the connection stays up even when our local handler fails.
	mux.Handle("error", func(ctx context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		if len(params) != 3 {
			return nil, fmt.Errorf("error: want 3 params, got %d", len(params))
		}
		iface, _ := xmlrpc.AsString(params[0])
		// error_code arrives as either an integer or a stringified
		// integer depending on the CCU firmware. Try int first; fall
		// back to string-parse.
		var code int
		if i, err := xmlrpc.AsInt(params[1]); err == nil {
			code = i
		} else if s, err := xmlrpc.AsString(params[1]); err == nil {
			_, _ = fmt.Sscanf(s, "%d", &code)
		}
		msg, _ := xmlrpc.AsString(params[2])
		_ = h.Error(ctx, iface, code, msg)
		return xmlrpc.NilValue{}, nil
	})

	mux.Handle("listDevices", func(ctx context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		if len(params) != 1 {
			return nil, fmt.Errorf("listDevices: want 1 param, got %d", len(params))
		}
		iface, _ := xmlrpc.AsString(params[0])
		arr, err := h.ListDevices(ctx, iface)
		if err != nil {
			return nil, err
		}
		return arr, nil
	})
	mux.RegisterSystemMethods()
}
