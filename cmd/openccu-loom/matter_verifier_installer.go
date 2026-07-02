// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/operational"
)

// matterVerifierInstaller implements [matterbridge.PaseVerifierInstaller].
// When a Matter commissioner opens an Enhanced Commissioning Window via the
// AdministratorCommissioning.OpenCommissioningWindow cluster command, it
// supplies the PAKE passcode verifier (w0 || L) it derived from a passcode
// it chose. This installer builds a PASE acceptor from that verifier and
// swaps it onto the bridge for the window's lifetime, returning a restore
// closure that re-arms the configured "between-windows" acceptor on close.
//
// It mirrors [matterEphemeralProvider] (which handles the operator-driven
// REST open flow) but takes the verifier as input instead of generating a
// random passcode. Wiring it directly onto the [matterbridge.CommissioningWindow]
// covers the multi-admin cluster path, which does NOT go through the
// REST opener/ephemeral provider.
//
// Two operating modes, matching the ephemeral provider:
//   - Singleton (default): the verifier adapter replaces the bridge's
//     long-lived adapter via AttachPaseHandler; Restore re-installs
//     `configured` (nil → noop).
//   - Per-Exchange (`concurrent_pairings=true`, configuredFactory != nil):
//     a fresh PerExchangePaseProvider is installed whose factory mints a
//     verifier-backed adapter per exchange; Restore rebuilds the configured
//     per-exchange provider.
type matterVerifierInstaller struct {
	bridge            *matterbridge.Bridge
	opMgr             *operational.Manager
	opCreds           *mattercore.OperationalCredentials
	logger            *slog.Logger
	configured        *matterbridge.PaseAdapter        // long-lived acceptor restored on close (singleton mode)
	configuredFactory func() *matterbridge.PaseAdapter // per-exchange factory restored on close (concurrent mode); nil in singleton mode

	// mu serialises InstallVerifier against its own Restore closure so a
	// window open racing a previous close observes a consistent bridge
	// PASE handler.
	mu sync.Mutex
}

// newMatterVerifierInstaller returns an installer ready to be wired onto a
// [matterbridge.CommissioningWindow] via SetPaseVerifierInstaller. `configured`
// / `configuredFactory` describe the acceptor to restore when the window
// closes (both nil → no PASE handler between windows, the safe default).
func newMatterVerifierInstaller(
	bridge *matterbridge.Bridge,
	opMgr *operational.Manager,
	opCreds *mattercore.OperationalCredentials,
	configured *matterbridge.PaseAdapter,
	configuredFactory func() *matterbridge.PaseAdapter,
	logger *slog.Logger,
) *matterVerifierInstaller {
	return &matterVerifierInstaller{
		bridge:            bridge,
		opMgr:             opMgr,
		opCreds:           opCreds,
		configured:        configured,
		configuredFactory: configuredFactory,
		logger:            logger,
	}
}

// InstallVerifier implements [matterbridge.PaseVerifierInstaller].
func (p *matterVerifierInstaller) InstallVerifier(verifier []byte, iterations uint32, salt []byte) (func(), error) {
	if p == nil || p.bridge == nil || p.opMgr == nil {
		return nil, errors.New("verifier installer: not wired")
	}
	iters := int(iterations)
	if iters == 0 {
		iters = 1000
	}

	concurrent := p.configuredFactory != nil
	mode := "singleton"
	if concurrent {
		mode = "per-exchange"
	}

	p.mu.Lock()
	if concurrent {
		// Per-exchange: install a provider whose factory mints a fresh
		// verifier-backed adapter for every Pake1 arrival within the window.
		vp := matterbridge.NewPerExchangePaseProvider(func() *matterbridge.PaseAdapter {
			a, err := buildPaseAdapterFromVerifier(verifier, salt, iters, p.opMgr, p.opCreds, p.logger)
			if err != nil {
				p.logger.Warn("matter.bridge.pase.verifier_factory", slog.String("err", err.Error()))
				return nil
			}
			return a
		})
		vp.StartReaper(context.Background(), 30*time.Second, 60*time.Second)
		p.bridge.AttachPaseHandlerProvider(vp.Resolve)
	} else {
		adapter, err := buildPaseAdapterFromVerifier(verifier, salt, iters, p.opMgr, p.opCreds, p.logger)
		if err != nil {
			p.mu.Unlock()
			return nil, err
		}
		p.bridge.AttachPaseHandler(adapter)
	}
	p.mu.Unlock()

	p.logger.Info("matter.bridge.pase.verifier_installed",
		slog.Int("iterations", iters),
		slog.Int("salt_len", len(salt)),
		slog.String("mode", mode))

	configured := p.configured
	configuredFactory := p.configuredFactory
	logger := p.logger
	bridge := p.bridge
	mu := &p.mu

	restore := func() {
		mu.Lock()
		defer mu.Unlock()
		if concurrent {
			restored := matterbridge.NewPerExchangePaseProvider(configuredFactory)
			restored.StartReaper(context.Background(), 30*time.Second, 60*time.Second)
			bridge.AttachPaseHandlerProvider(restored.Resolve)
		} else {
			bridge.AttachPaseHandler(configured) // nil → bridge reverts to noop
		}
		logger.Info("matter.bridge.pase.verifier_restored", slog.String("mode", mode))
	}

	return restore, nil
}
