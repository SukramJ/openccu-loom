// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// routing_test.go covers XML-RPC and BIN-RPC routing invariants:
// health endpoint shape, multi-CCU URL routing, legacy bare-root
// fallback, bare-root rejection when multiple centrals are registered,
// BIN-RPC system.listMethods method list, BIN-RPC server lifecycle,
// XML-RPC error callback dispatch, and Deregister central removal.

package rpcserver

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// ─── Test 1: Health endpoint shape ───────────────────────────────────────────

func TestXMLRPCServerHealthEndpointJSON(t *testing.T) {
	t.Parallel()
	_, url := newTestXMLRPCServer(t, "main", &stubHandlers{})
	base := strings.TrimSuffix(url, "/RPC2/main")

	resp, err := http.Get(base + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type=%q, want application/json", ct)
	}

	body, _ := io.ReadAll(resp.Body)
	var health map[string]any
	if err := json.Unmarshal(body, &health); err != nil {
		t.Fatalf("body not JSON: %v — body=%s", err, body)
	}
	for _, key := range []string{"status", "started", "centrals_count", "centrals", "request_count", "error_count"} {
		if _, ok := health[key]; !ok {
			t.Errorf("health JSON missing key %q", key)
		}
	}
	if health["status"] != "healthy" {
		t.Errorf("status=%q, want healthy", health["status"])
	}
}

// ─── Test 2: Multi-CCU routing — two centrals, one server ────────────────────

func TestXMLRPCServerRoutesMultipleCentralsIndependently(t *testing.T) {
	t.Parallel()
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}

	h1 := &stubHandlers{}
	h2 := &stubHandlers{}
	srv.Register("central-a", h1)
	srv.Register("central-b", h2)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	base := "http://" + srv.Addr().String()

	// Fire an event to central-a.
	clientA, _ := xmlrpc.NewClient(xmlrpc.Config{URL: base + "/RPC2/central-a"})
	_, err = clientA.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("A:1"),
		xmlrpc.StringValue("LEVEL"),
		xmlrpc.DoubleValue(0.25),
	})
	if err != nil {
		t.Fatalf("clientA event: %v", err)
	}

	// Fire an event to central-b.
	clientB, _ := xmlrpc.NewClient(xmlrpc.Config{URL: base + "/RPC2/central-b"})
	_, _ = clientB.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("B:1"),
		xmlrpc.StringValue("STATE"),
		xmlrpc.BoolValue(true),
	})

	if h1.events.Load() != 1 {
		t.Errorf("central-a received %d events, want 1", h1.events.Load())
	}
	if h2.events.Load() != 1 {
		t.Errorf("central-b received %d events, want 1", h2.events.Load())
	}
	if h1.events.Load()+h2.events.Load() != 2 {
		t.Errorf("events leaked across centrals")
	}
}

// ─── Test 3: Legacy bare-root routing (single central) ───────────────────────

func TestXMLRPCServerBareRootRoutesFallsBackToSingleCentral(t *testing.T) {
	t.Parallel()
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	h := &stubHandlers{}
	srv.Register("only-central", h)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	// Use the bare / path — no /RPC2/<name>.
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: "http://" + srv.Addr().String() + "/"})
	_, err = client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("X:1"),
		xmlrpc.StringValue("STATE"),
		xmlrpc.BoolValue(false),
	})
	if err != nil {
		t.Fatalf("bare-root event: %v", err)
	}
	if h.events.Load() != 1 {
		t.Fatalf("bare-root must route to single central, events=%d", h.events.Load())
	}
}

// ─── Test 4: Bare-root rejected when multiple centrals are registered ─────────

func TestXMLRPCServerBareRootRejectedForMultipleCentrals(t *testing.T) {
	t.Parallel()
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	srv.Register("c1", &stubHandlers{})
	srv.Register("c2", &stubHandlers{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: "http://" + srv.Addr().String() + "/"})
	_, err = client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("X:1"),
		xmlrpc.StringValue("STATE"),
		xmlrpc.IntValue(0),
	})
	if err == nil {
		t.Fatal("bare-root with multiple centrals must return error")
	}
}

