// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// sessionPurgeInterval is the cadence at which expired auth sessions are
// swept from memory and the durable store. Hourly is ample: the sweep is
// a single indexed DELETE, and expired sessions are also evicted lazily
// on Lookup, so this is a backstop for sessions that are never looked up
// again before they expire.
const sessionPurgeInterval = time.Hour

// startSessionPurge runs a background ticker that periodically calls
// PurgeExpired on the session store, evicting expired sessions from
// memory and (when durable) deleting their rows. Returns a stop function
// that cancels the goroutine and waits for it to exit. A nil store
// yields a no-op stopper. Mirrors the start/stop shape of
// startMatterHealthProbe.
func startSessionPurge(ctx context.Context, sessions *auth.SessionStore, logger *slog.Logger, interval time.Duration) func() {
	if sessions == nil {
		return func() {}
	}
	if interval <= 0 {
		interval = sessionPurgeInterval
	}
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-pctx.Done():
				return
			case <-ticker.C:
				purgeSessionsOnce(pctx, sessions, logger)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func purgeSessionsOnce(ctx context.Context, sessions *auth.SessionStore, logger *slog.Logger) {
	n, err := sessions.PurgeExpired(ctx)
	if err != nil {
		logger.Warn("auth.session.purge", slog.String("err", err.Error()))
		return
	}
	if n > 0 {
		logger.Debug("auth.session.purge", slog.Int("removed", n))
	}
}
