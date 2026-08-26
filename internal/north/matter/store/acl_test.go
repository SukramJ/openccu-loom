// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package store_test

import (
	"context"
	"testing"

	store "github.com/SukramJ/openccu-loom/internal/north/matter/store"
)

// TestACL_ListEmpty verifies that ListACL on a fresh fabric returns an empty
// slice (not nil, not an error).
func TestACL_ListEmpty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 1)

	entries, err := s.ListACL(ctx, 1)
	if err != nil {
		t.Fatalf("ListACL: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("len=%d want 0", len(entries))
	}
}

// TestACL_ReplaceAssignsPositions verifies that ReplaceACL writes positions
// 0..n-1 in input order.
func TestACL_ReplaceAssignsPositions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 2)

	entries := []store.ACLEntry{
		{Privilege: store.PrivilegeView, AuthMode: store.AuthModeCASE, Subjects: []uint64{10}},
		{Privilege: store.PrivilegeOperate, AuthMode: store.AuthModeGroup, Subjects: []uint64{20}},
		{Privilege: store.PrivilegeAdminister, AuthMode: store.AuthModeCASE, Subjects: []uint64{30}},
	}
	if err := s.ReplaceACL(ctx, 1, entries); err != nil {
		t.Fatalf("ReplaceACL: %v", err)
	}

	got, err := s.ListACL(ctx, 1)
	if err != nil {
		t.Fatalf("ListACL: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	for i, e := range got {
		if e.Position != uint16(i) { //nolint:gosec // i is the loop index over a small fixed slice
			t.Errorf("got[%d].Position=%d want %d", i, e.Position, i)
		}
	}
}

// TestACL_ReplaceIsAtomic verifies that a second ReplaceACL fully overwrites
// the first (atomically).
func TestACL_ReplaceIsAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 3)

	first := []store.ACLEntry{
		{Privilege: store.PrivilegeView, AuthMode: store.AuthModeCASE, Subjects: []uint64{1, 2, 3}},
		{Privilege: store.PrivilegeManage, AuthMode: store.AuthModeCASE, Subjects: []uint64{4}},
	}
	if err := s.ReplaceACL(ctx, 1, first); err != nil {
		t.Fatalf("first ReplaceACL: %v", err)
	}

	second := []store.ACLEntry{
		{Privilege: store.PrivilegeAdminister, AuthMode: store.AuthModeCASE, Subjects: []uint64{100}},
	}
	if err := s.ReplaceACL(ctx, 1, second); err != nil {
		t.Fatalf("second ReplaceACL: %v", err)
	}

	got, err := s.ListACL(ctx, 1)
	if err != nil {
		t.Fatalf("ListACL: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1 (previous 2 entries must be gone)", len(got))
	}
	if got[0].Privilege != store.PrivilegeAdminister {
		t.Errorf("Privilege=%d want Administer", got[0].Privilege)
	}
}

// TestACL_SubjectsWithNilTargets verifies an ACE that has subjects but no
// targets.
func TestACL_SubjectsWithNilTargets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 4)

	entries := []store.ACLEntry{
		{
			Privilege: store.PrivilegeOperate,
			AuthMode:  store.AuthModeCASE,
			Subjects:  []uint64{11, 22, 33},
			Targets:   nil,
		},
	}
	if err := s.ReplaceACL(ctx, 1, entries); err != nil {
		t.Fatalf("ReplaceACL: %v", err)
	}

	got, err := s.ListACL(ctx, 1)
	if err != nil {
		t.Fatalf("ListACL: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	e := got[0]
	if len(e.Subjects) != 3 || e.Subjects[0] != 11 || e.Subjects[1] != 22 || e.Subjects[2] != 33 {
		t.Errorf("Subjects=%v want [11 22 33]", e.Subjects)
	}
	if e.Targets != nil {
		t.Errorf("Targets=%v want nil", e.Targets)
	}
}

// TestACL_SubjectsWithMultipleTargets verifies an ACE with subjects and
// multiple targets.
func TestACL_SubjectsWithMultipleTargets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 5)

	entries := []store.ACLEntry{
		{
			Privilege: store.PrivilegeManage,
			AuthMode:  store.AuthModeGroup,
			Subjects:  []uint64{50, 51},
			Targets: []store.ACLTarget{
				{Cluster: new(uint32(0x0006)), Endpoint: new(uint16(1))},
				{Cluster: new(uint32(0x0008)), DeviceType: new(uint32(0x0100))},
			},
		},
	}
	if err := s.ReplaceACL(ctx, 1, entries); err != nil {
		t.Fatalf("ReplaceACL: %v", err)
	}

	got, err := s.ListACL(ctx, 1)
	if err != nil {
		t.Fatalf("ListACL: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	if len(got[0].Targets) != 2 {
		t.Fatalf("Targets len=%d want 2", len(got[0].Targets))
	}
	if got[0].Targets[0].Cluster == nil || *got[0].Targets[0].Cluster != 0x0006 {
		t.Errorf("Targets[0].Cluster=%v want 0x0006", got[0].Targets[0].Cluster)
	}
	if got[0].Targets[0].Endpoint == nil || *got[0].Targets[0].Endpoint != 1 {
		t.Errorf("Targets[0].Endpoint=%v want 1", got[0].Targets[0].Endpoint)
	}
	if got[0].Targets[1].DeviceType == nil || *got[0].Targets[1].DeviceType != 0x0100 {
		t.Errorf("Targets[1].DeviceType=%v want 0x0100", got[0].Targets[1].DeviceType)
	}
}

