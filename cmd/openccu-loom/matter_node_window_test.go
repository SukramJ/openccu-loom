// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"

	"github.com/SukramJ/go-fabric-ui/fabricui"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
)

// windowBundle boots the bridge on the given commissioning settings and
// returns the bundle with the node page family's options wired the way
// wireMatterRuntime wires them, the empty topology allowed (the test
// registry carries no CCU devices).
func windowBundle(t *testing.T, central string, mutate func(*config.Config)) (*matterBridgeBundle, fabricui.Options) {
	t.Helper()
	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: central, Host: "127.0.0.1"}}
	if mutate != nil {
		mutate(cfg)
	}
	reg := buildTestRegistry(t, central)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	bundle := startMatterBridge(ctx, cfg, reg, openTestLoomDB(t), health.NewTracker(), nil, slog.New(slog.DiscardHandler), nil)
	if bundle == nil {
		t.Fatal("startMatterBridge returned nil")
	}
	t.Cleanup(bundle.stop)
	mcfg := cfg.North.Matter.WithDefaults()
	return bundle, fabricui.Options{
		Node:               bundle.node,
		Events:             bundle.events,
		ConfiguredWindow:   bundle.configuredCommissioning && !mcfg.Commissioning.EphemeralWindow,
		AllowEmptyTopology: true,
	}
}

type windowBody struct {
	Discriminator   uint16 `json:"discriminator"`
	Passcode        uint32 `json:"passcode"`
	DurationSeconds uint16 `json:"duration_seconds"`
	QRCode          string `json:"qr_code"`
	ManualCode      string `json:"manual_code"`
}

// TestOperatorWindowReplacesTheBootWindow: an uncommissioned node opens
// its own window at Start (the facade's semantics, adopted in ADR 0084
// phase 3); the operator's request must not be refused by that window —
// it is restarted with the requested duration, on the configured
// credentials, and the codes are the node's. A second request while the
// operator's window runs answers the contract's 409 rather than
// restarting the window under a pairing in progress; after the window
// closed a new one opens.
func TestOperatorWindowReplacesTheBootWindow(t *testing.T) {
	t.Parallel()
	bundle, opts := windowBundle(t, "ccu-boot-window", func(cfg *config.Config) {
		cfg.North.Matter.Commissioning.Passcode = 20202021
		cfg.North.Matter.Commissioning.Salt = "openccu-loom-dev0"
	})
	window := bundle.node.CommissioningWindow()
	if !window.IsOpen() || !window.IsOwnWindow() {
		t.Skipf("the node did not open its own window at Start (open=%v own=%v)", window.IsOpen(), window.IsOwnWindow())
	}
	api := newNodeAPI(t, opts)

	rec := serveNodeAPIOn(t, api, http.MethodPost, "/api/v1/node/pairing/window", `{"duration_seconds":180}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("open over the boot window = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var got windowBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	qr, manual, err := bundle.node.PairingCodes()
	if err != nil {
		t.Fatalf("PairingCodes: %v", err)
	}
	if got.DurationSeconds != 180 || got.QRCode != qr || got.ManualCode != manual {
		t.Fatalf("window = %+v, want 180 s on the node's codes (%s, %s)", got, qr, manual)
	}
	if !window.IsOpen() || !window.IsOwnWindow() || window.RequestedDurationSeconds() != 180 {
		t.Fatalf("after the open: open=%v own=%v duration=%d, want the node's own window for 180 s",
			window.IsOpen(), window.IsOwnWindow(), window.RequestedDurationSeconds())
	}

	if rec := serveNodeAPIOn(t, api, http.MethodPost, "/api/v1/node/pairing/window", `{"duration_seconds":180}`); rec.Code != http.StatusConflict {
		t.Fatalf("second open while the operator's window runs = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if rec := serveNodeAPIOn(t, api, http.MethodPost, "/api/v1/node/pairing/window/close", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("close = %d %s, want 204", rec.Code, rec.Body.String())
	}
	if rec := serveNodeAPIOn(t, api, http.MethodPost, "/api/v1/node/pairing/window", `{"duration_seconds":200}`); rec.Code != http.StatusOK {
		t.Fatalf("open after the window closed = %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// TestOperatorWindowWithoutAPasscodeRunsOnFreshCredentials: the node runs
// on a random passcode when none is configured
// (matterCommissioningCredentials) and its boot window on credentials
// nobody holds; the operator's window replaces it with credentials
// generated for that window, whose passcode the response carries.
func TestOperatorWindowWithoutAPasscodeRunsOnFreshCredentials(t *testing.T) {
	t.Parallel()
	bundle, opts := windowBundle(t, "ccu-fresh-window", nil)
	if bundle.configuredCommissioning || opts.ConfiguredWindow {
		t.Fatal("passcode 0 was reported as a configured passcode")
	}
	rec := serveNodeAPI(t, opts, http.MethodPost, "/api/v1/node/pairing/window", `{"duration_seconds":180}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("open = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var got windowBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Passcode == 0 || got.Discriminator == 0 || got.QRCode == "" || got.ManualCode == "" {
		t.Fatalf("window = %+v, want fresh credentials with their codes", got)
	}
	window := bundle.node.CommissioningWindow()
	if !window.IsOpen() {
		t.Fatal("the window is not open after the operator's open")
	}
}
