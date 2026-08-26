// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func newGKM(t *testing.T) *core.GroupKeyManagement {
	t.Helper()
	gkm, err := core.NewGroupKeyManagement(newFakeStore(), core.GroupKeyMgmtConfig{})
	if err != nil {
		t.Fatalf("NewGroupKeyManagement: %v", err)
	}
	return gkm
}

func TestGKM_ClusterID(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	if got := gkm.MatterClusterID(); got != 0x003F {
		t.Fatalf("MatterClusterID = 0x%04X, want 0x003F", got)
	}
}

func TestGKM_ClusterRevision(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	v, ok := gkm.MatterRead(cluster.AttrGlobalClusterRevision)
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 3 {
		t.Fatalf("ClusterRevision = %v, want 3", v)
	}
}

func TestGKM_NewGroupKeyManagement_NilStore(t *testing.T) {
	t.Parallel()
	_, err := core.NewGroupKeyManagement(nil, core.GroupKeyMgmtConfig{})
	if err == nil {
		t.Fatal("expected error for nil store, got nil")
	}
}

func TestGKM_MaxGroupsPerFabric_Default(t *testing.T) {
	t.Parallel()
	// cfg value 0 → matter.js default 22
	// (GroupKeyManagementServer.ts:616 override maxGroupsPerFabric = 22).
	gkm, err := core.NewGroupKeyManagement(newFakeStore(), core.GroupKeyMgmtConfig{MaxGroupsPerFabric: 0})
	if err != nil {
		t.Fatalf("NewGroupKeyManagement: %v", err)
	}
	v, ok := gkm.MatterRead(0x0002)
	if !ok {
		t.Fatal("MaxGroupsPerFabric: ok=false")
	}
	if v.(uint16) != 22 {
		t.Fatalf("MaxGroupsPerFabric = %d, want 22", v.(uint16))
	}
}

func TestGKM_MaxGroupKeysPerFabric_Default(t *testing.T) {
	t.Parallel()
	// cfg value 0 → matter.js default 20
	// (GroupKeyManagementServer.ts:531).
	gkm, err := core.NewGroupKeyManagement(newFakeStore(), core.GroupKeyMgmtConfig{MaxGroupKeysPerFabric: 0})
	if err != nil {
		t.Fatalf("NewGroupKeyManagement: %v", err)
	}
	v, ok := gkm.MatterRead(0x0003)
	if !ok {
		t.Fatal("MaxGroupKeysPerFabric: ok=false")
	}
	if v.(uint16) != 20 {
		t.Fatalf("MaxGroupKeysPerFabric = %d, want 20", v.(uint16))
	}
}

func TestGKM_ReadMaxGroupsPerFabric(t *testing.T) {
	t.Parallel()
	gkm, _ := core.NewGroupKeyManagement(newFakeStore(), core.GroupKeyMgmtConfig{MaxGroupsPerFabric: 32})
	v, ok := gkm.MatterRead(0x0002)
	if !ok {
		t.Fatal("MaxGroupsPerFabric: ok=false")
	}
	if v.(uint16) != 32 {
		t.Fatalf("MaxGroupsPerFabric = %d, want 32", v.(uint16))
	}
}

func TestGKM_ReadMaxGroupKeysPerFabric(t *testing.T) {
	t.Parallel()
	gkm, _ := core.NewGroupKeyManagement(newFakeStore(), core.GroupKeyMgmtConfig{MaxGroupKeysPerFabric: 8})
	v, ok := gkm.MatterRead(0x0003)
	if !ok {
		t.Fatal("MaxGroupKeysPerFabric: ok=false")
	}
	if v.(uint16) != 8 {
		t.Fatalf("MaxGroupKeysPerFabric = %d, want 8", v.(uint16))
	}
}

func TestGKM_ReadGroupKeyMapInitiallyEmpty(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	v, ok := gkm.MatterRead(0x0000) // groupKeyMgmtAttrGroupKeyMap
	if !ok {
		t.Fatal("GroupKeyMap: ok=false")
	}
	mappings := v.([]core.GroupKeyMapStruct)
	if len(mappings) != 0 {
		t.Fatalf("GroupKeyMap initial len=%d, want 0", len(mappings))
	}
}

