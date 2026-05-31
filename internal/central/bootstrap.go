// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// Bootstrap is the composition root that turns a parsed [config.Config]
// into a [*Registry] of wired [*CentralUnit]s.
//
// It is intentionally thin: it does not touch transports or the REST
// layer. Those are composed in cmd/openccu-loom/main.go so unit tests
// can exercise Bootstrap without starting servers.
type Bootstrap struct {
	Logger *slog.Logger
}

// Build materialises every central named in cfg.Centrals. Returns a
// populated registry plus a shutdown closure that the caller should
// defer.
func (b *Bootstrap) Build(_ context.Context, cfg *config.Config) (*Registry, func(), error) {
	if cfg == nil {
		return nil, func() {}, fmt.Errorf("central.Bootstrap: nil config")
	}
	logger := b.Logger
	if logger == nil {
		logger = slog.Default()
	}

	reg := NewRegistry()
	for i := range cfg.Centrals {
		cc := &cfg.Centrals[i]
		unit, err := New(Config{Name: cc.Name, Logger: logger.With(slog.String("central", cc.Name))})
		if err != nil {
			return nil, func() {}, fmt.Errorf("central.Bootstrap(%s): %w", cc.Name, err)
		}
		if err := reg.Register(unit); err != nil {
			return nil, func() {}, err
		}
	}
	teardown := func() { reg.StopAll() }
	return reg, teardown, nil
}
