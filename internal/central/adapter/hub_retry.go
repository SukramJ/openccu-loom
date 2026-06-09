// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client/rega"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// hubWireFn matches [WireHub]'s signature so the background retry can be
// exercised with a fake in tests.
type hubWireFn func(ctx context.Context, cc config.CentralConfig, unit *central.Unit, logger *slog.Logger) (*rega.Runner, HubData, func(), error)

// wireLoadAndRefresh installs the central.refresh_client_data handler: a
// per-interface fetch-all-device-data sweep (the push-event-first
// reconciliation safety net). Shared by the boot path and the background
// hub-recovery path so both wire the same closure once a Rega runner exists.
func wireLoadAndRefresh(unit *central.Unit, pipeline *DevicePipeline, ifaces []config.InterfaceSpec, runner *rega.Runner, logger *slog.Logger) {
	if unit == nil || pipeline == nil || runner == nil {
		return
	}
	unit.SetLoadAndRefreshFn(func(ctx context.Context) error {
		var firstErr error
		for _, ifaceSpec := range ifaces {
			id := strings.TrimSpace(ifaceSpec.Name)
			if id == "" {
				continue
			}
			if err := pipeline.seedValues(ctx, id, runner, logger); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	})
}

// startHubRetry spawns a background goroutine that re-attempts hubFn (WireHub)
// with backoff until it succeeds or ctx is cancelled. On success it invokes
// onWired with the established runner + the hub-session closer. Used when the
// boot-time WireHub failed (e.g. the CCU's ReGa was not yet reachable during
// the daemon's startup window) — without it the central's hub surface and
// refresh safety net would stay dead until a manual restart.
func startHubRetry(
	ctx context.Context,
	cc config.CentralConfig,
	unit *central.Unit,
	logger *slog.Logger,
	hubFn hubWireFn,
	backoff []time.Duration,
	onWired func(runner *rega.Runner, closer func()),
) {
	go func() {
		ok := retryHubWiring(ctx, backoff, func(rctx context.Context) error {
			runner, _, closer, err := hubFn(rctx, cc, unit, logger)
			if err != nil {
				return err
			}
			onWired(runner, closer)
			return nil
		})
		if ok && logger != nil {
			logger.Info("wire.hub.recovered", slog.String("central", cc.Name))
		}
	}()
}

// defaultHubRetryBackoff is the wait schedule between WireHub retries after a
// boot-time hub-wiring failure. The CCU's ReGa is commonly not reachable
// during the daemon's startup window (CCU reboot, addon co-start); these
// delays re-probe while staying light, saturating at 60 s for the daemon's
// lifetime.
var defaultHubRetryBackoff = []time.Duration{
	5 * time.Second,
	10 * time.Second,
	20 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

// retryHubWiring calls attempt repeatedly until it returns nil (success) or
// ctx is cancelled. Between attempts it waits backoff[i], reusing the last
// element once the slice is exhausted. Returns true on success, false when
// ctx is cancelled first.
//
// This is the bounded, context-aware backoff loop behind the background
// hub-wiring recovery: WireHub runs once at boot, and a transient failure
// there would otherwise leave a central's hub + refresh safety net dead until
// a manual restart. The loop is pure (no goroutine, no I/O of its own) so it
// is unit-testable with a tiny backoff and a fake attempt.
func retryHubWiring(ctx context.Context, backoff []time.Duration, attempt func(context.Context) error) bool {
	for i := 0; ; i++ {
		if err := attempt(ctx); err == nil {
			return true
		}
		d := backoff[len(backoff)-1]
		if i < len(backoff) {
			d = backoff[i]
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(d):
		}
	}
}
