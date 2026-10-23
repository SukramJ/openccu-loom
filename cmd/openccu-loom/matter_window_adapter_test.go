// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	"github.com/SukramJ/go-fabric/bridge/bridgetest"
	mattersetup "github.com/SukramJ/go-fabric/secure/setup"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

func TestOpenCommissioningWindow_NilAdapterIsNotConfigured(t *testing.T) {
	t.Parallel()
	var a *matterCommissioningOpenerAdapter
	if _, err := a.OpenCommissioningWindow(context.Background(), 180); !errors.Is(err, matterbridge.ErrCommissioningWindowNotConfigured) {
		t.Fatalf("nil adapter: err = %v, want ErrCommissioningWindowNotConfigured", err)
	}
	a = &matterCommissioningOpenerAdapter{}
	if _, err := a.OpenCommissioningWindow(context.Background(), 180); !errors.Is(err, matterbridge.ErrCommissioningWindowNotConfigured) {
		t.Fatalf("adapter without a node: err = %v, want ErrCommissioningWindowNotConfigured", err)
	}
}

// TestMapWindowOpenError: the facade's two "a window is open" refusals
// become the REST contract's conflict; anything else propagates.
func TestMapWindowOpenError(t *testing.T) {
	t.Parallel()
	for _, err := range []error{matterbridge.ErrAdministratorWindowOpen, matterbridge.ErrCommissioningWindowAlreadyOpen} {
		if got := mapWindowOpenError(err); !errors.Is(got, handlers.ErrCommissioningInProgress) {
			t.Errorf("mapWindowOpenError(%v) = %v, want ErrCommissioningInProgress", err, got)
		}
	}
	other := errors.New("something else")
	if got := mapWindowOpenError(other); !errors.Is(got, other) {
		t.Errorf("mapWindowOpenError(other) = %v, want the error itself", got)
	}
}

// openerBundle boots the bridge on the given commissioning settings and
// returns the bundle with the REST opener wired the way wireMatterRuntime
// wires it.
func openerBundle(t *testing.T, central string, mutate func(*config.Config)) (*matterBridgeBundle, *matterCommissioningOpenerAdapter) {
	t.Helper()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	// Loopback, as the other pairing tests listen: the ephemeral pin
	// dials bridge.LocalAddr(), and a wildcard listener's address is not
	// dialable on every CI runner (macOS times out).
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: central, Host: "127.0.0.1"}}
	if mutate != nil {
		mutate(cfg)
	}
	reg := buildTestRegistry(t, central)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bundle := startMatterBridge(ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("startMatterBridge returned nil")
	}
	t.Cleanup(bundle.stop)
	mcfg := cfg.North.Matter.WithDefaults()
	return bundle, &matterCommissioningOpenerAdapter{
		node:               bundle.node,
		commissioning:      mcfg.Commissioning,
		discriminator:      mcfg.Discriminator,
		configured:         bundle.configuredCommissioning,
		allowEmptyTopology: true, // the test registry carries no CCU devices
	}
}

// TestOpenerOpensTheNodesOwnWindowWithTheConfiguredCredentials: the REST
// window is the facade's own window on the configured passcode and
// discriminator — the codes the operator gets are the node's pairing
// codes, the window is open and the node's own, and a second request
// while it runs answers the REST contract's conflict rather than
// restarting the window under a pairing in progress.
func TestOpenerOpensTheNodesOwnWindowWithTheConfiguredCredentials(t *testing.T) {
	t.Parallel()
	bundle, opener := openerBundle(t, "ccu-own", func(cfg *config.Config) {
		cfg.North.Matter.Commissioning.Passcode = 20202021
		cfg.North.Matter.Commissioning.Salt = "openccu-loom-dev0"
	})
	if !bundle.configuredCommissioning {
		t.Fatal("a valid configured passcode was not recognised as configured")
	}
	ctx := context.Background()
	res, err := opener.OpenCommissioningWindow(ctx, 180)
	if err != nil {
		t.Fatalf("OpenCommissioningWindow: %v", err)
	}
	qr, manual, err := bundle.node.PairingCodes()
	if err != nil {
		t.Fatalf("PairingCodes: %v", err)
	}
	if res.Passcode != 20202021 || res.Discriminator != 0xF00 || res.QRCode != qr || res.ManualCode != manual {
		t.Fatalf("result = %+v, want the configured passcode / discriminator and the node's codes (%s, %s)", res, qr, manual)
	}
	window := bundle.node.CommissioningWindow()
	if !window.IsOpen() || !window.IsOwnWindow() {
		t.Fatalf("after the REST open: open=%v own=%v, want the node's own window open", window.IsOpen(), window.IsOwnWindow())
	}
	if res.DurationSeconds != 180 {
		t.Errorf("DurationSeconds = %d, want the requested 180", res.DurationSeconds)
	}
	if _, err := opener.OpenCommissioningWindow(ctx, 180); !errors.Is(err, handlers.ErrCommissioningInProgress) {
		t.Fatalf("second open while the REST window runs: err = %v, want ErrCommissioningInProgress", err)
	}
	if err := window.RevokeWindow(ctx); err != nil {
		t.Fatalf("RevokeWindow: %v", err)
	}
	if _, err := opener.OpenCommissioningWindow(ctx, 60); err != nil {
		t.Fatalf("open after the window closed: %v", err)
	}
}

