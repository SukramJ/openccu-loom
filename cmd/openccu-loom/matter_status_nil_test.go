// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestMatterCandidateProviderAdapter_NilFields_ReturnsNil verifies that
// MatterCandidates returns nil (not panic) when reg and/or cfg is nil.
func TestMatterCandidateProviderAdapter_NilFields_ReturnsNil(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-one")
	cfg := &config.Config{}

	if got := (&matterCandidateProviderAdapter{}).MatterCandidates(context.Background()); got != nil {
		t.Errorf("nil reg + nil cfg: expected nil candidates, got %v", got)
	}
	if got := (&matterCandidateProviderAdapter{reg: reg}).MatterCandidates(context.Background()); got != nil {
		t.Errorf("nil cfg: expected nil candidates, got %v", got)
	}
	if got := (&matterCandidateProviderAdapter{cfg: cfg}).MatterCandidates(context.Background()); got != nil {
		t.Errorf("nil reg: expected nil candidates, got %v", got)
	}
}

// TestMatterCandidateProviderAdapter_NilReceiver_ReturnsNil verifies
// that a nil receiver returns nil rather than panicking.
func TestMatterCandidateProviderAdapter_NilReceiver_ReturnsNil(t *testing.T) {
	t.Parallel()
	var a *matterCandidateProviderAdapter
	if got := a.MatterCandidates(context.Background()); got != nil {
		t.Errorf("expected nil candidates, got %v", got)
	}
}

// TestMatterCandidateProviderAdapter_WalksRegistry_SkipsNilAndEmptyUnits
// verifies that MatterCandidates iterates every registered central without
// panicking and yields no candidates for centrals whose ModelRegistry holds
// no devices — proving the loop + nil-ModelRegistry-skip path rather than
// re-asserting the nil-guard case above.
func TestMatterCandidateProviderAdapter_WalksRegistry_SkipsNilAndEmptyUnits(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-one", "ccu-two")
	a := &matterCandidateProviderAdapter{reg: reg, cfg: &config.Config{}}

	got := a.MatterCandidates(context.Background())
	if len(got) != 0 {
		t.Errorf("expected no candidates from centrals with empty ModelRegistry, got %v", got)
	}
}