func TestGKM_ReadGroupTableInitiallyEmpty(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	v, ok := gkm.MatterRead(0x0001) // groupKeyMgmtAttrGroupTable
	if !ok {
		t.Fatal("GroupTable: ok=false")
	}
	table := v.([]core.GroupInfoMapStruct)
	if len(table) != 0 {
		t.Fatalf("GroupTable initial len=%d, want 0", len(table))
	}
}

func TestGKM_SetCurrentFabric(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	gkm.SetCurrentFabric(3)
	// Invoke should now proceed (fabric != 0).
	_, err := gkm.MatterInvoke(context.Background(), 0x04 /*KeySetReadAllIndices*/, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("KeySetReadAllIndices after SetCurrentFabric: %v", err)
	}
}

func TestGKM_InvokeWithoutFabric_Fails(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	// fabric == 0 (initial) → error.
	_, err := gkm.MatterInvoke(context.Background(), 0x04, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for invoke without active fabric, got nil")
	}
}

func TestGKM_KeySetWriteAndRead_RoundTrip(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	gkm.SetCurrentFabric(1)
	ctx := context.Background()

	req := core.KeySetWriteRequest{
		GroupKeySet: core.GroupKeySetStruct{
			GroupKeySetID:          42,
			GroupKeySecurityPolicy: 0,
			EpochKey0:              make([]byte, 16), // 16-byte AES-128 key per spec
			EpochStartTime0:        100,
		},
	}
	_, err := gkm.MatterInvoke(ctx, 0x00 /*KeySetWrite*/, req, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("KeySetWrite: %v", err)
	}

	resp, err := gkm.MatterInvoke(ctx, 0x01 /*KeySetRead*/, core.KeySetReadRequest{GroupKeySetID: 42}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("KeySetRead: %v", err)
	}

	kr := resp.(core.KeySetReadResponse)
	if kr.GroupKeySet.GroupKeySetID != 42 {
		t.Errorf("GroupKeySetID = %d, want 42", kr.GroupKeySet.GroupKeySetID)
	}
	if kr.GroupKeySet.GroupKeySecurityPolicy != 0 {
		t.Errorf("GroupKeySecurityPolicy = %d, want 0", kr.GroupKeySet.GroupKeySecurityPolicy)
	}
	// Per spec, epoch keys must be nil in the response.
	if kr.GroupKeySet.EpochKey0 != nil {
		t.Errorf("EpochKey0 must be nil in response, got %v", kr.GroupKeySet.EpochKey0)
	}
	if kr.GroupKeySet.EpochKey1 != nil {
		t.Errorf("EpochKey1 must be nil in response, got %v", kr.GroupKeySet.EpochKey1)
	}
	if kr.GroupKeySet.EpochKey2 != nil {
		t.Errorf("EpochKey2 must be nil in response, got %v", kr.GroupKeySet.EpochKey2)
	}
}

func TestGKM_KeySetRead_Miss(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	gkm.SetCurrentFabric(1)
	_, err := gkm.MatterInvoke(context.Background(), 0x01, core.KeySetReadRequest{GroupKeySetID: 9999}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for KeySetRead miss, got nil")
	}
}

func TestGKM_KeySetRemove(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	gkm.SetCurrentFabric(1)
	ctx := context.Background()

	// Write first.
	_, _ = gkm.MatterInvoke(ctx, 0x00, core.KeySetWriteRequest{
		GroupKeySet: core.GroupKeySetStruct{GroupKeySetID: 7, EpochKey0: make([]byte, 16), EpochStartTime0: 1},
	}, hmenum.CommandPriorityHigh)

	// Remove.
	_, err := gkm.MatterInvoke(ctx, 0x03, core.KeySetRemoveRequest{GroupKeySetID: 7}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("KeySetRemove: %v", err)
	}

	// Read after remove must miss.
	_, err = gkm.MatterInvoke(ctx, 0x01, core.KeySetReadRequest{GroupKeySetID: 7}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for KeySetRead after remove, got nil")
	}
}

