// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"errors"
	"testing"
)

func newDiagramStore(t *testing.T) *DiagramConfigStore {
	t.Helper()
	return NewDiagramConfigStore(openTestDB(t, "diagram_configs.db"))
}

func TestDiagram_CreateGetRoundTrip(t *testing.T) {
	t.Parallel()
	s := newDiagramStore(t)
	ctx := context.Background()
	cfg := `{"series":[{"central":"ccu1","interface_id":"i","channel_address":"D:1","parameter":"P"}]}`
	d, err := s.Create(ctx, "alice", "Wohnzimmer", "private", cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if d.ID == "" || d.OwnerSubject != "alice" || d.Name != "Wohnzimmer" {
		t.Fatalf("bad create result: %+v", d)
	}
	got, err := s.Get(ctx, d.ID, "alice", false)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ConfigJSON != cfg {
		t.Errorf("config round-trip mismatch: %q", got.ConfigJSON)
	}
}

func TestDiagram_ListOwnAndShared(t *testing.T) {
	t.Parallel()
	s := newDiagramStore(t)
	ctx := context.Background()
	_, _ = s.Create(ctx, "alice", "A-priv", "private", "")
	_, _ = s.Create(ctx, "alice", "A-shared", "shared", "")
	_, _ = s.Create(ctx, "bob", "B-priv", "private", "")
	_, _ = s.Create(ctx, "bob", "B-shared", "shared", "")

	got, err := s.List(ctx, "alice")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	names := map[string]bool{}
	for _, d := range got {
		names[d.Name] = true
	}
	// Alice sees her own (both) + bob's shared, but NOT bob's private.
	if !names["A-priv"] || !names["A-shared"] || !names["B-shared"] {
		t.Errorf("missing expected diagrams: %v", names)
	}
	if names["B-priv"] {
		t.Error("must not list another owner's private diagram")
	}
}

func TestDiagram_GetPrivateForbiddenForNonOwner(t *testing.T) {
	t.Parallel()
	s := newDiagramStore(t)
	ctx := context.Background()
	d, _ := s.Create(ctx, "alice", "Secret", "private", "")

	if _, err := s.Get(ctx, d.ID, "bob", false); !errors.Is(err, ErrDiagramForbidden) {
		t.Errorf("non-owner private Get: want ErrDiagramForbidden, got %v", err)
	}
	// Admin may read.
	if _, err := s.Get(ctx, d.ID, "bob", true); err != nil {
		t.Errorf("admin private Get: %v", err)
	}
}

func TestDiagram_UpdateOwnerOnly(t *testing.T) {
	t.Parallel()
	s := newDiagramStore(t)
	ctx := context.Background()
	d, _ := s.Create(ctx, "alice", "Name", "private", "")

	if _, err := s.Update(ctx, d.ID, "bob", false, "Hijack", "private", ""); !errors.Is(err, ErrDiagramForbidden) {
		t.Errorf("non-owner Update: want ErrDiagramForbidden, got %v", err)
	}
	up, err := s.Update(ctx, d.ID, "alice", false, "Renamed", "shared", "")
	if err != nil {
		t.Fatalf("owner Update: %v", err)
	}
	if up.Name != "Renamed" || up.Visibility != "shared" {
		t.Errorf("update did not apply: %+v", up)
	}
	// Admin may update someone else's diagram.
	if _, err := s.Update(ctx, d.ID, "carol", true, "AdminEdit", "private", ""); err != nil {
		t.Errorf("admin Update: %v", err)
	}
}

func TestDiagram_DeleteOwnerOnly(t *testing.T) {
	t.Parallel()
	s := newDiagramStore(t)
	ctx := context.Background()
	d, _ := s.Create(ctx, "alice", "Name", "private", "")

	if err := s.Delete(ctx, d.ID, "bob", false); !errors.Is(err, ErrDiagramForbidden) {
		t.Errorf("non-owner Delete: want ErrDiagramForbidden, got %v", err)
	}
	if err := s.Delete(ctx, d.ID, "alice", false); err != nil {
		t.Fatalf("owner Delete: %v", err)
	}
	if _, err := s.Get(ctx, d.ID, "alice", false); !errors.Is(err, ErrDiagramNotFound) {
		t.Errorf("after delete: want ErrDiagramNotFound, got %v", err)
	}
}

func TestDiagram_ValidationRejectsBadPayload(t *testing.T) {
	t.Parallel()
	s := newDiagramStore(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, "a", "", "private", ""); !errors.Is(err, ErrDiagramInvalid) {
		t.Error("empty name must be rejected")
	}
	if _, err := s.Create(ctx, "a", "n", "weird", ""); !errors.Is(err, ErrDiagramInvalid) {
		t.Error("bad visibility must be rejected")
	}
	if _, err := s.Create(ctx, "a", "n", "private", `{"series":[{"central":""}]}`); !errors.Is(err, ErrDiagramInvalid) {
		t.Error("series without central must be rejected")
	}
	// Too many series.
	big := `{"series":[` +
		`{"central":"c"},{"central":"c"},{"central":"c"},{"central":"c"},` +
		`{"central":"c"},{"central":"c"},{"central":"c"},{"central":"c"},{"central":"c"}]}`
	if _, err := s.Create(ctx, "a", "n", "private", big); !errors.Is(err, ErrDiagramInvalid) {
		t.Error("more than 8 series must be rejected")
	}
}

func TestDiagram_MultiCentralSeriesPersist(t *testing.T) {
	t.Parallel()
	s := newDiagramStore(t)
	ctx := context.Background()
	cfg := `{"series":[{"central":"ccuA","parameter":"P"},{"central":"ccuB","parameter":"Q"}]}`
	d, err := s.Create(ctx, "alice", "Cross-CCU", "private", cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := s.Get(ctx, d.ID, "alice", false)
	if got.ConfigJSON != cfg {
		t.Errorf("cross-central series not preserved: %q", got.ConfigJSON)
	}
}
