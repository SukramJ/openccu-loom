// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matterendpoint_test

import (
	"context"
	"errors"
	"testing"

	fabricendpoint "github.com/SukramJ/go-fabric/endpoint"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// TestForeignSourceKeyIsRejected pins that a key this store did not
// issue is refused rather than treated as absent.
//
// The distinction is the whole point. [fabricendpoint.ErrNotFound] means
// "this source has no endpoint id yet", and the assembler answers it by
// ALLOCATING A NEW ONE. So a store that quietly reported a key it cannot
// read as not-found would hand every bridged endpoint a fresh number on
// the next assembly and desynchronise every commissioned controller —
// exactly the failure the Down comment of
// internal/store/sqlite/migrations/007_matter_endpoints.sql describes.
// A wiring mistake has to stop the assembly instead.
func TestForeignSourceKeyIsRejected(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := matterendpoint.New(openTestDB(t))

	foreign := fabricendpoint.StringKey("some-other-owner/key")

	_, err := s.GetEndpoint(ctx, foreign)
	if err == nil {
		t.Fatal("GetEndpoint with a foreign key returned no error")
	}
	if errors.Is(err, fabricendpoint.ErrNotFound) {
		t.Errorf("GetEndpoint reported a foreign key as ErrNotFound (%v) — the assembler "+
			"would read that as 'unassigned' and allocate a new endpoint id for every source", err)
	}
	if err := s.RemoveEndpoint(ctx, foreign); err == nil {
		t.Error("RemoveEndpoint with a foreign key returned no error")
	}
	if _, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: foreign}); err == nil {
		t.Error("UpsertEndpointAssigning with a foreign key returned no error")
	}
}

// TestRecordScopeMustMatchItsKey pins that a record whose Scope
// contradicts its key's central is refused.
//
// The central is part of the primary key AND the column garbage
// collection partitions on. If the two could disagree, a row would be
// written into one central's partition while claiming to belong to
// another, and the next model-complete assembly of whichever central
// listed it would delete it as vanished — silently renumbering that
// device on every boot.
func TestRecordScopeMustMatchItsKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := matterendpoint.New(openTestDB(t))

	key := matterendpoint.SourceKey{
		CentralName:   "ccu1",
		DeviceAddress: "A:1",
		ChannelNo:     1,
		DPKind:        matterendpoint.DPKindCustom,
		DPKey:         "STATE",
	}

	if _, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, Scope: "ccu2"}); err == nil {
		t.Error("a record scoped to ccu2 but keyed to ccu1 was accepted")
	}
	// The matching scope, and the empty one the module may send, both pass.
	for _, scope := range []string{"", "ccu1"} {
		if _, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key, Scope: scope}); err != nil {
			t.Errorf("UpsertEndpointAssigning with scope %q: %v", scope, err)
		}
	}
}

// TestListEndpointsReportsScope pins that a listed record carries the
// scope the assembler's garbage collection filtered on. The module holds
// the key opaquely, so the record is the only place that answer can come
// from.
func TestListEndpointsReportsScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := matterendpoint.New(openTestDB(t))

	for _, central := range []string{"ccu1", "ccu2"} {
		key := matterendpoint.SourceKey{
			CentralName:   central,
			DeviceAddress: "A:1",
			ChannelNo:     1,
			DPKind:        matterendpoint.DPKindCustom,
			DPKey:         "STATE",
		}
		if _, err := s.UpsertEndpointAssigning(ctx, fabricendpoint.Record{Key: key}); err != nil {
			t.Fatalf("seed %s: %v", central, err)
		}
	}

	rows, err := s.ListEndpoints(ctx, "ccu1")
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("ListEndpoints(ccu1) returned %d rows, want 1", len(rows))
	}
	if rows[0].Scope != "ccu1" {
		t.Errorf("row Scope = %q, want ccu1", rows[0].Scope)
	}
}