// TestGKM_KeySetRead_NotFoundStatusCode pins that KeySetRead of a group
// key set id that was never written returns a typed [im.StatusCodeError]
// mapping to NotFound (0x8b), not a generic error. Mirrors matter.js
// GroupKeyManagementServer.ts throwing Status.NotFound for a missing
// GroupKeySetID.
func TestGKM_KeySetRead_NotFoundStatusCode(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	gkm.SetCurrentFabric(1)

	_, err := gkm.MatterInvoke(context.Background(), 0x01 /*KeySetRead*/, core.KeySetReadRequest{GroupKeySetID: 9999}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("KeySetRead of never-written id: expected error, got nil")
	}
	type statusCoder interface{ MatterStatusCode() im.StatusCode }
	var sc statusCoder
	if !errors.As(err, &sc) {
		t.Fatalf("KeySetRead error %v does not implement MatterStatusCode()", err)
	}
	if got := sc.MatterStatusCode(); got != im.StatusNotFound {
		t.Errorf("MatterStatusCode() = 0x%02X, want 0x%02X (NotFound)", uint8(got), uint8(im.StatusNotFound))
	}
}

// TestGKM_KeySetRemove_NotFoundStatusCode pins that KeySetRemove of a
// group key set id that was never written returns a typed
// [im.StatusCodeError] mapping to NotFound (0x8b) instead of succeeding
// silently. id 0 is the IPK and is rejected for a different reason
// (InvalidCommand), so this uses a non-zero id.
func TestGKM_KeySetRemove_NotFoundStatusCode(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	gkm.SetCurrentFabric(1)

	_, err := gkm.MatterInvoke(context.Background(), 0x03 /*KeySetRemove*/, core.KeySetRemoveRequest{GroupKeySetID: 55}, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("KeySetRemove of never-written id: expected error, got nil")
	}
	type statusCoder interface{ MatterStatusCode() im.StatusCode }
	var sc statusCoder
	if !errors.As(err, &sc) {
		t.Fatalf("KeySetRemove error %v does not implement MatterStatusCode()", err)
	}
	if got := sc.MatterStatusCode(); got != im.StatusNotFound {
		t.Errorf("MatterStatusCode() = 0x%02X, want 0x%02X (NotFound)", uint8(got), uint8(im.StatusNotFound))
	}
}

func TestGKM_KeySetReadAllIndices(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	gkm.SetCurrentFabric(1)
	ctx := context.Background()

	for _, id := range []uint16{10, 20, 30} {
		_, _ = gkm.MatterInvoke(ctx, 0x00, core.KeySetWriteRequest{
			GroupKeySet: core.GroupKeySetStruct{GroupKeySetID: id, EpochKey0: make([]byte, 16), EpochStartTime0: 1},
		}, hmenum.CommandPriorityHigh)
	}

	resp, err := gkm.MatterInvoke(ctx, 0x04, nil, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("KeySetReadAllIndices: %v", err)
	}
	allResp := resp.(core.KeySetReadAllIndicesResponse)
	if len(allResp.GroupKeySetIDs) != 3 {
		t.Fatalf("GroupKeySetIDs len=%d, want 3", len(allResp.GroupKeySetIDs))
	}
}

func TestGKM_WriteGroupKeyMap(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	gkm.SetCurrentFabric(2)
	ctx := context.Background()

	mappings := []core.GroupKeyMapStruct{
		{GroupID: 100, GroupKeySetID: 1, FabricIndex: 2},
		{GroupID: 200, GroupKeySetID: 2, FabricIndex: 2},
	}
	if err := gkm.MatterWrite(ctx, 0x0000, mappings, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite GroupKeyMap: %v", err)
	}

	v, ok := gkm.MatterRead(0x0000)
	if !ok {
		t.Fatal("GroupKeyMap: ok=false after write")
	}
	got := v.([]core.GroupKeyMapStruct)
	if len(got) != 2 {
		t.Fatalf("GroupKeyMap len=%d, want 2", len(got))
	}
}

