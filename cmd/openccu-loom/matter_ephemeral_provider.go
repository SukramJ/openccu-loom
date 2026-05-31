// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/config"
	matterbridge "github.com/SukramJ/openccu-loom/internal/north/matter/bridge"
	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/operational"
)

// matterEphemeralProvider implements
// [matterbridge.EphemeralProvider]. Each
// `OpenCommissioningWindow` call generates a fresh random
// passcode + discriminator + salt, builds a Spake2+ verifier from
// them, swaps it into the bridge for the window's lifetime, and
// returns a Restore closure that re-arms whatever PASE handler was
// installed before.
//
// Two operating modes:
//
//   - Singleton (default, `concurrent_pairings=false`): the
//     ephemeral PaseAdapter replaces the bridge's long-lived adapter
//     via [matterbridge.Bridge.AttachPaseHandler]. Restore re-installs
//     the configured adapter (or nil → noop).
//   - Per-Exchange (`concurrent_pairings=true`): a fresh
//     [matterbridge.PerExchangePaseProvider] is installed via
//     [matterbridge.Bridge.AttachPaseHandlerProvider] whose factory
//     builds a brand-new PaseAdapter from the ephemeral creds for
//     every commissioning exchange. Restore re-installs the
//     long-lived configured provider (rebuilt from `cfg.Commissioning`).
//
// In both modes the same QR + manual code (built from
// disc/passcode) is consumed by every commissioner that pairs
// during the window.
type matterEphemeralProvider struct {
	bridge            *matterbridge.Bridge
	cfg               config.NorthMatterCommissioning
	opMgr             *operational.Manager
	opCreds           *mattercore.OperationalCredentials
	logger            *slog.Logger
	configured        *matterbridge.PaseAdapter        // long-lived "fallback" adapter restored on close (singleton mode)
	configuredFactory func() *matterbridge.PaseAdapter // factory restored on close (concurrent mode); nil in singleton mode

	// mu serialises GenerateAndInstall against itself + the Restore
	// closure so a window opening that races a previous Restore
	// observes a consistent paseHandler on the bridge.
	mu sync.Mutex
}

// newMatterEphemeralProvider returns a provider ready to be wired
// onto a [matterbridge.CommissioningWindowOpener]. Pass the
// already-built configured PaseAdapter (singleton mode) or
// configuredFactory (concurrent-pairings mode) so the Restore
// closure can re-arm it on window close. Both nil → "no PASE
// handler" between windows (the bridge then rejects stray PASE
// traffic when no window is open, which is the safer default).
func newMatterEphemeralProvider(
	bridge *matterbridge.Bridge,
	cfg config.NorthMatterCommissioning,
	opMgr *operational.Manager,
	opCreds *mattercore.OperationalCredentials,
	configured *matterbridge.PaseAdapter,
	configuredFactory func() *matterbridge.PaseAdapter,
	logger *slog.Logger,
) *matterEphemeralProvider {
	return &matterEphemeralProvider{
		bridge:            bridge,
		cfg:               cfg,
		opMgr:             opMgr,
		opCreds:           opCreds,
		configured:        configured,
		configuredFactory: configuredFactory,
		logger:            logger,
	}
}

// GenerateAndInstall implements [matterbridge.EphemeralProvider].
func (p *matterEphemeralProvider) GenerateAndInstall(_ context.Context) (matterbridge.EphemeralCredentials, error) {
	if p == nil || p.bridge == nil || p.opMgr == nil {
		return matterbridge.EphemeralCredentials{}, fmt.Errorf("ephemeral provider: not wired")
	}

	disc, err := matterbridge.RandomDiscriminator()
	if err != nil {
		return matterbridge.EphemeralCredentials{}, err
	}
	pass, err := matterbridge.RandomPasscode()
	if err != nil {
		return matterbridge.EphemeralCredentials{}, err
	}
	salt, err := matterbridge.RandomSalt()
	if err != nil {
		return matterbridge.EphemeralCredentials{}, err
	}
	iterations := p.cfg.Iterations
	if iterations == 0 {
		iterations = 1000
	}

	concurrent := p.configuredFactory != nil
	mode := "singleton"
	if concurrent {
		mode = "per-exchange"
	}

	p.mu.Lock()
	if concurrent {
		// Per-exchange ephemeral: install a fresh
		// PerExchangePaseProvider whose factory minted a fresh
		// PaseAdapter from the same ephemeral creds for every Pake1
		// arrival. Reaper auto-cleans stale entries.
		ephem := matterbridge.NewPerExchangePaseProvider(func() *matterbridge.PaseAdapter {
			a, err := buildPaseAdapterFromCreds(pass, salt, iterations, p.opMgr, p.opCreds, nil, p.logger)
			if err != nil {
				p.logger.Warn("matter.bridge.pase.ephemeral_factory", slog.String("err", err.Error()))
				return nil
			}
			return a
		})
		ephem.StartReaper(context.Background(), 30*time.Second, 60*time.Second)
		p.bridge.AttachPaseHandlerProvider(ephem.Resolve)
	} else {
		// Singleton ephemeral: build one PaseAdapter and swap it onto
		// the bridge.
		adapter, err := buildPaseAdapterFromCreds(pass, salt, iterations, p.opMgr, p.opCreds, nil, p.logger)
		if err != nil {
			p.mu.Unlock()
			return matterbridge.EphemeralCredentials{}, err
		}
		p.bridge.AttachPaseHandler(adapter)
	}
	p.mu.Unlock()

	p.logger.Info("matter.bridge.pase.ephemeral_installed",
		slog.Int("iterations", iterations),
		slog.Int("salt_len", len(salt)),
		slog.Int("discriminator", int(disc)),
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
			// Restore the long-lived configured per-exchange provider.
			// Clearing the provider (nil) would force the bridge back
			// to its singleton path; rebuilding from the configured
			// factory keeps the operator's intended ConcurrentPairings
			// posture across windows.
			restored := matterbridge.NewPerExchangePaseProvider(configuredFactory)
			restored.StartReaper(context.Background(), 30*time.Second, 60*time.Second)
			bridge.AttachPaseHandlerProvider(restored.Resolve)
		} else {
			bridge.AttachPaseHandler(configured) // nil → bridge reverts to noop
		}
		logger.Info("matter.bridge.pase.ephemeral_restored",
			slog.String("mode", mode),
			slog.Bool("configured_present", configured != nil || configuredFactory != nil))
	}

	return matterbridge.EphemeralCredentials{
		Discriminator: disc,
		Passcode:      pass,
		Restore:       restore,
	}, nil
}
