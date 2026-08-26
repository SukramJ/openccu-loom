// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"log/slog"
	"time"
)

// shutdownDeinitTimeout bounds the deinit() a teardown closer issues. A CCU
// that is itself going down must not hold the daemon's shutdown open, and an
// unregistration that has not completed within a few seconds will not complete
// at all.
const shutdownDeinitTimeout = 3 * time.Second

// callbackDeiniter is the slice of a southbound backend a teardown closer
// needs: severing the callback registration for one URL.
type callbackDeiniter interface {
	Deinit(ctx context.Context, callbackURL string) error
}

// deinitOnShutdown severs the CCU-side callback registration for callbackURL
// on a FRESH, short-lived context.
//
// Teardown runs after the per-central bring-up context has been cancelled:
// [centralBringUp.teardown] cancels it, waits for the bring-up goroutine to
// drain, and only then runs the closers. A deinit issued on that context
// therefore fails immediately with context.Canceled and never reaches the
// CCU — the registration survives, the next generation registers a second
// one, and the interface delivers every event twice from then on.
//
// centralName / interfaceID only enrich the failure log.
func deinitOnShutdown(backend callbackDeiniter, callbackURL, centralName, interfaceID string, logger *slog.Logger) {
	if backend == nil || callbackURL == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), shutdownDeinitTimeout)
	defer cancel()
	if err := backend.Deinit(ctx, callbackURL); err != nil && logger != nil {
		logger.Debug("wire.deinit.shutdown",
			slog.String("central", centralName),
			slog.String("interface", interfaceID),
			slog.String("err", err.Error()))
	}
}