func TestGKM_WriteGroupKeyMap_CrossFabric(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	gkm.SetCurrentFabric(1)
	ctx := context.Background()

	// Entry with FabricIndex != currentFabric → error.
	mappings := []core.GroupKeyMapStruct{
		{GroupID: 100, GroupKeySetID: 1, FabricIndex: 2}, // cross-fabric
	}
	err := gkm.MatterWrite(ctx, 0x0000, mappings, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for cross-fabric GroupKeyMap write, got nil")
	}
}

func TestGKM_Write_ReadOnly(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	err := gkm.MatterWrite(context.Background(), 0x0001 /*GroupTable — read-only*/, nil, hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("expected error for write to read-only attr, got nil")
	}
}

func TestGKM_KeySetWriteValidation(t *testing.T) {
	t.Parallel()

	// key0 is a valid 16-byte epoch key used throughout the table cases.
	key0 := []byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	}
	key1 := []byte{
		0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
		0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20,
	}
	key2 := []byte{
		0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28,
		0x29, 0x2a, 0x2b, 0x2c, 0x2d, 0x2e, 0x2f, 0x30,
	}

	const max64 = uint64(0xFFFFFFFFFFFFFFFF)

	cases := []struct {
		name          string
		gks           core.GroupKeySetStruct
		wantError     bool
		wantSubstring string
	}{
		// ── Rejection cases ──────────────────────────────────────────────
		{
			name: "rule1_key0_empty",
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 0,
				EpochKey0:              nil, // empty
				EpochStartTime0:        100,
			},
			wantError:     true,
			wantSubstring: "invalid command argument",
		},
		{
			name: "rule1_starttime0_zero",
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 0,
				EpochKey0:              key0,
				EpochStartTime0:        0, // zero
			},
			wantError:     true,
			wantSubstring: "invalid command argument",
		},
		{
			name: "rule2_starttime0_equals_ipk_default",
			// ipkDefaultEpochStartTime == 0, and rule1 already catches 0.
			// The only way to trigger rule2 independently is if
			// EpochStartTime0 is exactly ipkDefaultEpochStartTime (0).
			// Because rule1 fires first on EpochKey0==nil, supply key0.
			// EpochStartTime0 == 0 triggers both rule1 and rule2; rule1
			// fires first but either way the sentinel substring matches.
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 0,
				EpochKey0:              key0,
				EpochStartTime0:        0, // <= ipkDefaultEpochStartTime
			},
			wantError:     true,
			wantSubstring: "invalid command argument",
		},
		{
			name: "rule3_key1_set_starttime1_zero",
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 0,
				EpochKey0:              key0,
				EpochStartTime0:        100,
				EpochKey1:              key1,
				EpochStartTime1:        0, // missing
			},
			wantError:     true,
			wantSubstring: "invalid command argument",
		},
		{
			name: "rule3_key1_set_starttime1_not_greater_than_starttime0",
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 0,
				EpochKey0:              key0,
				EpochStartTime0:        200,
				EpochKey1:              key1,
				EpochStartTime1:        100, // <= EpochStartTime0
			},
			wantError:     true,
			wantSubstring: "invalid command argument",
		},
		{
			name: "rule4_starttime1_set_key1_missing",
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 0,
				EpochKey0:              key0,
				EpochStartTime0:        100,
				EpochKey1:              nil, // missing
				EpochStartTime1:        200, // set without key
			},
			wantError:     true,
			wantSubstring: "invalid command argument",
		},
		{
			name: "rule5_key2_set_key1_missing",
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 0,
				EpochKey0:              key0,
				EpochStartTime0:        100,
				EpochKey1:              nil, // missing
				EpochStartTime1:        0,
				EpochKey2:              key2, // set without key1
				EpochStartTime2:        300,
			},
			wantError:     true,
			wantSubstring: "invalid command argument",
		},
		{
			name: "rule6_key2_set_starttime2_not_greater_than_starttime1",
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 0,
				EpochKey0:              key0,
				EpochStartTime0:        100,
				EpochKey1:              key1,
				EpochStartTime1:        200,
				EpochKey2:              key2,
				EpochStartTime2:        150, // <= EpochStartTime1
			},
			wantError:     true,
			wantSubstring: "invalid command argument",
		},
		{
			name: "rule7_starttime2_set_key2_missing",
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 0,
				EpochKey0:              key0,
				EpochStartTime0:        100,
				EpochKey1:              key1,
				EpochStartTime1:        200,
				EpochKey2:              nil, // missing
				EpochStartTime2:        300, // set without key
			},
			wantError:     true,
			wantSubstring: "invalid command argument",
		},
		{
			name: "rule8_security_policy_not_trust_first",
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 1, // only TrustFirst (0) is accepted
				EpochKey0:              key0,
				EpochStartTime0:        100,
			},
			wantError:     true,
			wantSubstring: "invalid command argument",
		},

		// ── Success cases ─────────────────────────────────────────────────
		{
			name: "accept_minimal_key0_only",
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 0,
				EpochKey0:              key0,
				EpochStartTime0:        1,
			},
			wantError: false,
		},
		{
			name: "accept_max64_disables_slot1",
			// EpochStartTime1 == max64 disables slot 1 → treated as absent.
			// Even though EpochKey1 is set in the request, the sentinel
			// nulls it out before validation runs.
			gks: core.GroupKeySetStruct{
				GroupKeySecurityPolicy: 0,
				EpochKey0:              key0,
				EpochStartTime0:        100,
				EpochKey1:              key1, // will be nulled by sentinel
				EpochStartTime1:        max64,
			},
			wantError: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gkm := newGKM(t)
			gkm.SetCurrentFabric(1)
			_, err := gkm.MatterInvoke(
				context.Background(),
				0x00, // KeySetWrite
				core.KeySetWriteRequest{GroupKeySet: tc.gks},
				hmenum.CommandPriorityHigh,
			)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if tc.wantSubstring != "" && !containsStr(err.Error(), tc.wantSubstring) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantSubstring)
				}
			} else if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// keySetWriteFields builds a minimally valid KeySetWriteRequest for
