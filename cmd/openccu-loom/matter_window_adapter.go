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

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	"github.com/SukramJ/go-fabric/node"
	mattersetup "github.com/SukramJ/go-fabric/secure/setup"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// matterCommissioningOpenerAdapter serves the operator's
// open-commissioning-window request over the node facade's window. With
// the configured credentials it opens the node's own window
// (bridge.CommissioningWindow.OpenOwnWindow): the facade runs PASE
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
	bridge        *matterbridge.Bridge
	commissioning config.NorthMatterCommissioning
	discriminator uint16
	vendorID      uint16
	productID     uint16
	// configured reports a valid operator-configured passcode; without
	// one the configured-credentials window is refused with
	// [matterbridge.ErrCommissioningWindowNotConfigured], as before the
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
func (a *matterCommissioningOpenerAdapter) windowBusy(window *matterbridge.CommissioningWindow) bool {
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
func (a *matterCommissioningOpenerAdapter) opened(window *matterbridge.CommissioningWindow) {
	a.mu.Lock()
	a.restUntil = time.Now().Add(time.Duration(window.RequestedDurationSeconds()) * time.Second)
	a.mu.Unlock()
}

func (a *matterCommissioningOpenerAdapter) OpenCommissioningWindow(ctx context.Context, durationSeconds uint16) (handlers.MatterCommissioningWindowResult, error) {
	if a == nil || a.node == nil {
		return handlers.MatterCommissioningWindowResult{}, matterbridge.ErrCommissioningWindowNotConfigured
	}
	if a.blocked != nil {
		return handlers.MatterCommissioningWindowResult{}, fmt.Errorf("%w: %w", handlers.ErrCommissioningBlocked, a.blocked)
	}
	if a.bridge != nil && !a.allowEmptyTopology {
		if topo := a.bridge.Topology(); topo != nil {
			bridged := 0
			for _, ep := range topo.Endpoints {
				if ep == nil || ep.IsRoot() || ep.IsAggregator() {
					continue
				}
				bridged++
			}
			if bridged == 0 {
				slog.Default().Warn("matter.window_adapter.refused_open",
					slog.String("reason", "topology_no_bridged_endpoints"),
					slog.Int("topology_endpoints", len(topo.Endpoints)))
				return handlers.MatterCommissioningWindowResult{}, handlers.ErrBridgeTopologyNotReady
			}
		}
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
		return handlers.MatterCommissioningWindowResult{}, matterbridge.ErrCommissioningWindowNotConfigured
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
// this window: a fresh discriminator, passcode and salt, the configured
// PBKDF iteration count. The passcode never leaves this process except
// through the pairing codes returned to the operator.
func (a *matterCommissioningOpenerAdapter) openEphemeral(ctx context.Context, window *matterbridge.CommissioningWindow, timeout time.Duration, durationSeconds uint16) (handlers.MatterCommissioningWindowResult, error) {
	disc, err := matterbridge.RandomDiscriminator()
	if err != nil {
		return handlers.MatterCommissioningWindowResult{}, err
	}
	pass, err := matterbridge.RandomPasscode()
	if err != nil {
		return handlers.MatterCommissioningWindowResult{}, err
	}
	salt, err := matterbridge.RandomSalt()
	if err != nil {
		return handlers.MatterCommissioningWindowResult{}, err
	}
	iterations := a.commissioning.Iterations
	if iterations <= 0 {
		iterations = 1000
	}
	creds := node.WindowCredentials{
		Discriminator: disc,
		Passcode:      pass,
		Iterations:    uint32(iterations), //nolint:gosec // bounded by the config validator
		Salt:          salt,
		Timeout:       timeout,
	}
	if err := a.node.OpenBasicWindow(ctx, creds); err != nil {
		return handlers.MatterCommissioningWindowResult{}, mapWindowOpenError(err)
	}
	a.opened(window)
	qr, err := mattersetup.QRCode(mattersetup.Payload{
		VendorID:      a.vendorID,
		ProductID:     a.productID,
		Discriminator: disc,
		Passcode:      pass,
		DiscoveryCaps: mattersetup.DiscoveryOnIP,
	})
	if err != nil {
		_ = window.RevokeWindow(ctx)
		return handlers.MatterCommissioningWindowResult{}, err
	}
	manual, err := mattersetup.ManualCode(disc, pass)
	if err != nil {
		_ = window.RevokeWindow(ctx)
		return handlers.MatterCommissioningWindowResult{}, err
	}
	slog.Default().Info("matter.bridge.pase.ephemeral_window",
		slog.Int("iterations", iterations),
		slog.Int("discriminator", int(disc)),
		slog.Int("duration_seconds", int(durationSeconds)))
	return handlers.MatterCommissioningWindowResult{
		Discriminator:   disc,
		Passcode:        pass,
		DurationSeconds: window.RequestedDurationSeconds(),
		QRCode:          qr,
		ManualCode:      manual,
	}, nil
}

// mapWindowOpenError translates the facade's refusal of a second window
// into the REST contract's conflict.
func mapWindowOpenError(err error) error {
	if errors.Is(err, matterbridge.ErrAdministratorWindowOpen) || errors.Is(err, matterbridge.ErrCommissioningWindowAlreadyOpen) {
		return handlers.ErrCommissioningInProgress
	}
	return err
}
