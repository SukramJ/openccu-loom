// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	"github.com/SukramJ/go-fabric/im"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// TestMatterCommissioningWindowReachesTheAdminCommissioningCluster asserts
// the effect of wireCommissioningWindow rather than its presence: a window
// the daemon opens through its REST opener is what the
// AdministratorCommissioning cluster reports. The cluster only learns the
// window's state through the controller the wiring installs; without it
// WindowStatus reads Closed while a window is live, and every
// cluster-driven OpenCommissioningWindow answers BUSY — Matter multi-admin
// is dead while the REST surface looks healthy.
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
		ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil,
		slog.New(slog.DiscardHandler), nil,
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
	bridge, ok := wiring.reassembler.(*matterbridge.Bridge)
	if !ok || bridge == nil || wiring.opener == nil {
		t.Fatalf("matter runtime did not produce a bridge and an opener (reassembler=%T); without them this pin asserts nothing", wiring.reassembler)
	}

	const (
		rootEndpoint     uint16 = 0
		admCommCluster   uint32 = 0x003C
		windowStatusAttr uint32 = 0x0000
		// The daemon's opener hands the window a PAKE verifier, which is
		// an Enhanced Commissioning Method window (WindowStatus 1).
		windowStatusEnhanced = "1"
	)
	readStatus := func() string {
		results := bridge.Dispatcher().Read(ctx, im.ConcreteAttributePath{Endpoint: rootEndpoint, Cluster: admCommCluster, Attribute: windowStatusAttr, HasEndpoint: true, HasCluster: true, HasAttribute: true})
		if len(results) != 1 || !results[0].Status.IsSuccess() {
			t.Fatalf("AdministratorCommissioning.WindowStatus read = %+v; the root endpoint must serve it", results)
		}
		return fmt.Sprint(results[0].Value.Value)
	}

	if got := readStatus(); got != "0" {
		t.Fatalf("WindowStatus before any window = %s, want 0 (Closed)", got)
	}
	// The production opener refuses to advertise a bridge without bridged
	// endpoints; this daemon has no CCU devices, and the gate is not what
	// is under test here.
	opener, ok := wiring.opener.(*matterCommissioningOpenerAdapter)
	if !ok {
		t.Fatalf("opener is %T, want the daemon's window adapter", wiring.opener)
	}
	opener.allowEmptyTopology = true
	if _, err := opener.OpenCommissioningWindow(ctx, 180); err != nil {
		t.Fatalf("open commissioning window: %v", err)
	}
	if got := readStatus(); got != windowStatusEnhanced {
		t.Fatalf("WindowStatus with a live REST-opened window = %s, want %s (Enhanced) — "+
			"the AdministratorCommissioning cluster never received the window controller", got, windowStatusEnhanced)
	}
}
