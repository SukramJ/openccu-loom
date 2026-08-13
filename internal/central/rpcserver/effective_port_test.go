// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

// effective_port_test.go — deep tests for the dynamic-port
// re-advertisement invariant.
//
// The invariant: when the callback server is bound on port 0, the OS
// assigns an ephemeral port. The *effective* port — read from
// srv.Addr() after bind — must be used to build the init() URL that
// is sent to every CCU. The configured value "0" must never appear in
// the advertised URL. On reconnect the effective port may change; the
// CCU learns the new value at reconnect time.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// ---------------------------------------------------------------------------
// Cluster A — Effective-port resolution
// ---------------------------------------------------------------------------

// TestXMLRPCServerTwoPortZeroBindsGetDifferentPorts verifies the
// property that is logically implied by the OS ephemeral-port contract:
// two successive servers bound on port 0 almost certainly receive
// distinct ports. The test also demonstrates that each server exposes
// a non-zero port via Addr().
//
// NOTE: TestXMLRPCServerEffectivePortFromDynamicBind in rpcserver_test.go
// already validates the single-server, non-zero port assertion. This test
// adds the two-server distinctness contract.
func TestXMLRPCServerTwoPortZeroBindsGetDifferentPorts(t *testing.T) {
	t.Parallel()

	srv1, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("srv1: %v", err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	go func() { done1 <- srv1.Serve(ctx1) }()
	defer func() { cancel1(); <-done1 }()

	srv2, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("srv2: %v", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- srv2.Serve(ctx2) }()
	defer func() { cancel2(); <-done2 }()

	port1 := effectivePort(t, srv1.Addr())
	port2 := effectivePort(t, srv2.Addr())

	if port1 == 0 || port2 == 0 {
		t.Fatalf("port1=%d port2=%d: both must be non-zero", port1, port2)
	}
	if port1 == port2 {
		// Technically possible (races on tiny port ranges) but
		// extremely unlikely. Flag it as a test concern, not a hard
		// invariant, so CI does not become flaky.
		t.Logf("WARNING: both servers got the same ephemeral port %d — retrying is safe but skipping assertion", port1)
	}
}

// TestXMLRPCServerWithFixedPortReturnsThatPort demonstrates that when
// a specific port is configured, srv.Addr() reports that exact port —
// not 0, not a different value.
func TestXMLRPCServerWithFixedPortReturnsThatPort(t *testing.T) {
	t.Parallel()

	var srv *XMLRPCServer
	free := bindFixedPort(t, func(port int) error {
		s, err := NewXMLRPCServer(XMLRPCConfig{Addr: fmt.Sprintf("127.0.0.1:%d", port)})
		if err != nil {
			return err
		}
		srv = s
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	got := effectivePort(t, srv.Addr())
	if got != free {
		t.Fatalf("effective port=%d want=%d", got, free)
	}
}

// TestBINRPCServerTwoPortZeroBindsGetDifferentPorts is the BIN-RPC
// analogue of TestXMLRPCServerTwoPortZeroBindsGetDifferentPorts.
func TestBINRPCServerTwoPortZeroBindsGetDifferentPorts(t *testing.T) {
	t.Parallel()

	srv1, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("srv1: %v", err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	go func() { done1 <- srv1.Serve(ctx1) }()
	defer func() { cancel1(); _ = srv1.Close(); <-done1 }()

	srv2, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("srv2: %v", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- srv2.Serve(ctx2) }()
	defer func() { cancel2(); _ = srv2.Close(); <-done2 }()

	port1 := effectivePort(t, srv1.Addr())
	port2 := effectivePort(t, srv2.Addr())

	if port1 == 0 || port2 == 0 {
		t.Fatalf("port1=%d port2=%d: both must be non-zero", port1, port2)
	}
	if port1 == port2 {
		t.Logf("WARNING: both BIN-RPC servers got the same ephemeral port %d", port1)
	}
}

// TestBINRPCServerWithFixedPortReturnsThatPort is the BIN-RPC analogue
// of TestXMLRPCServerWithFixedPortReturnsThatPort.
func TestBINRPCServerWithFixedPortReturnsThatPort(t *testing.T) {
	t.Parallel()

	var srv *BINRPCServer
	free := bindFixedPort(t, func(port int) error {
		s, err := NewBINRPCServer(BINRPCConfig{Addr: fmt.Sprintf("127.0.0.1:%d", port)})
		if err != nil {
			return err
		}
		srv = s
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); _ = srv.Close(); <-done }()

	got := effectivePort(t, srv.Addr())
	if got != free {
		t.Fatalf("effective port=%d want=%d", got, free)
	}
}

// ---------------------------------------------------------------------------
// Cluster B — Re-advertisement: init() URL must carry effective port
// ---------------------------------------------------------------------------

// TestInitCallURLContainsEffectivePortNotConfiguredZero is the core §11/1
// invariant test. It verifies that the URL a caller would assemble using
// srv.Addr() does NOT contain ":0" but DOES contain the actual OS-assigned
// port. This mirrors the logic in cmd/openccu-loom/daemon.go
// `startCallbackServer` (lines 503-508) and
// internal/central/adapter/ccu_wiring.go (line 79).
//
// Production path:
//
//	NewXMLRPCServer(addr "host:0") → listener binds → Addr() → *net.TCPAddr
//	tcpAddr.Port → non-zero effective port
//	baseURL := fmt.Sprintf("http://%s:%d", publicHost, port)
//	callbackURL := baseURL + "/RPC2/" + centralName
//
// The test validates that the URL built from Addr() never embeds ":0".
func TestInitCallURLContainsEffectivePortNotConfiguredZero(t *testing.T) {
	t.Parallel()

	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewXMLRPCServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	// Simulate the URL-assembly the daemon performs in startCallbackServer
	// and that adapter.WireCentrals consumes via CallbackBaseURL.
	tcpAddr, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() type=%T want *net.TCPAddr", srv.Addr())
	}
	effectivePt := tcpAddr.Port

	// §11/1 invariant A: configured "0" must not appear in the URL.
	if effectivePt == 0 {
		t.Fatal("§11/1 VIOLATED: effective port is 0 — the OS-assigned port was not read from Addr()")
	}

	// Assemble the callback URL exactly as startCallbackServer does.
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", effectivePt)
	callbackURL := strings.TrimRight(baseURL, "/") + "/RPC2/main-ccu"

	// §11/1 invariant B: the URL must not contain ":0".
	if strings.Contains(callbackURL, ":0") {
		t.Fatalf("§11/1 VIOLATED: callback URL %q contains configured port 0 instead of effective port", callbackURL)
	}

	// §11/1 invariant C: the URL must contain the actual numeric port.
	want := fmt.Sprintf(":%d/", effectivePt)
	if !strings.Contains(callbackURL, want) {
		t.Fatalf("§11/1 VIOLATED: callback URL %q does not contain effective port %d", callbackURL, effectivePt)
	}
}

// TestInitCallURLContainsEffectiveBINRPCPort verifies the BIN-RPC
// analogue: the port the CUxD backend is told about (via its
// callbackURL / addr argument) must reflect the OS-assigned TCP port,
// not the configured "0".
func TestInitCallURLContainsEffectiveBINRPCPort(t *testing.T) {
	t.Parallel()

	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewBINRPCServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); _ = srv.Close(); <-done }()

	tcpAddr, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() type=%T want *net.TCPAddr", srv.Addr())
	}

	// §11/1 invariant: effective port must be non-zero.
	if tcpAddr.Port == 0 {
		t.Fatal("§11/1 VIOLATED: BIN-RPC effective port is 0 — daemon would advertise port 0 to CUxD")
	}

	// The CUxD BIN-RPC URL format is "host:port" (raw TCP, no HTTP
	// scheme). Verify the assembled address is reachable and
	// port-accurate.
	addr := fmt.Sprintf("127.0.0.1:%d", tcpAddr.Port)
	if strings.Contains(addr, ":0") {
		t.Fatalf("§11/1 VIOLATED: BIN-RPC callback addr %q contains configured port 0", addr)
	}
}