// budget tests — only GroupKeySetID varies; EpochKey0/EpochStartTime0
// satisfy the mandatory-field validation so the request reaches
// enforceKeySetBudget.
func keySetWriteFields(id uint16) core.KeySetWriteRequest {
	return core.KeySetWriteRequest{
		GroupKeySet: core.GroupKeySetStruct{
			GroupKeySetID:          id,
			GroupKeySecurityPolicy: 0, // TrustFirst
			EpochKey0:              make([]byte, 16),
			EpochStartTime0:        1,
		},
	}
}

// mustBeResourceExhausted fails the test unless err is a typed
// [im.StatusCodeError] mapping to ResourceExhausted (0x89). Follows the
// statusCoder pattern used by TestGKM_KeySetRead_NotFoundStatusCode /
// TestGKM_KeySetRemove_NotFoundStatusCode above.
func mustBeResourceExhausted(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	type statusCoder interface{ MatterStatusCode() im.StatusCode }
	var sc statusCoder
	if !errors.As(err, &sc) {
		t.Fatalf("error %v does not implement MatterStatusCode()", err)
	}
	if got := sc.MatterStatusCode(); got != im.StatusResourceExhausted {
		t.Errorf("MatterStatusCode() = 0x%02X, want 0x%02X (ResourceExhausted)", uint8(got), uint8(im.StatusResourceExhausted))
	}
}

