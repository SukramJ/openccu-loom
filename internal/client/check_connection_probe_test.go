// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package client_test

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// methodRecorder records the RPC method names a client puts on the wire.
type methodRecorder struct {
	mu      sync.Mutex
	methods []string
}

func (r *methodRecorder) call(_ context.Context, method string, _ []any) (any, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.methods = append(r.methods, method)
	return true, nil
}

func (r *methodRecorder) last() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.methods) == 0 {
		return ""
	}
	return r.methods[len(r.methods)-1]
}

func probeClient(t *testing.T, caps backends.Capabilities, rec *methodRecorder) *client.InterfaceClient {
	t.Helper()
	ic, err := client.New(client.Config{
		CentralName:  "test",
		Interface:    hmenum.InterfaceHmIPRF,
		Caller:       client.CallerFunc(rec.call),
		Capabilities: caps,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return ic
}

// TestCheckConnectionAvailabilityUsesPingWhenPingPongIsDeclared keeps the
// CCU/CUxD path on the ping RPC.
func TestCheckConnectionAvailabilityUsesPingWhenPingPongIsDeclared(t *testing.T) {
	t.Parallel()
	rec := &methodRecorder{}
	ic := probeClient(t, backends.Capabilities{RPCCallback: true, PingPong: true}, rec)

	if !ic.CheckConnectionAvailability(context.Background(), true) {
		t.Fatal("probe reported unavailable")
	}
	if got := rec.last(); got != "ping" {
		t.Fatalf("method = %q, want %q", got, "ping")
	}
}

// TestCheckConnectionAvailabilityAvoidsPingWithoutPingPong pins the
// Homegear path: a backend that declares no ping/pong is probed with
// clientServerInitialized, never with the ping RPC it does not implement.
func TestCheckConnectionAvailabilityAvoidsPingWithoutPingPong(t *testing.T) {
	t.Parallel()
	rec := &methodRecorder{}
	ic := probeClient(t, backends.Capabilities{RPCCallback: true, PingPong: false}, rec)

	if !ic.CheckConnectionAvailability(context.Background(), true) {
		t.Fatal("probe reported unavailable")
	}
	if got := rec.last(); got == "ping" {
		t.Fatalf("method = %q: a backend without ping/pong must not be sent the ping RPC", got)
	}
	if got := rec.last(); got != "clientServerInitialized" {
		t.Fatalf("method = %q, want %q", got, "clientServerInitialized")
	}
}