// TestACL_TargetPointerFieldsRoundTrip verifies JSON round-trip for
// ACLTarget pointer fields with various nil/non-nil combinations.
func TestACL_TargetPointerFieldsRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 6)

	entries := []store.ACLEntry{
		{
			Privilege: store.PrivilegeView,
			AuthMode:  store.AuthModeCASE,
			Subjects:  []uint64{1},
			Targets: []store.ACLTarget{
				// All fields set.
				{Cluster: new(uint32(1)), Endpoint: new(uint16(2)), DeviceType: new(uint32(3))},
				// Only Cluster.
				{Cluster: new(uint32(4)), Endpoint: nil, DeviceType: nil},
				// Only Endpoint.
				{Cluster: nil, Endpoint: new(uint16(5)), DeviceType: nil},
				// Only DeviceType.
				{Cluster: nil, Endpoint: nil, DeviceType: new(uint32(6))},
				// All nil.
				{Cluster: nil, Endpoint: nil, DeviceType: nil},
			},
		},
	}
	if err := s.ReplaceACL(ctx, 1, entries); err != nil {
		t.Fatalf("ReplaceACL: %v", err)
	}

	got, err := s.ListACL(ctx, 1)
	if err != nil {
		t.Fatalf("ListACL: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	targets := got[0].Targets
	if len(targets) != 5 {
		t.Fatalf("targets len=%d want 5", len(targets))
	}

	// All fields set.
	if targets[0].Cluster == nil || *targets[0].Cluster != 1 {
		t.Errorf("targets[0].Cluster=%v want 1", targets[0].Cluster)
	}
	if targets[0].Endpoint == nil || *targets[0].Endpoint != 2 {
		t.Errorf("targets[0].Endpoint=%v want 2", targets[0].Endpoint)
	}
	if targets[0].DeviceType == nil || *targets[0].DeviceType != 3 {
		t.Errorf("targets[0].DeviceType=%v want 3", targets[0].DeviceType)
	}

	// Only Cluster.
	if targets[1].Cluster == nil || *targets[1].Cluster != 4 {
		t.Errorf("targets[1].Cluster=%v want 4", targets[1].Cluster)
	}
	if targets[1].Endpoint != nil {
		t.Errorf("targets[1].Endpoint=%v want nil", targets[1].Endpoint)
	}
	if targets[1].DeviceType != nil {
		t.Errorf("targets[1].DeviceType=%v want nil", targets[1].DeviceType)
	}

	// All nil.
	if targets[4].Cluster != nil || targets[4].Endpoint != nil || targets[4].DeviceType != nil {
		t.Errorf("targets[4] fields expected all nil, got %+v", targets[4])
	}
}

// TestACL_PrivilegeAndAuthModeRoundTrip verifies every Privilege and AuthMode
// value survives a write/read cycle.
func TestACL_PrivilegeAndAuthModeRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 7)

	type combo struct {
		priv store.Privilege
		auth store.AuthMode
	}
	combos := []combo{
		{store.PrivilegeView, store.AuthModePASE},
		{store.PrivilegeProxyView, store.AuthModeCASE},
		{store.PrivilegeOperate, store.AuthModeGroup},
		{store.PrivilegeManage, store.AuthModeCASE},
		{store.PrivilegeAdminister, store.AuthModeCASE},
	}

	entries := make([]store.ACLEntry, 0, len(combos))
	for i, c := range combos {
		entries = append(entries, store.ACLEntry{
			Privilege: c.priv,
			AuthMode:  c.auth,
			Subjects:  []uint64{uint64(i) + 1},
		})
	}
	if err := s.ReplaceACL(ctx, 1, entries); err != nil {
		t.Fatalf("ReplaceACL: %v", err)
	}

	got, err := s.ListACL(ctx, 1)
	if err != nil {
		t.Fatalf("ListACL: %v", err)
	}
	if len(got) != len(combos) {
		t.Fatalf("len=%d want %d", len(got), len(combos))
	}
	for i, c := range combos {
		if got[i].Privilege != c.priv {
			t.Errorf("[%d] Privilege=%d want %d", i, got[i].Privilege, c.priv)
		}
		if got[i].AuthMode != c.auth {
			t.Errorf("[%d] AuthMode=%d want %d", i, got[i].AuthMode, c.auth)
		}
	}
}

