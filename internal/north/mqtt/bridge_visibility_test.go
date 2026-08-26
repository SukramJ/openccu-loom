// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubVisibilitySet is an inline test-double for filter.VisibilitySet.
// A parameter listed in `blocked` is invisible; all others are visible.
type stubVisibilitySet struct {
	blocked map[string]bool // keyed by parameter name
}

func newBlockingVisSet(params ...string) *stubVisibilitySet {
	m := make(map[string]bool, len(params))
	for _, p := range params {
		m[p] = true
	}
	return &stubVisibilitySet{blocked: m}
}

func (s *stubVisibilitySet) Visible(_, _ string, _ hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return !s.blocked[string(p)]
}

func (s *stubVisibilitySet) VisibleForChannel(_, _ string, _ int, _ hmenum.ParamsetKey, p hmenum.Parameter) bool {
	return !s.blocked[string(p)]
}

// allowAllVisSet is a stub that always returns true (visible).
type allowAllVisSet struct{}

func (allowAllVisSet) Visible(_, _ string, _ hmenum.ParamsetKey, _ hmenum.Parameter) bool {
	return true
}

func (allowAllVisSet) VisibleForChannel(_, _ string, _ int, _ hmenum.ParamsetKey, _ hmenum.Parameter) bool {
	return true
}

// TestPublishStateBlockedByGlobalVisibilityFilter verifies that when
// BridgeConfig.Visibility is set and VisibleForChannel returns false for the
// event's parameter, neither a raw-plane topic nor a HA-Discovery topic is
// published.
func TestPublishStateBlockedByGlobalVisibilityFilter(t *testing.T) {
	t.Parallel()

	rec := &recordingPublisher{}
	vis := newBlockingVisSet("STATE") // "STATE" is the parameter in stableEvent

	db := &fixedDiscoveryBuilder{
		component: "switch",
		objectID:  "0001abcd_3_state",
		payload:   []byte(`{"unique_id":"blocked"}`),
	}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.RawEnabled = true
		c.HADiscoveryEnabled = true
		c.DiscoveryBuilder = db
		c.Visibility = vis
	})

	if err := b.PublishState(context.Background(), stableEvent); err != nil {
		t.Fatalf("PublishState returned unexpected error: %v", err)
	}

	if n := len(rec.records()); n != 0 {
		t.Fatalf("expected 0 publishes when visibility blocks the parameter, got %d: %v", n, rec.records())
	}
}

// TestPublishStatePassesWhenVisibilityNil verifies that nil Visibility
// behaves as "no filter" — both raw and discovery topics are published as
// normal.
func TestPublishStatePassesWhenVisibilityNil(t *testing.T) {
	t.Parallel()

	rec := &recordingPublisher{}
	db := &fixedDiscoveryBuilder{
		component: "switch",
		objectID:  "0001abcd_3_state",
		payload:   []byte(`{"unique_id":"nilvis"}`),
	}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.RawEnabled = true
		c.HADiscoveryEnabled = true
		c.DiscoveryBuilder = db
		c.Visibility = nil // explicit: no filter
	})

	if err := b.PublishState(context.Background(), stableEvent); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	// PublishState no longer writes the raw per-DP state topic — that moved
	// to PublishSlotState. Verify only that discovery is published when
	// visibility is nil (no gate applied).
	if n := rec.countPrefix("homeassistant/"); n == 0 {
		t.Fatalf("expected at least one discovery publish when visibility is nil, got 0")
	}
}

// TestPublishStatePassesWhenVisibilityAllows verifies that when a
// VisibilitySet is wired but returns true, publishes proceed as normal.
func TestPublishStatePassesWhenVisibilityAllows(t *testing.T) {
	t.Parallel()

	rec := &recordingPublisher{}
	db := &fixedDiscoveryBuilder{
		component: "switch",
		objectID:  "0001abcd_3_state",
		payload:   []byte(`{"unique_id":"allowed"}`),
	}
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.RawEnabled = true
		c.HADiscoveryEnabled = true
		c.DiscoveryBuilder = db
		c.Visibility = allowAllVisSet{} // non-nil but always allows
	})

	if err := b.PublishState(context.Background(), stableEvent); err != nil {
		t.Fatalf("PublishState: %v", err)
	}

	// PublishState no longer writes the raw per-DP state topic — that moved
	// to PublishSlotState. Verify only that discovery is published when
	// visibility allows.
	if n := rec.countPrefix("homeassistant/"); n == 0 {
		t.Fatalf("expected at least one discovery publish when visibility allows, got 0")
	}
}

// TestPublishStateBlockedByVisibilityReturnsNilError verifies that a
// visibility-blocked call returns nil, not an error.
func TestPublishStateBlockedByVisibilityReturnsNilError(t *testing.T) {
	t.Parallel()

	rec := &recordingPublisher{}
	vis := newBlockingVisSet("STATE")
	b := newDeepBridge(t, rec, func(c *BridgeConfig) {
		c.Visibility = vis
	})

	err := b.PublishState(context.Background(), stableEvent)
	if err != nil {
		t.Fatalf("expected nil error when visibility blocks, got: %v", err)
	}
}
