// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/go-fabric/node"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// matterCommissioningOpenerAdapter serves the operator's
// open-commissioning-window request over the node facade's window. With
// the configured credentials it opens the node's own window
// (node.CommissioningWindow.OpenOwnWindow): the facade runs PASE
// against the configured passcode and publishes the commissionable
// record (CM=1) under the configured discriminator. In ephemeral-window
// mode it opens a basic window with credentials generated for this
// window alone (node.Node.OpenBasicWindow): the facade installs the
// verifier for the window's lifetime, advertises the window's
// discriminator, and restores the configured credentials when the window
// closes.
//
// The window is refused while the bridge serves no bridged endpoint,
// unless allowEmptyTopology: a controller that pairs an empty bridge
// renders it as "added but not supported", and the operator has to
// unpair before the first exposure takes effect.
type matterCommissioningOpenerAdapter struct {
	node          *node.Node
	commissioning config.NorthMatterCommissioning
	discriminator uint16
	// configured reports a valid operator-configured passcode; without
	// one the configured-credentials window is refused with
	// [node.ErrCommissioningWindowNotConfigured], as before the
	// facade (the node itself runs on credentials nobody holds, see
	// matterCommissioningCredentials).
	configured bool
	// blocked, when set, refuses every window: new commissioning is shut
	// (matterBridgeBundle.commissioningBlocked).
	blocked error
	// allowEmptyTopology lifts the bridged-endpoint check (tests of the
	// window itself).
	allowEmptyTopology bool

	// restUntil is when the window this adapter last opened expires. The
	// facade opens the node's own window at start (an uncommissioned
	// node) and lets a later own window replace it; the REST contract
	// answers 409 while a window the operator opened is running rather
	// than restarting it with fresh credentials under a pairing in
	// progress. An administrator's window (a commissioner's
	// OpenCommissioningWindow) is never replaced.
	mu        sync.Mutex
	restUntil time.Time
}

// windowBusy reports whether an open window must not be replaced: an
// administrator's, or one this adapter opened that has not expired.
func (a *matterCommissioningOpenerAdapter) windowBusy(window *node.CommissioningWindow) bool {
	if !window.IsOpen() {
		return false
	}
	if !window.IsOwnWindow() {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Now().Before(a.restUntil)
}

// opened records the window this adapter just opened.
func (a *matterCommissioningOpenerAdapter) opened(window *node.CommissioningWindow) {
	a.mu.Lock()
	a.restUntil = time.Now().Add(time.Duration(window.RequestedDurationSeconds()) * time.Second)
	a.mu.Unlock()
}

func (a *matterCommissioningOpenerAdapter) OpenCommissioningWindow(ctx context.Context, durationSeconds uint16) (handlers.MatterCommissioningWindowResult, error) {
	if a == nil || a.node == nil {
		return handlers.MatterCommissioningWindowResult{}, node.ErrCommissioningWindowNotConfigured
	}
	if a.blocked != nil {
		return handlers.MatterCommissioningWindowResult{}, fmt.Errorf("%w: %w", handlers.ErrCommissioningBlocked, a.blocked)
	}
	if !a.allowEmptyTopology && len(a.node.Topology().Devices) == 0 {
		slog.Default().Warn("matter.window_adapter.refused_open",
			slog.String("reason", "topology_no_bridged_endpoints"))
		return handlers.MatterCommissioningWindowResult{}, handlers.ErrBridgeTopologyNotReady
	}
	window := a.node.CommissioningWindow()
	if a.windowBusy(window) {
		return handlers.MatterCommissioningWindowResult{}, handlers.ErrCommissioningInProgress
	}
	timeout := time.Duration(durationSeconds) * time.Second

	if a.commissioning.EphemeralWindow {
		return a.openEphemeral(ctx, window, timeout, durationSeconds)
	}
	if !a.configured {
		return handlers.MatterCommissioningWindowResult{}, node.ErrCommissioningWindowNotConfigured
	}
	if err := window.OpenOwnWindow(ctx, timeout); err != nil {
		return handlers.MatterCommissioningWindowResult{}, mapWindowOpenError(err)
	}
	a.opened(window)
	qr, manual, err := a.node.PairingCodes()
	if err != nil {
		_ = window.RevokeWindow(ctx)
		return handlers.MatterCommissioningWindowResult{}, err
	}
	return handlers.MatterCommissioningWindowResult{
		Discriminator:   a.discriminator,
		Passcode:        a.commissioning.Passcode,
		DurationSeconds: window.RequestedDurationSeconds(),
		QRCode:          qr,
		ManualCode:      manual,
	}, nil
}

// openEphemeral opens the node's own window on credentials generated for
// this window: a fresh discriminator, passcode and salt
// (node.NewCommissioningCredentials), the configured PBKDF iteration
// count. The passcode never leaves this process except through the
// pairing codes returned to the operator.
func (a *matterCommissioningOpenerAdapter) openEphemeral(ctx context.Context, window *node.CommissioningWindow, timeout time.Duration, durationSeconds uint16) (handlers.MatterCommissioningWindowResult, error) {
	fresh, err := node.NewCommissioningCredentials()
	if err != nil {
		return handlers.MatterCommissioningWindowResult{}, err
	}
	if a.commissioning.Iterations > 0 {
		fresh.PBKDFIterations = a.commissioning.Iterations
	}
	creds := fresh.WindowCredentials(timeout)
	if err := a.node.OpenBasicWindow(ctx, creds); err != nil {
		return handlers.MatterCommissioningWindowResult{}, mapWindowOpenError(err)
	}
	a.opened(window)
	qr, manual, err := a.node.PairingCodesFor(creds)
	if err != nil {
		_ = window.RevokeWindow(ctx)
		return handlers.MatterCommissioningWindowResult{}, err
	}
	slog.Default().Info("matter.bridge.pase.ephemeral_window",
		slog.Int("iterations", fresh.PBKDFIterations),
		slog.Int("discriminator", int(creds.Discriminator)),
		slog.Int("duration_seconds", int(durationSeconds)))
	return handlers.MatterCommissioningWindowResult{
		Discriminator:   creds.Discriminator,
		Passcode:        creds.Passcode,
		DurationSeconds: window.RequestedDurationSeconds(),
		QRCode:          qr,
		ManualCode:      manual,
	}, nil
}

// mapWindowOpenError translates the facade's refusal of a second window
// into the REST contract's conflict.
func mapWindowOpenError(err error) error {
	if errors.Is(err, node.ErrAdministratorWindowOpen) || errors.Is(err, node.ErrCommissioningWindowAlreadyOpen) {
		return handlers.ErrCommissioningInProgress
	}
	return err
}