// ─── Test 5: Deregister removes central ──────────────────────────────────────

func TestXMLRPCServerDeregisterRemovesCentral(t *testing.T) {
	t.Parallel()
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	h := &stubHandlers{}
	srv.Register("temp", h)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: "http://" + srv.Addr().String() + "/RPC2/temp"})

	// Before deregister — must work.
	_, err = client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("D:1"),
		xmlrpc.StringValue("STATE"),
		xmlrpc.BoolValue(true),
	})
	if err != nil {
		t.Fatalf("before deregister: %v", err)
	}

	srv.Deregister("temp")

	// After deregister — must fail.
	_, err = client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("D:1"),
		xmlrpc.StringValue("STATE"),
		xmlrpc.BoolValue(true),
	})
	if err == nil {
		t.Fatal("expected error after Deregister")
	}
}

// ─── Test 6: BIN-RPC system.listMethods returns expected methods ──────────────

func TestBINRPCServerSystemListMethodsReturnsExpectedMethods(t *testing.T) {
	t.Parallel()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	h := &stubHandlers{}
	srv.Register("CUxD", h)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	// Stop via ctx cancel, wait for Serve to return, THEN Close — calling
	// Close while the Serve goroutine is still unwinding races its wg.Wait.
	t.Cleanup(func() { cancel(); <-done; _ = srv.Close() })

	client, err := binrpc.NewClient(binrpc.Config{
		Addr:      srv.Addr().String(),
		Interface: "CUxD",
	})
	if err != nil {
		t.Fatal(err)
	}

	v, err := client.Call(context.Background(), "system.listMethods", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
	})
	if err != nil {
		t.Fatalf("system.listMethods: %v", err)
	}

	arr, err := xmlrpc.AsArray(v)
	if err != nil {
		t.Fatalf("result not array: %v", err)
	}
	names := make([]string, 0, len(arr))
	for _, item := range arr {
		s, _ := xmlrpc.AsString(item)
		names = append(names, s)
	}

	required := []string{"event", "newDevices", "deleteDevices", "listDevices", "system.listMethods"}
	for _, want := range required {
		found := slices.Contains(names, want)
		if !found {
			t.Errorf("system.listMethods missing %q, got %v", want, names)
		}
	}
}

// ─── Test 7: BIN-RPC server lifecycle: Start / Stop ──────────────────────────

func TestBINRPCServerLifecycleStartStop(t *testing.T) {
	t.Parallel()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}

	port := srv.Addr().(*net.TCPAddr).Port
	if port == 0 {
		t.Fatal("BIN-RPC server must bind a non-zero port")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	// Verify server is reachable before stop.
	conn, err := net.DialTimeout("tcp", srv.Addr().String(), 200*time.Millisecond)
	if err != nil {
		cancel()
		<-done
		t.Fatalf("server not reachable: %v", err)
	}
	_ = conn.Close()

	// Stop the server via ctx cancel — the production shutdown path (the
	// daemon serves on callbackCtx and cancels it on teardown; it never
	// calls Close on a live Serve). Wait for Serve to fully return BEFORE
	// calling Close: invoking Close() while the Serve goroutine is still
	// unwinding races its wg.Wait, which the race detector flags.
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for BIN-RPC server to stop")
	}
	// Close is idempotent and safe to call after Serve has returned.
	if err := srv.Close(); err != nil {
		t.Fatalf("Close after shutdown returned error: %v", err)
	}
}

// ─── Test 8: XML-RPC error callback is dispatched ─────────────────────────────

func TestXMLRPCServerErrorCallbackIsDispatched(t *testing.T) {
	t.Parallel()
	h := &errorCapturingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)

	client, err := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.Call(context.Background(), "error", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.IntValue(-7),
		xmlrpc.StringValue("device offline"),
	})
	if err != nil {
		t.Fatalf("error callback: %v", err)
	}

	if h.errorCode.Load() != -7 {
		t.Errorf("errorCode=%d, want -7", h.errorCode.Load())
	}
}

