// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// quietLogger keeps the callback server's bring-up chatter out of the
// test output.
func quietLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// TestStartCallbackServerBindsInsideTheConfiguredPortRange is the guard
// for a setting that was accepted, badged restart-required in the schema,
// and then ignored on every boot.
//
// The range only applied when callback.port was 0, but applyDefaults
// fills that field with 8120 on every load — and the DB-tier overlay runs
// ApplyDefaults a second time — so no installation could ever reach the
// branch. An operator behind a firewall that only opens 30000-30099 got
// port 8120 and a CCU whose callbacks never arrived.
func TestStartCallbackServerBindsInsideTheConfiguredPortRange(t *testing.T) {
	lo, hi := probeFreePortWindow(t)

	cfg := config.Default()
	cfg.Callback.Host = "127.0.0.1"
	// Exactly the situation every real installation is in: the default
	// port is set, because applyDefaults put it there.
	cfg.Callback.Port = 8120
	cfg.Callback.PortRange = fmt.Sprintf("%d-%d", lo, hi)

	// The listener is released when Serve observes the cancelled context,
	// so the test's own context is the teardown.
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, port, err := startCallbackServer(ctx, cfg, nil, nil, quietLogger())
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	if port < lo || port > hi {
		t.Fatalf("callback bound on port %d, outside the configured range %d-%d", port, lo, hi)
	}
	if port == cfg.Callback.Port {
		t.Fatalf("callback bound on the defaulted port %d instead of the configured range", port)
	}
}

// TestStartCallbackServerUsesTheFixedPortWhenNoRangeIsConfigured keeps
// the precedence from swinging the other way: without a range, the
// configured port still decides.
func TestStartCallbackServerUsesTheFixedPortWhenNoRangeIsConfigured(t *testing.T) {
	lo, _ := probeFreePortWindow(t)

	cfg := config.Default()
	cfg.Callback.Host = "127.0.0.1"
	cfg.Callback.Port = lo
	cfg.Callback.PortRange = ""

	// The listener is released when Serve observes the cancelled context,
	// so the test's own context is the teardown.
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	_, port, err := startCallbackServer(ctx, cfg, nil, nil, quietLogger())
	if err != nil {
		t.Fatalf("startCallbackServer: %v", err)
	}

	if port != lo {
		t.Fatalf("callback port: want the configured %d, got %d", lo, port)
	}
}

// TestStartCallbackServerRejectsAMalformedPortRange checks that a range
// this daemon cannot parse stops the bring-up rather than silently
// falling back to the default port.
func TestStartCallbackServerRejectsAMalformedPortRange(t *testing.T) {
	cfg := config.Default()
	cfg.Callback.Host = "127.0.0.1"
	cfg.Callback.PortRange = "not-a-range"

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	if _, _, err := startCallbackServer(ctx, cfg, nil, nil, quietLogger()); err == nil {
		t.Fatal("a malformed port_range must fail the callback bring-up")
	}
}

// probeFreePortWindow returns a [lo, hi] window whose lower bound is
// known to have been free a moment ago. The window is wide enough that
// the range scan finds a free port even if the probe port was taken in
// the meantime.
func probeFreePortWindow(t *testing.T) (lo, hi int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatalf("probe close: %v", err)
	}
	return port, port + 9
}