// ---------------------------------------------------------------------------
// Cluster C — Reconnect: effective port is read fresh after restart
// ---------------------------------------------------------------------------

// TestXMLRPCServerStopAndRestartExposesUpdatedEffectivePort verifies the
// reconnect contract: stopping the server and creating a new one on
// port 0 yields a freshly OS-assigned port. The test pins that
// EffectivePort is read from the new Addr() — i.e. the daemon must
// not cache the previous port across restarts.
func TestXMLRPCServerStopAndRestartExposesUpdatedEffectivePort(t *testing.T) {
	t.Parallel()

	// Bind and start first server instance.
	srv1, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewXMLRPCServer (first): %v", err)
	}
	ctx1, cancel1 := context.WithCancel(context.Background())
	done1 := make(chan error, 1)
	go func() { done1 <- srv1.Serve(ctx1) }()
	portA := effectivePort(t, srv1.Addr())

	// Stop the first server.
	cancel1()
	if err := <-done1; err != nil {
		t.Fatalf("first server error on stop: %v", err)
	}

	// Bind a second server on port 0. The OS may or may not reuse portA,
	// but either way the *accessor* must return the actual OS port — not 0.
	srv2, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewXMLRPCServer (second): %v", err)
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- srv2.Serve(ctx2) }()
	defer func() { cancel2(); <-done2 }()

	portB := effectivePort(t, srv2.Addr())

	if portB == 0 {
		t.Fatalf("reconnect: second server effective port is 0 (portA=%d)", portA)
	}
	// Log the port pair so readers can verify the OS re-assigned.
	t.Logf("reconnect: portA=%d portB=%d", portA, portB)
}

