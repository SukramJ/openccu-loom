// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// defaultInterfaceRetryBackoff is the wait schedule between interface
// device-load (ingest + callback init) retries after a boot-time failure. An
// add-on that co-starts with the CCU commonly sees the backend answer http 503
// ("internal backend exception" / "service not available") for the first
// minute or so while ReGaHss and the per-interface RPC service warm up. These
// delays re-probe while staying light, saturating at 60 s for the daemon's
// lifetime.
var defaultInterfaceRetryBackoff = []time.Duration{
	2 * time.Second,
	5 * time.Second,
	10 * time.Second,
	20 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

// startInterfaceRetry spawns a background goroutine that re-attempts the
// interface device-load (ingest + callback init) with backoff until it succeeds
// or ctx is cancelled. Used when the boot-time ingest failed (e.g. the CCU's
// backend answered 503 during the daemon's startup window): without it the
// interface stays empty until an unrelated recovery cycle happens to fire — or
// indefinitely if the CCU's ping stays responsive while listDevices is still
// 503. Mirrors the hub-side startHubRetry so both surfaces self-heal the same
// way.
//
// The returned *sync.WaitGroup counts the single background goroutine so tests
// can drain on shutdown after cancelling ctx; production callers cancel ctx via
// the interface closer and may ignore the return value.
func startInterfaceRetry(
	ctx context.Context,
	central, interfaceID string,
	backoff []time.Duration,
	attempt func(context.Context) error,
	logger *slog.Logger,
) *sync.WaitGroup {
	var wg sync.WaitGroup
	wg.Add(1)
	SafeGo("interface_retry."+interfaceID, func() {
		defer wg.Done()
		ok := retryWithBackoff(ctx, backoff, attempt)
		if ok && logger != nil {
			logger.Info("wire.interface.recovered",
				slog.String("central", central),
				slog.String("interface", interfaceID))
		}
	})
	return &wg
}
