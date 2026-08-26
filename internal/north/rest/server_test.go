// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rest

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

// TestNewServerAndAddrBeforeStart verifies that NewServer can be constructed
// with a nil logger and that Addr returns the configured listen string before
// Start is called.
func TestNewServerAndAddrBeforeStart(t *testing.T) {
	srv := NewServer(":0", http.NotFoundHandler(), nil)
	// Before Start the Addr mirrors the listen argument that was passed in.
	if srv.Addr() != ":0" {
		t.Fatalf("Addr=%q", srv.Addr())
	}
}

// TestServerStartShutdown starts the server on a random port and verifies that
// it serves requests and shuts down cleanly.
func TestServerStartShutdown(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "pong")
	})
	srv := NewServer(":0", handler, nil)

	done := make(chan error, 1)
	go func() { done <- srv.Start() }()

	// Wait until the server is accepting connections.
	var addr string
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		addr = srv.Addr()
		if addr != ":0" && addr != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if addr == ":0" || addr == "" {
		t.Fatal("server did not bind within 2s")
	}

	// Issue a real HTTP request to the bound address.
	resp, err := http.Get("http://" + addr + "/ping")
	if err != nil {
		t.Fatalf("GET /ping: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}

	// Graceful shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Start returned non-nil after clean shutdown: %v", err)
	}
}

// TestServerAddrUpdatedAfterStart ensures Addr reflects the OS-assigned port
// (not ":0") once Start is running.
func TestServerAddrUpdatedAfterStart(t *testing.T) {
	srv := NewServer(":0", http.NotFoundHandler(), nil)

	go func() { _ = srv.Start() }()

	// Start() binds the listener and stores the resolved address before
	// Serve blocks, so poll Addr() until it reflects the OS-assigned port
	// rather than guessing the bind latency with fixed sleeps.
	deadline := time.Now().Add(2 * time.Second)
	var addr string
	for {
		addr = srv.Addr()
		if addr != "" && addr != ":0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Addr still %q after Start (2s)", addr)
		}
		time.Sleep(2 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