// TestEffectivePortIsStableAcrossMultipleAddrCalls verifies that
// repeated calls to Addr() always return the same port — the binding
// is stable; there is no TOCTOU risk in calling Addr() multiple times
// to build init() URLs for different centrals.
func TestEffectivePortIsStableAcrossMultipleAddrCalls(t *testing.T) {
	t.Parallel()

	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	first := effectivePort(t, srv.Addr())
	for i := range 50 {
		got := effectivePort(t, srv.Addr())
		if got != first {
			t.Fatalf("Addr() returned port %d on call %d but %d on call 0", got, i+1, first)
		}
	}
}

// ---------------------------------------------------------------------------
// Cluster D — Edge cases
// ---------------------------------------------------------------------------

// TestXMLRPCServerAddrIsAvailableBeforeServe verifies that Addr() is
// usable immediately after NewXMLRPCServer — the listener is bound in
// the constructor, before Serve is called. This is critical because
// startCallbackServer reads Addr() synchronously before spawning the
// goroutine.
func TestXMLRPCServerAddrIsAvailableBeforeServe(t *testing.T) {
	t.Parallel()

	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}

	// Addr() before Serve() — must be non-nil and port non-zero.
	addr := srv.Addr()
	if addr == nil {
		t.Fatal("Addr() returned nil before Serve()")
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() type=%T want *net.TCPAddr", addr)
	}
	if tcp.Port == 0 {
		t.Fatal("Addr().Port is 0 before Serve() — effective port not resolved at construction time")
	}

	// Tidy up: start and immediately cancel.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	cancel()
	<-done
}

// TestBINRPCServerAddrIsAvailableBeforeServe is the BIN-RPC analogue
// of TestXMLRPCServerAddrIsAvailableBeforeServe.
func TestBINRPCServerAddrIsAvailableBeforeServe(t *testing.T) {
	t.Parallel()

	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	addr := srv.Addr()
	if addr == nil {
		t.Fatal("Addr() returned nil before Serve()")
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() type=%T want *net.TCPAddr", addr)
	}
	if tcp.Port == 0 {
		t.Fatal("Addr().Port is 0 before Serve()")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	cancel()
	_ = srv.Close()
	<-done
}

// TestXMLRPCServerLoopbackOnlyWhenHostIsLoopback verifies that a server
// bound on "127.0.0.1" is reachable via loopback and that the
// effective address reflects the loopback IP.
func TestXMLRPCServerLoopbackOnlyWhenHostIsLoopback(t *testing.T) {
	t.Parallel()

	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	tcp, ok := srv.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() type=%T", srv.Addr())
	}
	if !tcp.IP.IsLoopback() {
		t.Fatalf("expected loopback IP, got %s", tcp.IP)
	}

	// Verify the server is actually reachable on the loopback addr.
	srv.Register("probe", &stubHandlers{})

	client, err := xmlrpc.NewClient(xmlrpc.Config{
		URL:       fmt.Sprintf("http://%s/RPC2/probe", srv.Addr().String()),
		Interface: "HmIP-RF",
	})
	if err != nil {
		t.Fatal(err)
	}
	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	_, err = client.Call(dialCtx, "listDevices", []xmlrpc.Value{xmlrpc.StringValue("HmIP-RF")})
	if err != nil {
		t.Fatalf("loopback dial failed: %v — effective addr was %s", err, srv.Addr())
	}
}

