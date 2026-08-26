// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"strings"
	"testing"
)

// TestCentralMetricsReachTheDiagnosticsDump pins the last link of the
// per-central metrics chain: the composition root must hand the REST
// router the provider that reads every central's aggregator.
//
// Both ends of the chain look healthy without it. The daemon builds an
// Observer + Aggregator per central at boot and keeps them alive; the
// diagnostics handler renders whatever provider it is given. Leave this
// one assignment out and every RPC, recovery, cache and model counter
// is computed on a live daemon and discarded, while the dump that
// support escalations are triaged from silently omits the block.
func TestCentralMetricsReachTheDiagnosticsDump(t *testing.T) {
	t.Parallel()

	mount := collapseSpaces(readRestMount(t))
	if !strings.Contains(mount, "CentralMetrics: introspect.MetricsSnapshots") {
		t.Error("the REST router never receives the per-central metrics provider, so the diagnostics dump " +
			"omits the typed metrics block while the daemon keeps computing it")
	}
}
