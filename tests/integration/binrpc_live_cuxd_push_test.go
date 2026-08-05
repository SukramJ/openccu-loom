// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build cuxd_live_push

// Package integration — live proof that CUxD push events reach the
// production BIN-RPC callback server.
//
// # Why this exists separately
//
// The read-only live smoke in binrpc_live_cuxd_test.go deliberately never
// calls init(), so it exercises only the direction the daemon initiates. The
// defect it therefore could not see: CUxD wraps every callback in a
// `system.multicall` envelope, which the callback server had no case for, so
// no CUxD event had ever been delivered. A codec round-trip cannot catch that
// — only registering as a real callback target can.
//
// # This test WRITES to the CCU
//
// It calls `init(url, id)` on CUxD, which registers this process as a
// callback target, and deregisters afterwards. It touches no device: no
// setValue, no putParamset. The only stimulus is `ping`, whose PONG is the
// event being asserted.
//
// Separate build tag and separate env var from the read-only smoke, so the
// registering variant can never run by accident.
//
// # Running
//
//	OPENCCU_LOOM_LIVE_CUXD_ADDR=<host>:8701 \
//	OPENCCU_LOOM_LIVE_CUXD_CALLBACK_HOST=<this-machine-ip> \
//	    go test -tags=cuxd_live_push -timeout=90s \
//	      ./tests/integration/... -run TestCuxdLivePush -v
//
// The callback host must be an address the CCU can reach back on; it cannot
// be derived reliably from inside the process, so it is explicit.
package integration

import (
	"context"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// liveCuxdPushEnv returns the CUxD address and the reachable callback host,
// skipping unless both are set.
func liveCuxdPushEnv(t *testing.T) (cuxdAddr, callbackHost string) {
	t.Helper()
	cuxdAddr = os.Getenv("OPENCCU_LOOM_LIVE_CUXD_ADDR")
	callbackHost = os.Getenv("OPENCCU_LOOM_LIVE_CUXD_CALLBACK_HOST")
	if cuxdAddr == "" || callbackHost == "" {
		t.Skip("set OPENCCU_LOOM_LIVE_CUXD_ADDR and OPENCCU_LOOM_LIVE_CUXD_CALLBACK_HOST " +
			"to enable the registering live-CUxD push test (it writes a callback registration)")
	}
	return cuxdAddr, callbackHost
}

// capturingHandlers records the events the production dispatch delivered.
type capturingHandlers struct {
	mu     sync.Mutex
	events [][]string
}

func (h *capturingHandlers) Event(_ context.Context, iface, addr, param string, v xmlrpc.Value) error {
	val, _ := xmlrpc.AsString(v)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, []string{iface, addr, param, val})
	return nil
}

func (h *capturingHandlers) NewDevices(context.Context, string, xmlrpc.ArrayValue) error { return nil }

func (h *capturingHandlers) DeleteDevices(context.Context, string, []string) error { return nil }

func (h *capturingHandlers) UpdateDevice(context.Context, string, string, int) error { return nil }

func (h *capturingHandlers) ReplaceDevice(context.Context, string, string, string) error { return nil }

func (h *capturingHandlers) ReaddedDevice(context.Context, string, []string) error { return nil }

func (h *capturingHandlers) Error(context.Context, string, int, string) error { return nil }

func (h *capturingHandlers) ListDevices(context.Context, string) (xmlrpc.ArrayValue, error) {
	return xmlrpc.ArrayValue{}, nil
}

func (h *capturingHandlers) snapshot() [][]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([][]string, len(h.events))
	copy(out, h.events)
	return out
}

// TestCuxdLivePushReachesCallbackServer registers the production BIN-RPC
// callback server with a live CUxD, triggers a PONG, and asserts the event
// arrived. This is the end-to-end proof for the system.multicall fix: before
// it, CUxD delivered this exact callback and the server rejected it as
// malformed, so nothing reached the handler.
func TestCuxdLivePushReachesCallbackServer(t *testing.T) {
	cuxdAddr, callbackHost := liveCuxdPushEnv(t)

	srv, err := rpcserver.NewBINRPCServer(rpcserver.BINRPCConfig{Addr: "0.0.0.0:0"})
	if err != nil {
		t.Fatalf("NewBINRPCServer: %v", err)
	}
	handlers := &capturingHandlers{}
	interfaceID := "loom-livetest-CUxD"
	srv.Register(interfaceID, handlers)

	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-served; _ = srv.Close() })

	port := srv.Addr().(*net.TCPAddr).Port
	callbackURL := "xmlrpc_bin://" + net.JoinHostPort(callbackHost, itoa(port))

	client, err := binrpc.NewClient(binrpc.Config{Addr: cuxdAddr, Interface: interfaceID})
	if err != nil {
		t.Fatalf("binrpc.NewClient: %v", err)
	}

	// Register. The deferred deregistration uses the URL-only shape — the
	// only form CUxD honours; see the announcer wire-shape guards.
	if _, err := client.Call(context.Background(), "init", []xmlrpc.Value{
		xmlrpc.StringValue(callbackURL), xmlrpc.StringValue(interfaceID),
	}); err != nil {
		t.Fatalf("init: %v", err)
	}
	t.Cleanup(func() {
		if _, err := client.Call(context.Background(), "init",
			[]xmlrpc.Value{xmlrpc.StringValue(callbackURL)}); err != nil {
			t.Errorf("deregistration failed — a stale callback registration may remain on CUxD: %v", err)
		}
	})

	// Ping is the stimulus: CUxD answers with a PONG event on the CENTRAL
	// pseudo-address, wrapped in system.multicall.
	token := interfaceID + "#livepush"
	if _, err := client.Call(context.Background(), "ping",
		[]xmlrpc.Value{xmlrpc.StringValue(token)}); err != nil {
		t.Fatalf("ping: %v", err)
	}

	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range handlers.snapshot() {
			if e[1] == "CENTRAL" && e[2] == "PONG" {
				if e[0] != interfaceID {
					t.Errorf("PONG arrived under interface_id %q, want %q — "+
						"the callback route is keyed on this value", e[0], interfaceID)
				}
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("no PONG reached the callback server within 20s; delivered events: %v",
		handlers.snapshot())
}

// itoa avoids pulling strconv in for one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
