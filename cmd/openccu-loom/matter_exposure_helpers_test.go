// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"testing"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// exposeEveryCandidate enables every mappable source of every registered
// central in the exposure allowlist and reassembles, the way an operator
// enables devices through the exposure endpoints. The allowlist gates the
// first assembly (startMatterBridge wires the checker before the node
// starts), so a pin that needs bridged endpoints enables them first.
func exposeEveryCandidate(ctx context.Context, t *testing.T, bundle *matterBridgeBundle, reg *central.Registry) {
	t.Helper()
	for _, u := range reg.List() {
		if u == nil || u.ModelRegistry == nil {
			continue
		}
		candidates := matteradapter.CollectCandidates(u.Name(), u.ModelRegistry.List(), true)
		for i := range candidates {
			key := candidates[i].Key
			if err := bundle.identity.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: true, Actor: "test"}); err != nil {
				t.Fatalf("UpsertExposure(%s): %v", key, err)
			}
		}
	}
	if err := bundle.sync(ctx); err != nil {
		t.Fatalf("sync after exposing every candidate: %v", err)
	}
}