// TestGKM_KeySetWriteBudget_CapReached pins that KeySetWrite ADD
// operations succeed up to MaxGroupKeysPerFabric key sets and that the
// write which would push the fabric over the cap fails with a typed
// [im.StatusCodeError] mapping to ResourceExhausted (0x89), not a
// generic error. Mirrors matter.js
// GroupKeyManagementServer.ts:386-394, which counts the fabric's
// persisted key sets (state entries + 1 for the implicit IPK key set
// 0) and rejects once that count reaches maxGroupKeysPerFabric.
func TestGKM_KeySetWriteBudget_CapReached(t *testing.T) {
	t.Parallel()
	const budgetCap = 3
	gkm, err := core.NewGroupKeyManagement(newFakeStore(), core.GroupKeyMgmtConfig{MaxGroupKeysPerFabric: budgetCap})
	if err != nil {
		t.Fatalf("NewGroupKeyManagement: %v", err)
	}
	gkm.SetCurrentFabric(1)
	ctx := context.Background()

	// Writes up to the cap (three distinct new ids) must all succeed.
	for _, id := range []uint16{1, 2, 3} {
		if _, err := gkm.MatterInvoke(ctx, 0x00 /*KeySetWrite*/, keySetWriteFields(id), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("KeySetWrite id=%d: unexpected error: %v", id, err)
		}
	}

	// The next ADD (a new, fourth id) must be rejected: len(existing) ==
	// cap already.
	_, err = gkm.MatterInvoke(ctx, 0x00, keySetWriteFields(4), hmenum.CommandPriorityHigh)
	mustBeResourceExhausted(t, err)
}

// TestGKM_KeySetWriteBudget_UpdateAtCapSucceeds pins that an UPDATE of
// an already-existing key set id is always allowed, even when the
// fabric is already at its MaxGroupKeysPerFabric cap — only ADDing a
// new id is budget-limited. Mirrors matter.js
// GroupKeyManagementServer.ts:378-382 (existingIndex !== -1 branch
// updates in place, bypassing the budget check entirely).
func TestGKM_KeySetWriteBudget_UpdateAtCapSucceeds(t *testing.T) {
	t.Parallel()
	const budgetCap = 3
	gkm, err := core.NewGroupKeyManagement(newFakeStore(), core.GroupKeyMgmtConfig{MaxGroupKeysPerFabric: budgetCap})
	if err != nil {
		t.Fatalf("NewGroupKeyManagement: %v", err)
	}
	gkm.SetCurrentFabric(1)
	ctx := context.Background()

	for _, id := range []uint16{1, 2, 3} {
		if _, err := gkm.MatterInvoke(ctx, 0x00, keySetWriteFields(id), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("KeySetWrite id=%d: unexpected error: %v", id, err)
		}
	}

	// Re-writing an existing id (an UPDATE, not an ADD) at the cap must
	// still succeed.
	if _, err := gkm.MatterInvoke(ctx, 0x00, keySetWriteFields(2), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("KeySetWrite update of existing id=2 at cap: unexpected error: %v", err)
	}

	// A genuinely new id at the cap must still be rejected.
	_, err = gkm.MatterInvoke(ctx, 0x00, keySetWriteFields(9), hmenum.CommandPriorityHigh)
	mustBeResourceExhausted(t, err)
}

// TestGKM_KeySetWriteBudget_CrossFabricIsolation pins that the budget
// is enforced per fabric: a fabric already at its cap must not block a
// different fabric's KeySetWrite. Mirrors matter.js
// GroupKeyManagementServer.ts:386-394, which filters
// state.groupKeySets by fabricIndex before counting — the same
// per-fabric scoping [GroupStoreFacade.ListGroupKeySets] applies here.
func TestGKM_KeySetWriteBudget_CrossFabricIsolation(t *testing.T) {
	t.Parallel()
	const budgetCap = 3
	gkm, err := core.NewGroupKeyManagement(newFakeStore(), core.GroupKeyMgmtConfig{MaxGroupKeysPerFabric: budgetCap})
	if err != nil {
		t.Fatalf("NewGroupKeyManagement: %v", err)
	}
	ctxA := im.WithFabricFilter(context.Background(), true, 1)
	ctxB := im.WithFabricFilter(context.Background(), true, 2)

	// Fill fabric A to its cap.
	for _, id := range []uint16{1, 2, 3} {
		if _, err := gkm.MatterInvoke(ctxA, 0x00, keySetWriteFields(id), hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("fabric A KeySetWrite id=%d: unexpected error: %v", id, err)
		}
	}
	// Fabric A is now exhausted.
	_, err = gkm.MatterInvoke(ctxA, 0x00, keySetWriteFields(4), hmenum.CommandPriorityHigh)
	mustBeResourceExhausted(t, err)

	// Fabric B, still empty, must accept its own writes.
	if _, err := gkm.MatterInvoke(ctxB, 0x00, keySetWriteFields(1), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("fabric B KeySetWrite id=1: unexpected error, cross-fabric budget leaked: %v", err)
	}
}

