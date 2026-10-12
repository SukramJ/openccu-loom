// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matterendpoint_test

import (
	"context"
	"testing"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

func TestNodeLabel_RoundTripSurvivesReassemblyUpsert(t *testing.T) {
	t.Parallel()
	s := matterendpoint.New(openTestDB(t))
	ctx := context.Background()
	key := testKey("c1", "A:1", 1, matterendpoint.DPKindCustom, "STATE")
	if err := s.UpsertEndpoint(ctx, fabricendpoint.Record{Key: key, EndpointID: 7, DeviceType: 0x0100}); err != nil {
		t.Fatalf("UpsertEndpoint: %v", err)
	}
	if got := s.LoadNodeLabel(key); got != "" {
		t.Fatalf("LoadNodeLabel before any write = %q, want empty", got)
	}

	s.SaveNodeLabel(key, "Kitchen lamp")
	if got := s.LoadNodeLabel(key); got != "Kitchen lamp" {
		t.Fatalf("LoadNodeLabel = %q, want %q", got, "Kitchen lamp")
	}

	// Re-assembly upserts the identity row again; the label must survive.
	if err := s.UpsertEndpoint(ctx, fabricendpoint.Record{Key: key, EndpointID: 7, DeviceType: 0x0100}); err != nil {
		t.Fatalf("UpsertEndpoint again: %v", err)
	}
	if got := s.LoadNodeLabel(key); got != "Kitchen lamp" {
		t.Fatalf("LoadNodeLabel after re-upsert = %q, want %q", got, "Kitchen lamp")
	}

	// Another key on the same device stays unlabelled.
	other := testKey("c1", "A:1", 2, matterendpoint.DPKindCustom, "STATE")
	if err := s.UpsertEndpoint(ctx, fabricendpoint.Record{Key: other, EndpointID: 8, DeviceType: 0x0100}); err != nil {
		t.Fatalf("UpsertEndpoint other: %v", err)
	}
	if got := s.LoadNodeLabel(other); got != "" {
		t.Fatalf("LoadNodeLabel(other) = %q, want empty", got)
	}

	// Clearing writes the empty label back.
	s.SaveNodeLabel(key, "")
	if got := s.LoadNodeLabel(key); got != "" {
		t.Fatalf("LoadNodeLabel after clear = %q, want empty", got)
	}
}

func TestNodeLabel_KeyWithoutRow(t *testing.T) {
	t.Parallel()
	s := matterendpoint.New(openTestDB(t))
	ctx := context.Background()
	key := testKey("c1", "B:1", 1, matterendpoint.DPKindCustom, "STATE")

	if got := s.LoadNodeLabel(key); got != "" {
		t.Fatalf("LoadNodeLabel without row = %q, want empty", got)
	}
	// A save without a row is dropped, never creates a row: only the
	// assembler allocates endpoint ids.
	s.SaveNodeLabel(key, "orphan")
	if _, err := s.GetEndpoint(ctx, key); err == nil {
		t.Fatal("SaveNodeLabel created a matter_endpoints row for an unassembled key")
	}
	if got := s.LoadNodeLabel(key); got != "" {
		t.Fatalf("LoadNodeLabel after orphan save = %q, want empty", got)
	}
}
