// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"time"

	mattercore "github.com/SukramJ/go-fabric/cluster/core"
	matterstore "github.com/SukramJ/go-fabric/store"
)

// matterOperationalTimeSetting keys the GeneralDiagnostics operational
// time, in milliseconds, in the Matter store's settings table — the same
// key go-fabric's node facade writes (node/persist.go), so a database
// either host wrote reads back under the other.
const matterOperationalTimeSetting = "gendiag.operational_ms"

// matterOperationalTimePersistInterval bounds what a crash loses of the
// operational time: the shutdown closure writes the final value, the
// ticker in [keepMatterOperationalTime] the intermediate ones.
const matterOperationalTimePersistInterval = 15 * time.Minute

// loadMatterOperationalTime reads the persisted operational time. A
// database written before the millisecond key existed carries the
// attribute's whole hours in the diagnostics row; those are read once as
// the seed and the next store writes the new key. Any read error is
// logged and yields zero: the attribute then restarts, which is what the
// whole-hour persistence did on every boot that lasted under an hour.
func loadMatterOperationalTime(ctx context.Context, st *matterstore.Store, rec matterstore.DiagnosticsRecord, logger *slog.Logger) time.Duration {
	raw, ok, err := st.GetSetting(ctx, matterOperationalTimeSetting)
	if err != nil {
		logger.Warn("matter.bridge.diagnostics.operational_time.load", slog.String("err", err.Error()))
		return 0
	}
	if ok {
		ms, perr := strconv.ParseInt(raw, 10, 64)
		if perr != nil {
			logger.Warn("matter.bridge.diagnostics.operational_time.parse", slog.String("value", raw), slog.String("err", perr.Error()))
			return 0
		}
		return time.Duration(ms) * time.Millisecond
	}
	hours := min(uint64(rec.BaseOperationalHours), uint64(math.MaxInt64/int64(time.Hour)))
	return time.Duration(hours) * time.Hour //nolint:gosec // bounded above
}

// storeMatterOperationalTime persists the operational time in milliseconds.
func storeMatterOperationalTime(ctx context.Context, st *matterstore.Store, g *mattercore.GeneralDiagnostics) error {
	return st.SetSetting(ctx, matterOperationalTimeSetting, strconv.FormatInt(g.TotalOperationalTime().Milliseconds(), 10))
}

// keepMatterOperationalTime persists the operational time every interval
// until ctx ends; the shutdown closure stores it once more.
func keepMatterOperationalTime(ctx context.Context, st *matterstore.Store, g *mattercore.GeneralDiagnostics, interval time.Duration, logger *slog.Logger) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if err := storeMatterOperationalTime(ctx, st, g); err != nil {
				logger.Warn("matter.bridge.diagnostics.operational_time.persist", slog.String("err", err.Error()))
			}
		}
	}
}
