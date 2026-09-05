// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	mattercore "github.com/SukramJ/go-fabric/cluster/core"
	"github.com/SukramJ/go-fabric/secure/operational"

	"github.com/SukramJ/openccu-loom/internal/config"
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
	// observes a consistent paseHandler on the bridge. It also guards
	// `active`.
	mu sync.Mutex
	// active is the per-exchange provider currently attached to the bridge
	// (concurrent mode only). Each one runs a reaper goroutine that only
	// [matterbridge.PerExchangePaseProvider.Stop] ends, so every swap — and
	// the daemon shutdown — has to release the one it replaces. Without
	// that every opened commissioning window plus its close left a ticker
	// goroutine behind for the process lifetime.
	active *matterbridge.PerExchangePaseProvider
}

// swapActive installs next as the live per-exchange provider and stops the
// one it replaces. Caller holds p.mu.
func (p *matterEphemeralProvider) swapActive(next *matterbridge.PerExchangePaseProvider) {
	prev := p.active
	p.active = next
	if prev != nil {
		prev.Stop()
	}
}

// Close releases the provider this one still holds. The daemon's Matter
// teardown calls it; a window left open at shutdown would otherwise keep its
// reaper ticking past the bridge it belonged to.
func (p *matterEphemeralProvider) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.swapActive(nil)
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
		return matterbridge.EphemeralCredentials{}, errors.New("ephemeral provider: not wired")
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
		ephem := matterbridge.NewPerExchangePaseProvider(func() *matterbridge.PaseAdapter { //nolint:contextcheck // factory signature is fixed; buildPaseAdapterFromCreds has no ctx parameter
			a, err := buildPaseAdapterFromCreds(pass, salt, iterations, p.opMgr, p.opCreds, nil, p.logger)
			if err != nil {
				p.logger.Warn("matter.bridge.pase.ephemeral_factory", slog.String("err", err.Error()))
				return nil
			}
			return a
		})
		// The reaper spans the commissioning window, not the exchange
		// that opened it: a request-scoped ctx would stop it the moment
		// the REST call returns. swapActive / Close end it instead.
		ephem.StartReaper(context.Background(), 30*time.Second, 60*time.Second) //nolint:contextcheck // see above: the reaper outlives the invoking exchange
		p.bridge.AttachPaseHandlerProvider(ephem.Resolve)
		p.swapActive(ephem)
	} else {
		// Singleton ephemeral: build one PaseAdapter and swap it onto
		// the bridge.
		adapter, err := buildPaseAdapterFromCreds(pass, salt, iterations, p.opMgr, p.opCreds, nil, p.logger) //nolint:contextcheck // buildPaseAdapterFromCreds has no ctx parameter
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

	restore := func() { //nolint:contextcheck // restore callback has no ctx parameter; StartReaper needs a daemon-lifetime context independent of the commissioning window's ctx
		p.mu.Lock()
		defer p.mu.Unlock()
		if concurrent {
			// Restore the long-lived configured per-exchange provider.
			// Clearing the provider (nil) would force the bridge back
			// to its singleton path; rebuilding from the configured
			// factory keeps the operator's intended ConcurrentPairings
			// posture across windows.
			restored := matterbridge.NewPerExchangePaseProvider(configuredFactory)
			restored.StartReaper(context.Background(), 30*time.Second, 60*time.Second)
			bridge.AttachPaseHandlerProvider(restored.Resolve)
			// Releases the window's own provider: its ephemeral passcode
			// is dead the moment the window closes.
			p.swapActive(restored)
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
