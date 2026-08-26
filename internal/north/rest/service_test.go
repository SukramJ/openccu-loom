// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rest

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

// okHandler returns HTTP 200 for any request.
var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
})

// waitForAddr polls srv.Addr() until it reflects a real bound port (not ":0")
// or the deadline elapses. Returns the bound address or "".
func waitForAddr(srv *Server, d time.Duration) string {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if a := srv.Addr(); a != "" && a != ":0" {
			return a
		}
		time.Sleep(2 * time.Millisecond)
	}
	return ""
}

// TestServiceStartNonBlockingAndServes verifies that Start returns quickly and
// that the server actually accepts HTTP requests after Start returns.
func TestServiceStartNonBlockingAndServes(t *testing.T) {
	srv := NewServer(":0", okHandler, nil)
	svc := NewService(srv, nil)

	ctx := context.Background()

	started := make(chan error, 1)
	go func() { started <- svc.Start(ctx) }()

	select {
	case err := <-started:
		if err != nil {
			t.Fatalf("Start returned error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Start did not return within 1s (blocked)")
	}

	addr := waitForAddr(srv, 2*time.Second)
	if addr == "" {
		t.Fatal("server did not bind within 2s")
	}

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// TestServiceHealthyTransitions checks that Healthy reports the correct state
// before Start, after Start, and after Stop.
func TestServiceHealthyTransitions(t *testing.T) {
	srv := NewServer(":0", okHandler, nil)
	svc := NewService(srv, nil)

	// Before Start: not healthy.
	if ok, _ := svc.Healthy(); ok {
		t.Fatal("expected Healthy==false before Start")
	}

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// After Start: healthy.
	if ok, _ := svc.Healthy(); !ok {
		t.Fatal("expected Healthy==true after Start")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// After Stop: not healthy.
	if ok, _ := svc.Healthy(); ok {
		t.Fatal("expected Healthy==false after Stop")
	}
}

// TestServiceIdempotentStartStop verifies that Start is safe to call twice and
// that Stop is safe to call before Start or twice.
func TestServiceIdempotentStartStop(t *testing.T) {
	// Stop before Start must return nil.
	srv1 := NewServer(":0", okHandler, nil)
	svc1 := NewService(srv1, nil)
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc1.Stop(stopCtx); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}

	// Double Start must not error or double-bind.
	srv2 := NewServer(":0", okHandler, nil)
	svc2 := NewService(srv2, nil)
	ctx := context.Background()
	if err := svc2.Start(ctx); err != nil {
		t.Fatalf("first Start: %v", err)
	}
	if err := svc2.Start(ctx); err != nil {
		t.Fatalf("second Start (idempotent): %v", err)
	}

	stopCtx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	if err := svc2.Stop(stopCtx2); err != nil {
		t.Fatalf("Stop after double Start: %v", err)
	}

	// Double Stop must not error.
	stopCtx3, cancel3 := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel3()
	if err := svc2.Stop(stopCtx3); err != nil {
		t.Fatalf("second Stop (idempotent): %v", err)
	}
}

// TestServiceFastBindFailureSurfaces verifies that Start returns a non-nil
// error quickly when the port is already in use.
func TestServiceFastBindFailureSurfaces(t *testing.T) {
	// Grab a free port by listening on it, then keep the listener open.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer ln.Close()
	busyAddr := ln.Addr().String()

	srv := NewServer(busyAddr, okHandler, nil)
	svc := NewService(srv, nil)

	ctx := context.Background()
	started := make(chan error, 1)
	go func() { started <- svc.Start(ctx) }()

	select {
	case err := <-started:
		if err == nil {
			t.Fatal("expected non-nil error on duplicate bind, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Start did not surface bind error within 3s")
	}
}

// TestServiceName checks that Name returns the expected service identifier.
func TestServiceName(t *testing.T) {
	svc := NewService(NewServer(":0", okHandler, nil), nil)
	if got := svc.Name(); got != "rest" {
		t.Fatalf("Name()=%q, want %q", got, "rest")
	}
}
