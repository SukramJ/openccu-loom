// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/cover"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/custom/lock"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
)

// TestCustomTypesImplementAggregateDataPoint pins the contract that
// the five primary custom-DP families (Climate, Cover, Light, Lock,
// Switch) satisfy [custom.AggregateDataPoint] so north-bound
// adapters can gate "available / pending / unknown" rendering on a
// uniform interface — no per-family branching in the discovery /
// REST / WS path.
//
// Compile-time interface assertions are enough; functional behaviour
// of IsRefreshed / StateUncertain is exercised by per-family tests.
func TestCustomTypesImplementAggregateDataPoint(t *testing.T) {
	t.Parallel()
	// Compile-time assertions: every type below must satisfy the
	// AggregateDataPoint interface, otherwise the test file fails to
	// compile.
	var (
		_ custom.AggregateDataPoint = (*climate.Climate)(nil)
		_ custom.AggregateDataPoint = (*cover.Cover)(nil)
		_ custom.AggregateDataPoint = (*light.Light)(nil)
		_ custom.AggregateDataPoint = (*lock.Lock)(nil)
		_ custom.AggregateDataPoint = (*switchdev.Switch)(nil)
	)
}
