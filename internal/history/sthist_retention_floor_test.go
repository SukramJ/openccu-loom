// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package history

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestStHistRollupLagIsTheRetentionFloor pins the two values that must not
// drift apart.
//
// The retention purge deletes raw samples once they fall outside the
// configured retention; the hourly fold only folds samples older than
// rollupHourlyLag. If the lag exceeds the floor the config accepts, an
// operator whose retention sits between the two loses raw rows before the
// fold has seen them — silently, permanently, and only for that range of
// settings.
//
// config.HistoryRetentionFloor documented itself as mirroring this lag and
// said in the same breath that the mirroring was unenforced. It is derived
// now, so raising the lag raises the floor with it.
func TestStHistRollupLagIsTheRetentionFloor(t *testing.T) {
	t.Parallel()

	if rollupHourlyLag != config.HistoryRetentionFloor {
		t.Errorf("rollupHourlyLag = %v, config.HistoryRetentionFloor = %v: a purge can now outrun the fold",
			rollupHourlyLag, config.HistoryRetentionFloor)
	}
}