// containsStr is a small helper to avoid importing strings in the test file.
func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || sub == "" ||
		func() bool {
			for i := 0; i <= len(s)-len(sub); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// ─── GroupKeyMap fabric-filter ────────────────────────────────────────────────

// TestGKM_MatterReadFiltered_GroupKeyMap_FabricScoped verifies that
// MatterReadFiltered(ctx, GroupKeyMap) returns only the mappings for the
// fabric carried in the IM context, not those belonging to a different
// fabric. The old MatterRead path used g.currentFabric (always 0 in
// production) and returned an empty list for every CASE session.
//
// Mirrors matter.js GroupKeyManagementServer.ts:103-115 — GroupKeyMap
// read uses context.session.associatedFabric as the per-fabric filter.
func TestGKM_MatterReadFiltered_GroupKeyMap_FabricScoped(t *testing.T) {
	t.Parallel()

	fs := newFakeStore()
	gkm, err := core.NewGroupKeyManagement(fs, core.GroupKeyMgmtConfig{})
	if err != nil {
		t.Fatalf("NewGroupKeyManagement: %v", err)
	}

	// Pre-populate a KeySet and a mapping for fabric 2.
	ksBuf := core.KeySetWriteRequest{
		GroupKeySet: core.GroupKeySetStruct{
			GroupKeySetID:          1,
			GroupKeySecurityPolicy: 0, // TrustFirst
			EpochKey0:              make([]byte, 16),
			EpochStartTime0:        1000,
		},
	}
	ctx2 := im.WithFabricFilter(context.Background(), true, 2)
	if _, err := gkm.MatterInvoke(ctx2, 0x00 /* KeySetWrite */, ksBuf, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("KeySetWrite fabric 2: %v", err)
	}
	// Write GroupKeyMap for fabric 2 using MatterWrite.
	if err := gkm.MatterWrite(ctx2, 0x0000, []core.GroupKeyMapStruct{
		{GroupID: 10, GroupKeySetID: 1, FabricIndex: 2},
	}, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("MatterWrite GroupKeyMap fabric 2: %v", err)
	}

	// Also write a mapping for fabric 3.
	ksBuf3 := core.KeySetWriteRequest{
		GroupKeySet: core.GroupKeySetStruct{
			GroupKeySetID:          2,
			GroupKeySecurityPolicy: 0,
			EpochKey0:              make([]byte, 16),
			EpochStartTime0:        2000,
		},
	}
	ctx3 := im.WithFabricFilter(context.Background(), true, 3)
	if _, err := gkm.MatterInvoke(ctx3, 0x00, ksBuf3, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("KeySetWrite fabric 3: %v", err)
	}
	if err := gkm.MatterWrite(ctx3, 0x0000, []core.GroupKeyMapStruct{
		{GroupID: 20, GroupKeySetID: 2, FabricIndex: 3},
	}, hmenum.CommandPriorityLow); err != nil {
		t.Fatalf("MatterWrite GroupKeyMap fabric 3: %v", err)
	}

	// MatterReadFiltered for fabric 2 must return only fabric-2 entries.
	v2, ok := gkm.MatterReadFiltered(ctx2, 0x0000)
	if !ok {
		t.Fatal("MatterReadFiltered fabric 2 GroupKeyMap: ok=false")
	}
	list2, isSlice := v2.([]core.GroupKeyMapStruct)
	if !isSlice {
		t.Fatalf("MatterReadFiltered returned %T, want []GroupKeyMapStruct", v2)
	}
	if len(list2) != 1 || list2[0].GroupID != 10 {
		t.Errorf("fabric 2: got %v, want [{GroupID:10 ...}]", list2)
	}

	// MatterReadFiltered for fabric 3 must return only fabric-3 entries.
	v3, ok := gkm.MatterReadFiltered(ctx3, 0x0000)
	if !ok {
		t.Fatal("MatterReadFiltered fabric 3 GroupKeyMap: ok=false")
	}
	list3 := v3.([]core.GroupKeyMapStruct)
	if len(list3) != 1 || list3[0].GroupID != 20 {
		t.Errorf("fabric 3: got %v, want [{GroupID:20 ...}]", list3)
	}
}

// TestGKM_MatterRead_GroupKeyMap_FabricZeroFallback verifies that
// MatterRead (no fabric context) falls back to g.currentFabric=0 and
// returns an empty list for fabric 0. This is acceptable for test paths
// that do not stamp the IM context.
func TestGKM_MatterRead_GroupKeyMap_FabricZeroFallback(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	v, ok := gkm.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead GroupKeyMap: ok=false")
	}
	list, isSlice := v.([]core.GroupKeyMapStruct)
	if !isSlice {
		t.Fatalf("MatterRead returned %T, want []GroupKeyMapStruct", v)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list for fabric 0, got %d entries", len(list))
	}
}