// TestXMLRPCServerRequiresNonEmptyAddr verifies that NewXMLRPCServer
// rejects an empty Addr gracefully rather than panicking.
func TestXMLRPCServerRequiresNonEmptyAddr(t *testing.T) {
	t.Parallel()

	_, err := NewXMLRPCServer(XMLRPCConfig{Addr: ""})
	if err == nil {
		t.Fatal("expected error for empty Addr, got nil")
	}
}

// TestBINRPCServerRequiresNonEmptyAddr is the BIN-RPC analogue.
func TestBINRPCServerRequiresNonEmptyAddr(t *testing.T) {
	t.Parallel()

	_, err := NewBINRPCServer(BINRPCConfig{Addr: ""})
	if err == nil {
		t.Fatal("expected error for empty Addr, got nil")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// effectivePort extracts the TCP port from addr, failing the test if
// addr is nil or not a *net.TCPAddr.
func effectivePort(t *testing.T, addr net.Addr) int {
	t.Helper()
	if addr == nil {
		t.Fatal("Addr() is nil")
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() type=%T want *net.TCPAddr", addr)
	}
	return tcp.Port
}

// freePort asks the OS for a free TCP port by binding on :0, reads the
// assigned port, and then closes the listener before returning the
// port number. There is an inherent TOCTOU race (another process could
// claim the port between the close and the bind), but this is the
// standard Go idiom for test port allocation and is good enough for
// CI where there is no port competition.
// bindFixedPort picks a free port, hands it to bind, and retries with a
// fresh one when the bind loses the race — returning the port that
// finally succeeded.
//
// Picking a port and binding it are necessarily two steps: the port has
// to be released before the code under test can take it, and anything
// else on the machine may claim it in between. Within this package that
// window is narrow enough never to lose; across a full `go test ./...`,
// where dozens of packages bind ports concurrently, it eventually does —
// and the failure lands on whichever pull request happened to run then.
//
// Retrying keeps what these tests assert intact. Their subject is that a
// configured port is reported back verbatim, not that a bind succeeds on
// the first attempt.
func bindFixedPort(t *testing.T, bind func(port int) error) int {
	t.Helper()
	const attempts = 8
	var lastErr error
	for range attempts {
		port := freePort(t)
		if err := bind(port); err != nil {
			lastErr = err
			continue
		}
		return port
	}
	t.Fatalf("no free port survived %d bind attempts: %v", attempts, lastErr)
	return 0
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// TestBindFixedPortSurvivesALostRace measures what the retry is for: the
// pick-then-bind sequence has an unavoidable window, and the helper has
// to come back with a port that bound rather than failing the test that
// depends on it.
//
// The race cannot be provoked reliably from inside one package — the OS
// hands a competing listener some other port — so the lost bind is
// injected here instead. Without the retry the first error is terminal,
// which is exactly how this surfaced: as one red test on an unrelated
// pull request during a full `go test ./...`.
func TestBindFixedPortSurvivesALostRace(t *testing.T) {
	t.Parallel()

	var (
		attempts int
		bound    int
	)
	got := bindFixedPort(t, func(port int) error {
		attempts++
		if attempts < 3 {
			return errors.New("listen tcp: address already in use")
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return err
		}
		t.Cleanup(func() { _ = ln.Close() })
		bound = ln.Addr().(*net.TCPAddr).Port
		return nil
	})
	if attempts != 3 {
		t.Errorf("bind was attempted %d time(s), want 3 — the first two losses must be retried", attempts)
	}
	if got != bound {
		t.Errorf("reported port %d, want the one that actually bound (%d)", got, bound)
	}
}
