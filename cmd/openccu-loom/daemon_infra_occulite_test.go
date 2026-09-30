// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestSharedInfrastructureWiresOcculiteSocketRevalidation pins the ws side
// of the ADR 0079 wiring through the production constructors: the handler
// wireSharedInfrastructure builds must carry the box-shell revalidator, so
// an occulite-authenticated upgrade produces a SECOND state call on the box
// — the resolver's verification plus the watch's immediate re-check. A
// handler built without the option leaves it at one, which is how this pin
// bites.
func TestSharedInfrastructureWiresOcculiteSocketRevalidation(t *testing.T) {
	liteAddonStampForTest(t)

	var stateCalls atomic.Int32
	box := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/v1/state" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") == "Bearer "+sidConfirmed {
			stateCalls.Add(1)
			_, _ = w.Write([]byte(`{"authenticated":true,"user":"alice","role":"admin"}`))
			return
		}
		_, _ = w.Write([]byte(`{"authenticated":false}`))
	}))
	t.Cleanup(box.Close)
	t.Setenv(liteAddonBaseURLEnv, box.URL)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cfg := &config.Config{DataDir: t.TempDir()}
	si, teardown := wireSharedInfrastructure(ctx, cfg, slog.New(slog.DiscardHandler), central.NewRegistry(), &reloadDeps{}, nil)
	t.Cleanup(teardown)

	passthrough := func(next http.Handler) http.Handler { return next }
	w := wireREST(context.Background(), restWiringDeps{
		cfg:            cfg,
		logger:         slog.New(slog.DiscardHandler),
		authMw:         auth.NewMiddleware(auth.NewMemoryUserStore(), auth.NewMemoryTokenStore(nil)),
		restResolve:    passthrough,
		sessionResolve: passthrough,
	})

	server := httptest.NewServer(w.authResolve(si.wsHandler))
	t.Cleanup(server.Close)

	wsURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	conn, err := net.Dial("tcp", wsURL.Host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("ws key: %v", err)
	}
	req := "GET /api/v1/ws/events HTTP/1.1\r\n" +
		"Host: " + wsURL.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		auth.OcculiteSessionHeader + ": " + sidConfirmed + "\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key) + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status = %d, want 101 (the SSO identity did not reach the upgrade)", resp.StatusCode)
	}

	// One call authenticated the upgrade; the second is the watch's
	// immediate re-check — the proof the production handler carries the
	// revalidator. Poll briefly: the watch runs on its own goroutine.
	deadline := time.Now().Add(5 * time.Second)
	for stateCalls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := stateCalls.Load(); got < 2 {
		t.Fatalf("state calls = %d, want >= 2: the ws handler is not wired with the occulite revalidator", got)
	}
}
