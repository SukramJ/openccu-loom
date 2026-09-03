// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package central

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/wiring"
)

// Bootstrap is the composition root that turns a parsed [config.Config]
// into a [*Registry] of wired [*Unit]s.
//
// It is intentionally thin: it does not touch transports or the REST
// layer. Those are composed in cmd/openccu-loom/main.go so unit tests
// can exercise Bootstrap without starting servers.
type Bootstrap struct {
	Logger *slog.Logger
	// Manifest is the wiring ledger the registry records its seams into.
	// Nil gets a fresh one, which is right for tests and for the CLI; the
	// daemon passes the manifest it has already been declaring into since
	// before the registry existed.
	Manifest *wiring.Manifest
}

// Build materialises every central named in cfg.Centrals. Returns a
// populated registry plus a shutdown closure that the caller should
// defer.
func (b *Bootstrap) Build(_ context.Context, cfg *config.Config) (*Registry, func(), error) {
	if cfg == nil {
		return nil, func() {}, errors.New("central.Bootstrap: nil config")
	}
	logger := b.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Daemon-global instance identity — a component of every central's
	// wire interface_id (`loom-<instance_name>-<ccu_name>-<iface>`, built
	// by adapter.InitInterfaceID; ADR-0024). Same for every central in
	// this daemon.
	instanceName := cfg.North.Discovery.MDNS.ResolveInstanceName()

	reg := NewRegistryWithManifest(b.Manifest)
	for i := range cfg.Centrals {
		cc := &cfg.Centrals[i]
		unit, err := New(Config{
			Name:         cc.Name,
			InstanceName: instanceName,
			Logger:       logger.With(slog.String("central", cc.Name)),
		})
		if err != nil {
			return nil, func() {}, fmt.Errorf("central.Bootstrap(%s): %w", cc.Name, err)
		}
		if err := reg.Register(unit); err != nil {
			return nil, func() {}, err
		}
	}
	teardown := func() { reg.StopAll() } //nolint:contextcheck // shutdown: StopAll/Stop have no ctx parameter; background cleanup is intentional
	return reg, teardown, nil
}
