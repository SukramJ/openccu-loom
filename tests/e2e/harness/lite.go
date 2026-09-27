// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package harness

import (
	"context"
	"net"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

// Backend selects the south-bound system the harness daemon talks to.
type Backend int

const (
	// BackendCCU is a CCU simulated by godevccu (XML-RPC + JSON-RPC).
	BackendCCU Backend = iota
	// BackendOpenCCULite is an openccu-lite box simulated by litefake:
	// every call goes through the occulited proxy and events arrive on
	// its stream, with no callback server involved.
	BackendOpenCCULite
)

// String names the backend for subtest names.
func (b Backend) String() string {
	if b == BackendOpenCCULite {
		return "openccu-lite"
	}
	return "ccu"
}

// startLiteFake boots litefake with the harness fleet and registers a
// t.Cleanup that closes it.
func startLiteFake(t *testing.T, devices []string, notReady bool) *litefake.Fake {
	t.Helper()
	if len(devices) == 0 {
		devices = DefaultDevices
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f, err := litefake.Start(ctx, litefake.Options{Devices: devices, StartNotReady: notReady})
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// liteHostPort splits the fake's base URL into the host and the port the
// central addresses it by.
func liteHostPort(t *testing.T, f *litefake.Fake) (string, int) {
	t.Helper()
	u, err := url.Parse(f.URL())
	if err != nil {
		t.Fatalf("parse litefake URL: %v", err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatalf("split litefake host: %v", err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("parse litefake port: %v", err)
	}
	return host, port
}

// Lite returns the litefake box. Nil unless Options.Backend is
// BackendOpenCCULite.
func (h *Harness) Lite() *litefake.Fake { return h.lite }

// SetCCUReady flips the south-bound system between booting and ready,
// whichever backend the harness runs.
func (h *Harness) SetCCUReady(ready bool) {
	if h.lite != nil {
		h.lite.SetReady(ready)
		return
	}
	h.ccu.V().SetReady(ready)
}