// errorCapturingHandlers is a Handlers stub that records the error callback.
type errorCapturingHandlers struct {
	stubHandlers
	errorCode atomicInt32
}

func (e *errorCapturingHandlers) Error(_ context.Context, _ string, errorCode int, _ string) error {
	e.errorCode.Store(int32(errorCode)) //nolint:gosec // G115: errorCode is an XML-RPC fault code; range matches int32 by protocol convention
	return nil
}

// atomicInt32 is a simple atomic int32 wrapper for this test.
type atomicInt32 struct {
	v int32
}

func (a *atomicInt32) Store(v int32) {
	// Use a simple mutex-free assignment — tests are not in a tight loop.
	a.v = v
}

func (a *atomicInt32) Load() int32 {
	return a.v
}

// ─── Test 9: BIN-RPC peer allowlist ──────────────────────────────────────────

// TestBINRPCServerPeerAllowlistRejectsUnlisted verifies that a connection
// from a peer whose IP is not covered by PeerAllowlist is closed before
// any BIN-RPC data is dispatched (the client sees a read error, not a
// fault response).
func TestBINRPCServerPeerAllowlistRejectsUnlisted(t *testing.T) {
	t.Parallel()
	// Allow only 192.0.2.0/24 (TEST-NET-1, RFC 5737 — no local host is
	// in this range, so any loopback connection will be rejected).
	allowlist := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	srv, err := NewBINRPCServer(BINRPCConfig{
		Addr:          "127.0.0.1:0",
		PeerAllowlist: allowlist,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = srv.Close() })

	// Raw TCP connection from 127.0.0.1 — not in 192.0.2.0/24.
	conn, err := net.DialTimeout("tcp", srv.Addr().String(), 500*time.Millisecond)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	// The server should close the conn immediately; reading should return EOF.
	_ = conn.SetDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 8)
	_, err = conn.Read(buf)
	if err == nil {
		t.Fatal("expected EOF/connection-close from server after allowlist rejection")
	}
}

// TestBINRPCServerPeerAllowlistAcceptsListedPeer verifies that a connection
// whose source IP IS in the allowlist proceeds normally.
func TestBINRPCServerPeerAllowlistAcceptsListedPeer(t *testing.T) {
	t.Parallel()
	// Allow 127.0.0.1/8 so the loopback address passes the filter.
	allowlist := []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	srv, err := NewBINRPCServer(BINRPCConfig{
		Addr:          "127.0.0.1:0",
		PeerAllowlist: allowlist,
	})
	if err != nil {
		t.Fatal(err)
	}
	h := &stubHandlers{}
	srv.Register("CUxD", h)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = srv.Close() })

	client, err := binrpc.NewClient(binrpc.Config{
		Addr:      srv.Addr().String(),
		Interface: "CUxD",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Call(context.Background(), "system.listMethods", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
	})
	if err != nil {
		t.Fatalf("expected listed peer to succeed, got: %v", err)
	}
}

// TestBINRPCServerEmptyAllowlistAcceptsAll verifies that the default
// (nil/empty PeerAllowlist) accepts all peers, preserving existing behaviour.
func TestBINRPCServerEmptyAllowlistAcceptsAll(t *testing.T) {
	t.Parallel()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"}) // no allowlist
	if err != nil {
		t.Fatal(err)
	}
	h := &stubHandlers{}
	srv.Register("CUxD", h)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = srv.Close() })

	client, err := binrpc.NewClient(binrpc.Config{
		Addr:      srv.Addr().String(),
		Interface: "CUxD",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Call(context.Background(), "system.listMethods", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
	})
	if err != nil {
		t.Fatalf("empty allowlist must accept all peers, got: %v", err)
	}
}