func TestGroupKeyMgmt_MatterDataVersion(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	_ = gkm.MatterDataVersion() // must not panic
}

func TestGroupKeyMgmt_MatterReportable(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	list := gkm.MatterReportable()
	if len(list) == 0 {
		t.Fatal("MatterReportable() is empty")
	}
}

func TestGroupKeyMgmt_MatterAttributes(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	list := gkm.MatterAttributes()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	for _, want := range []uint32{0x0000, 0x0001} {
		if !have[want] {
			t.Errorf("MatterAttributes() missing attr 0x%04X", want)
		}
	}
}

func TestGroupKeyMgmt_MatterAcceptedCommands(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	list := gkm.MatterAcceptedCommands()
	if len(list) == 0 {
		t.Fatal("MatterAcceptedCommands() is empty")
	}
}

func TestGroupKeyMgmt_MatterGeneratedCommands(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	list := gkm.MatterGeneratedCommands()
	if len(list) == 0 {
		t.Fatal("MatterGeneratedCommands() is empty")
	}
}

// TestGKM_WriteGroupKeyMapRemovesDroppedBindings pins the replace
// semantics of Matter §11.2.10.4.1: writing the fabric-scoped
// GroupKeyMap list replaces it, so a binding the controller left out is
// unbound. Applying only the entries present leaves the dropped binding
// readable, and the next read then contradicts the write the bridge
// just acknowledged.
func TestGKM_WriteGroupKeyMapRemovesDroppedBindings(t *testing.T) {
	t.Parallel()
	gkm := newGKM(t)
	gkm.SetCurrentFabric(2)
	ctx := context.Background()

	initial := []core.GroupKeyMapStruct{
		{GroupID: 100, GroupKeySetID: 1, FabricIndex: 2},
		{GroupID: 200, GroupKeySetID: 2, FabricIndex: 2},
	}
	if err := gkm.MatterWrite(ctx, 0x0000, initial, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite GroupKeyMap (initial): %v", err)
	}

	// Controller unbinds group 100 by writing a shortened list.
	shortened := []core.GroupKeyMapStruct{{GroupID: 200, GroupKeySetID: 2, FabricIndex: 2}}
	if err := gkm.MatterWrite(ctx, 0x0000, shortened, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite GroupKeyMap (shortened): %v", err)
	}

	v, ok := gkm.MatterRead(0x0000)
	if !ok {
		t.Fatal("GroupKeyMap: ok=false after write")
	}
	got := v.([]core.GroupKeyMapStruct)
	if len(got) != 1 || got[0].GroupID != 200 {
		t.Fatalf("GroupKeyMap = %+v, want exactly the written binding for group 200", got)
	}

	// An empty write clears the whole fabric-scoped list.
	if err := gkm.MatterWrite(ctx, 0x0000, []core.GroupKeyMapStruct{}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite GroupKeyMap (empty): %v", err)
	}
	v, _ = gkm.MatterRead(0x0000)
	if got := v.([]core.GroupKeyMapStruct); len(got) != 0 {
		t.Fatalf("GroupKeyMap after empty write = %+v, want empty", got)
	}
}