// TestACL_RemoveFabricCascades verifies that removing a fabric deletes its
// ACL entries.
func TestACL_RemoveFabricCascades(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	s := store.New(db)
	addTestFabric(t, s, 1, 8)

	entries := []store.ACLEntry{
		{Privilege: store.PrivilegeAdminister, AuthMode: store.AuthModeCASE, Subjects: []uint64{999}},
	}
	if err := s.ReplaceACL(ctx, 1, entries); err != nil {
		t.Fatalf("ReplaceACL: %v", err)
	}

	// Verify ACL row exists.
	var n int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM matter_acl_entries WHERE fabric_index = 1`,
	).Scan(&n); err != nil {
		t.Fatalf("count before remove: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 ACL row before remove, got %d", n)
	}

	if err := s.RemoveFabric(ctx, 1); err != nil {
		t.Fatalf("RemoveFabric: %v", err)
	}

	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM matter_acl_entries WHERE fabric_index = 1`,
	).Scan(&n); err != nil {
		t.Fatalf("count after remove: %v", err)
	}
	if n != 0 {
		t.Errorf("CASCADE failed: %d ACL rows remain after fabric removal", n)
	}
}

// TestACL_ReplaceWithNilTargets verifies ReplaceACL with a mix of entries:
// one with subjects, one with nil targets and a target entry.
func TestACL_ReplaceWithNilTargets(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 0x31)

	entries := []store.ACLEntry{
		{Privilege: store.PrivilegeView, AuthMode: store.AuthModeCASE, Subjects: []uint64{0x11}},
		{Privilege: store.PrivilegeManage, AuthMode: store.AuthModeGroup, Subjects: nil, Targets: []store.ACLTarget{
			{Endpoint: new(uint16(5))},
		}},
	}
	if err := s.ReplaceACL(ctx, 1, entries); err != nil {
		t.Fatalf("ReplaceACL: %v", err)
	}

	list, err := s.ListACL(ctx, 1)
	if err != nil {
		t.Fatalf("ListACL: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len=%d want 2", len(list))
	}
	// Position ordering must be preserved.
	if list[0].Privilege != store.PrivilegeView {
		t.Errorf("list[0].Privilege=%d want PrivilegeView", list[0].Privilege)
	}
	if list[1].AuthMode != store.AuthModeGroup {
		t.Errorf("list[1].AuthMode=%d want AuthModeGroup", list[1].AuthMode)
	}
}

// TestACL_ReplaceWipesPrevious verifies that ReplaceACL atomically wipes
// previous entries.
func TestACL_ReplaceWipesPrevious(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := store.New(openTestDB(t))
	addTestFabric(t, s, 1, 0x32)

	// Insert 3 entries.
	first := []store.ACLEntry{
		{Privilege: store.PrivilegeAdminister, AuthMode: store.AuthModeCASE, Subjects: []uint64{1, 2, 3}},
		{Privilege: store.PrivilegeOperate, AuthMode: store.AuthModePASE, Subjects: []uint64{4}},
		{Privilege: store.PrivilegeView, AuthMode: store.AuthModeGroup},
	}
	if err := s.ReplaceACL(ctx, 1, first); err != nil {
		t.Fatalf("ReplaceACL first: %v", err)
	}

	// Replace with 1 entry.
	second := []store.ACLEntry{
		{Privilege: store.PrivilegeProxyView, AuthMode: store.AuthModeCASE, Subjects: []uint64{9}},
	}
	if err := s.ReplaceACL(ctx, 1, second); err != nil {
		t.Fatalf("ReplaceACL second: %v", err)
	}

	list, err := s.ListACL(ctx, 1)
	if err != nil {
		t.Fatalf("ListACL: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len=%d want 1 (atomic replacement)", len(list))
	}
	if list[0].Privilege != store.PrivilegeProxyView {
		t.Errorf("Privilege=%d want PrivilegeProxyView", list[0].Privilege)
	}
}
