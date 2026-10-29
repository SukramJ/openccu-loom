// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"testing"

	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/node"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestMatterCommissioningWindowReachesTheAdminCommissioningCluster asserts
// the effect of the window wiring rather than its presence: the root
// endpoint serves AdministratorCommissioning, and a window the daemon
// opens through the node page family's route is the node facade's window — open, the
// node's own, and what the status surface reports. The facade keeps the
// AdministratorCommissioning attributes for administrator windows alone,
// as matter.js does: the node's own window reads WindowNotOpen there,
// before and after the operator's open.
func TestMatterCommissioningWindowReachesTheAdminCommissioningCluster(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.North.Matter.Commissioning.Passcode = 20202021
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-window", Host: "127.0.0.1"}}

	reg := buildTestRegistry(t, "ccu-window")
	ctx, cancel := context.WithCancel(context.Background())

	wiring, closers, teardown := wireMatterRuntime(
		ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil, nil, nil,
		slog.New(slog.DiscardHandler), nil, nil,
	)
	// Cancel here rather than in a t.Cleanup of its own. Cleanups run LIFO, and
	// openTestLoomDB registers its close-and-remove between this call and the
	// cancel: a separately registered cancel would therefore fire *after* the
	// database directory was already being removed, leaving ctx-bound
	// goroutines writing into it and failing the test with
	// "TempDir RemoveAll cleanup: directory not empty".
	t.Cleanup(func() {
		cancel()
		for _, c := range closers {
			c()
		}
		if teardown != nil {
			teardown()
		}
	})
	n, ok := wiring.nodeUI.Node.(*node.Node)
	if !ok || n == nil {
		t.Fatalf("matter runtime did not hand the node page family a node (%T); without it this pin asserts nothing", wiring.nodeUI.Node)
	}
	bridge := n.Bridge()

	const (
		rootEndpoint     uint16 = 0
		admCommCluster   uint32 = 0x003C
		windowStatusAttr uint32 = 0x0000
		windowNotOpen           = "0"
	)
	readStatus := func() string {
		results := bridge.Dispatcher().Read(ctx, im.ConcreteAttributePath{Endpoint: rootEndpoint, Cluster: admCommCluster, Attribute: windowStatusAttr, HasEndpoint: true, HasCluster: true, HasAttribute: true})
		if len(results) != 1 || !results[0].Status.IsSuccess() {
			t.Fatalf("AdministratorCommissioning.WindowStatus read = %+v; the root endpoint must serve it", results)
		}
		return fmt.Sprint(results[0].Value.Value)
	}

	if got := readStatus(); got != windowNotOpen {
		t.Fatalf("WindowStatus at boot = %s, want %s (the node's own window is not an administrator's)", got, windowNotOpen)
	}
	opts := wiring.nodeUI
	opts.AllowEmptyTopology = true
	if rec := serveNodeAPI(t, opts, http.MethodPost, "/api/v1/node/pairing/window", `{"duration_seconds":180}`); rec.Code != http.StatusOK {
		t.Fatalf("open commissioning window: %d %s", rec.Code, rec.Body.String())
	}
	window := bridge.CommissioningWindow()
	if window == nil || !window.IsOpen() || !window.IsOwnWindow() {
		t.Fatalf("after the REST open the facade's window is open=%v own=%v; want the node's own window open — "+
			"the REST opener never reached the window the AdministratorCommissioning cluster is wired to",
			window != nil && window.IsOpen(), window != nil && window.IsOwnWindow())
	}
	if got := window.RequestedDurationSeconds(); got != 180 {
		t.Fatalf("the window runs for %d s, want the requested 180", got)
	}
	if status := wiring.status.MatterStatus(ctx); !status.WindowOpen || status.WindowDuration != 180 {
		t.Fatalf("status = open %v / %d s, want open / 180 — the status surface reads another window than the opener opens",
			status.WindowOpen, status.WindowDuration)
	}
	if got := readStatus(); got != windowNotOpen {
		t.Fatalf("WindowStatus with the node's own window open = %s, want %s", got, windowNotOpen)
	}
}
