// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matterendpoint_test

import (
	"context"
	"testing"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// TestEraseEndpointsRestartsTheNumbering pins the factory reset's erase:
// every endpoint record of every central goes, and the next assignment
// is the first bridged number again. The removal before the erase is the
// contrast — a removed number is never reissued, so only the erase can
// hand the range out a second time. The operator's exposure allowlist is
// not endpoint identity and survives the erase.
func TestEraseEndpointsRestartsTheNumbering(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := matterendpoint.New(openTestDB(t))

	assign := func(k matterendpoint.SourceKey) uint16 {
		t.Helper()
		id, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: k})
		if err != nil {
			t.Fatalf("assign %v: %v", k, err)
		}
		return id
	}

	a := testKey("ccu1", "A:1", 1, matterendpoint.DPKindCustom, "STATE")
	b := testKey("ccu1", "B:1", 1, matterendpoint.DPKindCustom, "STATE")
	c := testKey("ccu2", "C:1", 1, matterendpoint.DPKindCustom, "STATE")
	if got := assign(a); got != 2 {
		t.Fatalf("first assignment = %d, want 2", got)
	}
	assign(b)
	removed := assign(c)
	if err := s.RemoveEndpoint(ctx, c); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got := assign(testKey("ccu2", "D:1", 1, matterendpoint.DPKindCustom, "STATE")); got <= removed {
		t.Fatalf("assignment after a removal = %d, want above the removed %d (never reissued)", got, removed)
	}

	if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: a, Enabled: true}); err != nil {
		t.Fatalf("seed exposure: %v", err)
	}

	if err := s.EraseEndpoints(ctx); err != nil {
		t.Fatalf("EraseEndpoints: %v", err)
	}
	if recs, err := s.ListEndpoints(ctx, ""); err != nil || len(recs) != 0 {
		t.Fatalf("records after the erase = %v (err %v), want none", recs, err)
	}
	if got := assign(b); got != 2 {
		t.Errorf("first assignment after the erase = %d, want 2 (the first bridged number)", got)
	}
	if ok, err := s.IsExposed(ctx, a); err != nil || !ok {
		t.Errorf("exposure after the erase = %v (err %v), want it kept", ok, err)
	}
}