// TestOpenerReplacesTheBootWindow: an uncommissioned node opens its own
// window at Start (the facade's semantics, adopted in ADR 0084 phase 3);
// the operator's request must not be refused by that window — it is
// restarted with the requested duration.
func TestOpenerReplacesTheBootWindow(t *testing.T) {
	t.Parallel()
	bundle, opener := openerBundle(t, "ccu-boot", func(cfg *config.Config) {
		cfg.North.Matter.Commissioning.Passcode = 20202021
		cfg.North.Matter.Commissioning.Salt = "openccu-loom-dev0"
	})
	window := bundle.node.CommissioningWindow()
	if !window.IsOpen() || !window.IsOwnWindow() {
		t.Skipf("the node did not open its own window at Start (open=%v own=%v)", window.IsOpen(), window.IsOwnWindow())
	}
	res, err := opener.OpenCommissioningWindow(context.Background(), 120)
	if err != nil {
		t.Fatalf("OpenCommissioningWindow over the boot window: %v", err)
	}
	if res.DurationSeconds != 120 {
		t.Errorf("DurationSeconds = %d, want the requested 120 (the boot window's timeout otherwise)", res.DurationSeconds)
	}
}

// TestOpenerWithoutAConfiguredPasscodeRefusesTheConfiguredWindow: the node
// runs on a random passcode when none is configured
// (matterCommissioningCredentials), but the operator cannot be handed a
// window on credentials nobody holds.
func TestOpenerWithoutAConfiguredPasscodeRefusesTheConfiguredWindow(t *testing.T) {
	t.Parallel()
	bundle, opener := openerBundle(t, "ccu-nopass", nil)
	if bundle.configuredCommissioning {
		t.Fatal("passcode 0 was reported as a configured passcode")
	}
	if _, err := opener.OpenCommissioningWindow(context.Background(), 180); !errors.Is(err, matterbridge.ErrCommissioningWindowNotConfigured) {
		t.Fatalf("err = %v, want ErrCommissioningWindowNotConfigured", err)
	}
}

// TestOpenerEphemeralWindowRunsOnFreshCredentials: in ephemeral-window
// mode the REST window is the facade's basic window on credentials
// generated for it — no configured passcode needed — and the window
// advertises the generated discriminator.
func TestOpenerEphemeralWindowRunsOnFreshCredentials(t *testing.T) {
	t.Parallel()
	bundle, opener := openerBundle(t, "ccu-ephem", func(cfg *config.Config) {
		cfg.North.Matter.Commissioning.EphemeralWindow = true
	})
	res, err := opener.OpenCommissioningWindow(context.Background(), 180)
	if err != nil {
		t.Fatalf("OpenCommissioningWindow (ephemeral): %v", err)
	}
	if !mattersetup.IsValidSetupPIN(res.Passcode) {
		t.Fatalf("ephemeral passcode %d is not a valid setup PIN", res.Passcode)
	}
	if res.QRCode == "" || res.ManualCode == "" {
		t.Fatalf("ephemeral window returned empty pairing codes: %+v", res)
	}
	window := bundle.node.CommissioningWindow()
	if !window.IsOpen() || !window.IsOwnWindow() {
		t.Fatalf("after the ephemeral open: open=%v own=%v, want the node's own window open", window.IsOpen(), window.IsOwnWindow())
	}
	// The effect: a commissioner pairs with the credentials the operator
	// was told — the facade installed the verifier of the generated
	// passcode for this window.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmr := bridgetest.Dial(t, bundle.node.Bridge().LocalAddr(), bridgetest.Options{
		Passcode:      res.Passcode,
		Discriminator: res.Discriminator,
		FabricID:      0x0FAB,
		NodeID:        0x0D0D,
		Seed:          1,
	})
	if _, err := cmr.Commission(ctx); err != nil {
		t.Fatalf("Commission with the ephemeral credentials: %v", err)
	}
}

func TestOpenerRefusesAnEmptyTopology(t *testing.T) {
	t.Parallel()
	_, opener := openerBundle(t, "ccu-empty", func(cfg *config.Config) {
		cfg.North.Matter.Commissioning.Passcode = 20202021
		cfg.North.Matter.Commissioning.Salt = "openccu-loom-dev0"
	})
	opener.allowEmptyTopology = false
	if _, err := opener.OpenCommissioningWindow(context.Background(), 180); !errors.Is(err, handlers.ErrBridgeTopologyNotReady) {
		t.Fatalf("err = %v, want ErrBridgeTopologyNotReady", err)
	}
}
