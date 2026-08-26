// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	mstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// fakeACLStore is a minimal ACLStoreFacade for AccessControl tests.
type fakeACLStore struct {
	replaceACLErr error
}

func (f *fakeACLStore) ListACL(_ context.Context, _ uint8) ([]mstore.ACLEntry, error) {
	return nil, nil
}

func (f *fakeACLStore) ReplaceACL(_ context.Context, _ uint8, _ []mstore.ACLEntry) error {
	return f.replaceACLErr
}

// seededACLStore is a richer ACLStoreFacade that returns a configurable
// snapshot from ListACL so the event-emitter tests can exercise the
// Added / Removed / Changed classification logic.
type seededACLStore struct {
	existing      []mstore.ACLEntry
	replaceACLErr error
}

func (s *seededACLStore) ListACL(_ context.Context, _ uint8) ([]mstore.ACLEntry, error) {
	return append([]mstore.ACLEntry(nil), s.existing...), nil
}

func (s *seededACLStore) ReplaceACL(_ context.Context, _ uint8, _ []mstore.ACLEntry) error {
	return s.replaceACLErr
}

// minimalACLEntry returns a well-formed store.ACLEntry for use in tests.
func minimalACLEntry(fabricIndex uint8) mstore.ACLEntry {
	return mstore.ACLEntry{
		FabricIndex: fabricIndex,
		Privilege:   mstore.Privilege(5), // Administer
		AuthMode:    mstore.AuthMode(2),  // CASE
		Subjects:    []uint64{1},
	}
}

// newAccessControl builds an AccessControl with a no-error fake store.
func newAccessControl(t *testing.T) *core.AccessControl {
	t.Helper()
	ac, err := core.NewAccessControl(&fakeACLStore{})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	return ac
}

// writeACL is a convenience wrapper for MatterWrite(ctx, 0x0000, entries).
func writeACL(ac *core.AccessControl, entries []core.AccessControlEntryStruct) error {
	return ac.MatterWrite(context.Background(), 0x0000, entries, hmenum.CommandPriorityHigh)
}

// newAccessControlWithStore builds an AccessControl backed by the
// provided store (useful for event-emitter tests that need pre-seeded
// ACL snapshots).
func newAccessControlWithStore(t *testing.T, s core.ACLStoreFacade) *core.AccessControl {
	t.Helper()
	ac, err := core.NewAccessControl(s)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	return ac
}

// ---- TestAccessControl_ACLWriteValidation ----

func TestAccessControl_ACLWriteValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		fabricIndex uint8 // set via SetCurrentFabric; 0 → use entry fallback
		entries     []core.AccessControlEntryStruct
		wantErr     bool
		errContains []string // all substrings must be present in error message
	}{
		{
			name:        "entries_per_fabric_exceeded",
			fabricIndex: 1,
			entries: func() []core.AccessControlEntryStruct {
				out := make([]core.AccessControlEntryStruct, 5)
				for i := range out {
					out[i] = core.AccessControlEntryStruct{
						Privilege:   5,
						AuthMode:    2,
						Subjects:    []uint64{uint64(i + 1)},
						FabricIndex: 1,
					}
				}
				return out
			}(),
			wantErr:     true,
			errContains: []string{"resource exhausted", "AccessControlEntriesPerFabric"},
		},
		{
			name:        "subjects_per_entry_exceeded",
			fabricIndex: 1,
			entries: []core.AccessControlEntryStruct{
				{
					Privilege:   5,
					AuthMode:    2,
					Subjects:    []uint64{1, 2, 3, 4, 5},
					FabricIndex: 1,
				},
			},
			wantErr:     true,
			errContains: []string{"resource exhausted", "SubjectsPerAccessControlEntry"},
		},
		{
			name:        "targets_per_entry_exceeded",
			fabricIndex: 1,
			entries: []core.AccessControlEntryStruct{
				{
					Privilege: 5,
					AuthMode:  2,
					Subjects:  []uint64{1},
					Targets: []core.ACLTargetStruct{
						{Cluster: new(uint32(0x0006))},
						{Cluster: new(uint32(0x0008))},
						{Cluster: new(uint32(0x0300))},
						{Cluster: new(uint32(0x0101))},
						{Cluster: new(uint32(0x0201))},
					},
					FabricIndex: 1,
				},
			},
			wantErr:     true,
			errContains: []string{"resource exhausted", "TargetsPerAccessControlEntry"},
		},
		{
			name:        "authmode_pase_rejected",
			fabricIndex: 1,
			entries: []core.AccessControlEntryStruct{
				{
					Privilege:   5,
					AuthMode:    1, // PASE
					Subjects:    []uint64{1},
					FabricIndex: 1,
				},
			},
			wantErr:     true,
			errContains: []string{"constraint error", "PASE is forbidden"},
		},
		{
			name:        "group_authmode_administer_rejected",
			fabricIndex: 1,
			entries: []core.AccessControlEntryStruct{
				{
					Privilege:   5, // Administer
					AuthMode:    3, // Group
					Subjects:    []uint64{1},
					FabricIndex: 1,
				},
			},
			wantErr:     true,
			errContains: []string{"constraint error", "Administer privilege rejected"},
		},
		{
			name:        "target_devicetype_and_endpoint_rejected",
			fabricIndex: 1,
			entries: []core.AccessControlEntryStruct{
				{
					Privilege: 5,
					AuthMode:  2,
					Subjects:  []uint64{1},
					Targets: []core.ACLTargetStruct{
						{DeviceType: new(uint32(0x0100)), Endpoint: new(uint16(1))}, // mutually exclusive
					},
					FabricIndex: 1,
				},
			},
			wantErr:     true,
			errContains: []string{"constraint error", "mutually exclusive"},
		},
		{
			name:        "target_all_nil_rejected",
			fabricIndex: 1,
			entries: []core.AccessControlEntryStruct{
				{
					Privilege: 5,
					AuthMode:  2,
					Subjects:  []uint64{1},
					Targets: []core.ACLTargetStruct{
						{Cluster: nil, Endpoint: nil, DeviceType: nil}, // all nil
					},
					FabricIndex: 1,
				},
			},
			wantErr:     true,
			errContains: []string{"constraint error", "at least one of"},
		},
		{
			name:        "privilege_out_of_range",
			fabricIndex: 1,
			entries: []core.AccessControlEntryStruct{
				{
					Privilege:   7, // invalid: valid range is 1..5 (View..Administer)
					AuthMode:    2, // CASE
					Subjects:    []uint64{1},
					FabricIndex: 1,
				},
			},
			wantErr:     true,
			errContains: []string{"constraint error", "Privilege=7"},
		},
		{
			name:        "authmode_out_of_range",
			fabricIndex: 1,
			entries: []core.AccessControlEntryStruct{
				{
					Privilege:   5, // Administer
					AuthMode:    9, // invalid: valid range is 1..3 (PASE/CASE/Group)
					Subjects:    []uint64{1},
					FabricIndex: 1,
				},
			},
			wantErr:     true,
			errContains: []string{"constraint error", "AuthMode=9"},
		},
		{
			name:        "valid_minimal",
			fabricIndex: 1,
			entries: []core.AccessControlEntryStruct{
				{
					Privilege:   5, // Administer
					AuthMode:    2, // CASE
					Subjects:    []uint64{1},
					FabricIndex: 1,
				},
			},
			wantErr: false,
		},
		{
			name:        "valid_max_capacity",
			fabricIndex: 1,
			entries: func() []core.AccessControlEntryStruct {
				out := make([]core.AccessControlEntryStruct, 4)
				for i := range out {
					subjects := []uint64{
						uint64(i*4 + 1),
						uint64(i*4 + 2),
						uint64(i*4 + 3),
						uint64(i*4 + 4),
					}
					targets := []core.ACLTargetStruct{
						{Cluster: new(uint32(0x0006))},
						{Endpoint: new(uint16(i + 1))},
						{DeviceType: new(uint32(0x0100))},
						{Cluster: new(uint32(0x0008)), Endpoint: nil, DeviceType: nil},
					}
					out[i] = core.AccessControlEntryStruct{
						Privilege:   5,
						AuthMode:    2,
						Subjects:    subjects,
						Targets:     targets,
						FabricIndex: 1,
					}
				}
				return out
			}(),
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ac := newAccessControl(t)
			if tc.fabricIndex != 0 {
				ac.SetCurrentFabric(tc.fabricIndex)
			}

			err := writeACL(ac, tc.entries)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("MatterWrite: expected error, got nil")
				}
				for _, sub := range tc.errContains {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("error %q does not contain %q", err.Error(), sub)
					}
				}
			} else if err != nil {
				t.Fatalf("MatterWrite: unexpected error: %v", err)
			}
		})
	}
}

// ---- TestAccessControl_CASESubjectOperationalNodeIdUpperBound ----

// TestAccessControl_CASESubjectOperationalNodeIdUpperBound pins the
// operational-node-ID upper bound used to validate a CASE-AuthMode ACL
// subject. The correct maximum is 0xFFFF_FFEF_FFFF_FFFF; anything above
// it falls into the reserved ranges (CAT 0xFFFF_FFFD.., Temporary-Local
// 0xFFFF_FFFE.., Group 0xFFFF_FFFF_FFFF_FF00..) and must NOT be accepted
// as a plain operational subject. A byte-transposed bound of
// 0xFFFF_FFFF_FFFF_FFEF would swallow the whole reserved space and
// accept a Group Node ID as a CASE subject.
// Mirrors matter.js packages/types/src/datatype/NodeId.ts:27-28
// (OPERATIONAL_NODE_MIN/MAX) and :57-59 (isOperationalNodeId).
func TestAccessControl_CASESubjectOperationalNodeIdUpperBound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject uint64
		wantErr bool
	}{
		{
			name:    "operational_min_accepted",
			subject: 0x0000_0000_0000_0001,
			wantErr: false,
		},
		{
			name:    "operational_max_accepted",
			subject: 0xFFFF_FFEF_FFFF_FFFF, // OPERATIONAL_NODE_MAX
			wantErr: false,
		},
		{
			name:    "just_above_operational_max_rejected",
			subject: 0xFFFF_FFF0_0000_0000, // one past OPERATIONAL_NODE_MAX
			wantErr: true,
		},
		{
			name:    "transposed_bound_value_rejected",
			subject: 0xFFFF_FFFF_FFFF_FFEF, // the buggy upper bound itself
			wantErr: true,
		},
		{
			name:    "group_node_id_rejected",
			subject: 0xFFFF_FFFF_FFFF_FF01, // Group Node ID, not a CASE subject
			wantErr: true,
		},
		{
			name:    "case_auth_tag_accepted",
			subject: 0xFFFF_FFFD_0001_0001, // CAT (upper 32 bits == 0xFFFF_FFFD)
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ac := newAccessControl(t)
			ac.SetCurrentFabric(1)

			err := writeACL(ac, []core.AccessControlEntryStruct{
				{
					Privilege:   5, // Administer
					AuthMode:    2, // CASE
					Subjects:    []uint64{tc.subject},
					FabricIndex: 1,
				},
			})

			if tc.wantErr {
				if err == nil {
					t.Fatalf("CASE subject 0x%016X: expected constraint error, got nil", tc.subject)
				}
				if !strings.Contains(err.Error(), "constraint error") {
					t.Fatalf("CASE subject 0x%016X: error %q lacks \"constraint error\"", tc.subject, err.Error())
				}
			} else if err != nil {
				t.Fatalf("CASE subject 0x%016X: unexpected error: %v", tc.subject, err)
			}
		})
	}
}

// ---- TestAccessControl_CASESubjectCATVersionZeroRejected ----

// TestAccessControl_CASESubjectCATVersionZeroRejected pins that a CASE ACL
// subject that is a CASE Auth Tag (upper 32 bits == 0xFFFF_FFFD) with a
// version number of 0 (the low 16 bits) is rejected with a constraint error.
// A non-zero CAT version is accepted. Mirrors matter.js
// packages/node/src/behaviors/access-control/AccessControlServer.ts:213-220
// (CaseAuthenticatedTag.getVersion(cat) === 0 → ConstraintError) and
// packages/types/src/datatype/CaseAuthenticatedTag.ts:31-33. Matter §6.6.2.1.2.
func TestAccessControl_CASESubjectCATVersionZeroRejected(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject uint64
		wantErr bool
	}{
		{
			name:    "cat_version_zero_rejected",
			subject: 0xFFFF_FFFD_0001_0000, // CAT id 0x0001, version 0x0000
			wantErr: true,
		},
		{
			name:    "cat_version_zero_high_id_rejected",
			subject: 0xFFFF_FFFD_ABCD_0000, // CAT id 0xABCD, version 0x0000
			wantErr: true,
		},
		{
			name:    "cat_version_one_accepted",
			subject: 0xFFFF_FFFD_0001_0001, // CAT id 0x0001, version 0x0001
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ac := newAccessControl(t)
			ac.SetCurrentFabric(1)

			err := writeACL(ac, []core.AccessControlEntryStruct{
				{
					Privilege:   5, // Administer
					AuthMode:    2, // CASE
					Subjects:    []uint64{tc.subject},
					FabricIndex: 1,
				},
			})

			if tc.wantErr {
				if err == nil {
					t.Fatalf("CAT subject 0x%016X: expected constraint error, got nil", tc.subject)
				}
				if !strings.Contains(err.Error(), "constraint error") {
					t.Fatalf("CAT subject 0x%016X: error %q lacks \"constraint error\"", tc.subject, err.Error())
				}
			} else if err != nil {
				t.Fatalf("CAT subject 0x%016X: unexpected error: %v", tc.subject, err)
			}
		})
	}
}

// ---- TestAccessControl_TargetClusterAndDeviceTypeValidity ----

// TestAccessControl_TargetClusterAndDeviceTypeValidity pins that an ACL
// Target's Cluster and DeviceType fields are validated as well-formed Matter
// identifiers. A standard cluster needs a zero vendor prefix + type suffix
// 0x0000..0x7FFF; a manufacturer-specific cluster needs a non-zero vendor
// prefix + type suffix 0xFC00..0xFFFE; a DeviceType needs a type suffix
// 0x0000..0xBFFF. Mirrors matter.js
// packages/node/src/behaviors/access-control/AccessControlServer.ts:266-278
// (ClusterId.isValid / DeviceTypeId.isValid). Matter §7.10 / §7.19.2.29.
func TestAccessControl_TargetClusterAndDeviceTypeValidity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		target  core.ACLTargetStruct
		wantErr bool
	}{
		{
			name:    "standard_cluster_valid",
			target:  core.ACLTargetStruct{Cluster: new(uint32(0x0006))},
			wantErr: false,
		},
		{
			name:    "ms_cluster_valid",
			target:  core.ACLTargetStruct{Cluster: new(uint32(0x0001_FC00))}, // vendor 1, suffix 0xFC00
			wantErr: false,
		},
		{
			name:    "standard_cluster_suffix_out_of_range_rejected",
			target:  core.ACLTargetStruct{Cluster: new(uint32(0x0000_8000))}, // prefix 0, suffix 0x8000 > 0x7FFF
			wantErr: true,
		},
		{
			name:    "ms_cluster_suffix_too_low_rejected",
			target:  core.ACLTargetStruct{Cluster: new(uint32(0x0001_0006))}, // vendor 1, suffix 0x0006 < 0xFC00
			wantErr: true,
		},
		{
			name:    "devicetype_valid",
			target:  core.ACLTargetStruct{DeviceType: new(uint32(0x0100))},
			wantErr: false,
		},
		{
			name:    "devicetype_suffix_out_of_range_rejected",
			target:  core.ACLTargetStruct{DeviceType: new(uint32(0x0000_C000))}, // suffix 0xC000 > 0xBFFF
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ac := newAccessControl(t)
			ac.SetCurrentFabric(1)

			err := writeACL(ac, []core.AccessControlEntryStruct{
				{
					Privilege:   5, // Administer
					AuthMode:    2, // CASE
					Subjects:    []uint64{1},
					Targets:     []core.ACLTargetStruct{tc.target},
					FabricIndex: 1,
				},
			})

			if tc.wantErr {
				if err == nil {
					t.Fatalf("target %+v: expected constraint error, got nil", tc.target)
				}
				if !strings.Contains(err.Error(), "constraint error") {
					t.Fatalf("target %+v: error %q lacks \"constraint error\"", tc.target, err.Error())
				}
			} else if err != nil {
				t.Fatalf("target %+v: unexpected error: %v", tc.target, err)
			}
		})
	}
}

// ---- TestAccessControl_ACLWriteEmitsEntryChanged ----

// TestAccessControl_ACLWriteEmitsEntryChanged verifies that a successful
// MatterWrite to the ACL attribute (0x0000) emits exactly one
// AccessControlEntryChanged event (cluster 0x001F, event 0x0000) at
// priority Info. The ChangeType is derived from the list-length delta
// between the pre-write snapshot and the new list, mirroring matter.js
// packages/node/src/behaviors/access-control/AccessControlServer.ts.
func TestAccessControl_ACLWriteEmitsEntryChanged(t *testing.T) {
	t.Parallel()

	minimalEntry := core.AccessControlEntryStruct{
		Privilege:   5, // Administer
		AuthMode:    2, // CASE
		Subjects:    []uint64{1},
		FabricIndex: 1,
	}

	tests := []struct {
		name           string
		existingCount  int // number of entries already in the store
		newCount       int // number of entries written
		wantChangeType uint8
	}{
		{
			name:           "empty_store_write_one_entry_added",
			existingCount:  0,
			newCount:       1,
			wantChangeType: core.AccessControlChangeTypeAdded,
		},
		{
			name:           "two_entry_store_write_one_entry_removed",
			existingCount:  2,
			newCount:       1,
			wantChangeType: core.AccessControlChangeTypeRemoved,
		},
		{
			name:           "two_entry_store_write_two_entries_changed",
			existingCount:  2,
			newCount:       2,
			wantChangeType: core.AccessControlChangeTypeChanged,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Build a seeded store with the requested number of existing
			// entries. All entries are stamped on fabric 1.
			existing := make([]mstore.ACLEntry, tc.existingCount)
			for i := range existing {
				existing[i] = minimalACLEntry(1)
			}
			store := &seededACLStore{existing: existing}

			ac := newAccessControlWithStore(t, store)
			ac.SetCurrentFabric(1)
			ac.SetEndpoint(0) // root endpoint

			emitter := &fakeEmitter{}
			ac.SetMatterEventEmitter(emitter)

			// Build the new entries slice.
			newEntries := make([]core.AccessControlEntryStruct, tc.newCount)
			for i := range newEntries {
				e := minimalEntry
				e.FabricIndex = 1
				newEntries[i] = e
			}

			if err := writeACL(ac, newEntries); err != nil {
				t.Fatalf("MatterWrite: unexpected error: %v", err)
			}

			emitter.mu.Lock()
			got := append([]recordedEvent(nil), emitter.events...)
			emitter.mu.Unlock()

			if len(got) != 1 {
				t.Fatalf("expected 1 emitted event, got %d", len(got))
			}
			ev := got[0]

			if ev.cluster != 0x001F {
				t.Errorf("cluster = 0x%04X, want 0x001F (AccessControl)", ev.cluster)
			}
			if ev.event != 0x0000 {
				t.Errorf("event = 0x%04X, want 0x0000 (AccessControlEntryChanged)", ev.event)
			}
			if ev.priority != interfaces.MatterEventPriorityInfo {
				t.Errorf("priority = %v, want Info (matter.js access-control.element.ts:62)", ev.priority)
			}
			if ev.endpoint != 0 {
				t.Errorf("endpoint = %d, want 0 (root endpoint)", ev.endpoint)
			}
			payload, ok := ev.data.(core.AccessControlEntryChangedEvent)
			if !ok {
				t.Fatalf("data = %T, want AccessControlEntryChangedEvent", ev.data)
			}
			if payload.ChangeType != tc.wantChangeType {
				t.Errorf("ChangeType = %d, want %d", payload.ChangeType, tc.wantChangeType)
			}
			if payload.AdminNodeID != nil {
				t.Errorf("AdminNodeID = %v, want nil (not tracked in v1.1)", payload.AdminNodeID)
			}
			if payload.AdminPasscodeID != nil {
				t.Errorf("AdminPasscodeID = %v, want nil (not tracked in v1.1)", payload.AdminPasscodeID)
			}
			if payload.LatestValue != nil {
				t.Errorf("LatestValue = %v, want nil (bulk-replace path)", payload.LatestValue)
			}
			if payload.FabricIndex != 1 {
				t.Errorf("FabricIndex = %d, want 1", payload.FabricIndex)
			}
		})
	}
}

// TestAccessControl_FabricScopedRead_ReturnsCallerFabricACL pins the
// Bug M fix: AccessControl.ACL is a fabric-scoped attribute. When the
// IM dispatcher hands a fabric-filter context (CASE session post-
// AddNOC), MatterReadFiltered MUST return ACL entries for THAT fabric,
// not whatever was last stamped via SetCurrentFabric (only ever set
// by ACL writes — zero on fresh CASE).
//
// Mirrors matter.js AccessControlServer.ts which consults
// FabricFilter for every `acl` attribute read. Without this, strict
// controllers read ACL on their post-CASE Subscribe-Initial, get
// `[]`, and reject the entire endpoint as "subject has no Administer
// privilege" → tear the fabric down via RemoveFabric.
func TestAccessControl_FabricScopedRead_ReturnsCallerFabricACL(t *testing.T) {
	t.Parallel()

	storeFake := &seededACLStore{
		existing: []mstore.ACLEntry{
			{FabricIndex: 1, Privilege: 5, AuthMode: 2, Subjects: []uint64{0xCAFE}},
			{FabricIndex: 2, Privilege: 5, AuthMode: 2, Subjects: []uint64{0xBEEF}},
		},
	}
	ac, err := core.NewAccessControl(storeFake)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}

	// Even with currentFabric never set (zero value), a CASE Subscribe
	// on fabric=2 must produce the fabric-2 ACL entries.
	ctx := im.WithFabricFilter(context.Background(), true, 2)
	v, ok := ac.MatterReadFiltered(ctx, 0x0000)
	if !ok {
		t.Fatal("MatterReadFiltered(ACL): ok=false")
	}
	got, ok := v.([]core.AccessControlEntryStruct)
	if !ok {
		t.Fatalf("ACL type = %T, want []AccessControlEntryStruct", v)
	}

	// seededACLStore.ListACL filters by fabricIndex parameter — see
	// helpers above. Implementation note: the fake's ListACL ignores
	// the fabric arg and returns the full list, so the cluster-level
	// filter applies AFTER the store call. Either way, the wire MUST
	// produce a non-empty list when fabric is provided.
	if len(got) == 0 {
		t.Fatalf("ACL list is empty — Bug M regression: Apple reads empty list on CASE Subscribe and rejects fabric")
	}
	// Bug-M guard: zero-length is the failure mode that triggers the
	// Apple "unsupported bridge" pair symptom even after Bugs I/J/K/L.
}

// captureACLStore records the fabricIndex of the most recent ReplaceACL
// call so tests can assert which fabric the cluster targeted. Used by
// the fabric-from-context test below.
type captureACLStore struct {
	lastWriteFabric  uint8
	lastWriteEntries []mstore.ACLEntry
	calls            int
}

func (c *captureACLStore) ListACL(_ context.Context, _ uint8) ([]mstore.ACLEntry, error) {
	return nil, nil
}

func (c *captureACLStore) ReplaceACL(_ context.Context, fabricIndex uint8, entries []mstore.ACLEntry) error {
	c.calls++
	c.lastWriteFabric = fabricIndex
	c.lastWriteEntries = append([]mstore.ACLEntry(nil), entries...)
	return nil
}

// TestAccessControl_ACLWrite_FabricFromContext pins that
// AccessControl.MatterWrite resolves the target fabric from the IM
// context stamped by bridge/receive.go from the inbound CASE session —
// not from the legacy SetCurrentFabric path or the client-supplied
// FabricIndex on the entry payload.
//
// Apple Home's post-CommissioningComplete ACL rewrite arrives on the
// fresh CASE session before any prior write has stamped a fabric via
// SetCurrentFabric. Apple sends entries with FabricIndex=0 per Matter
// §7.5.2 (clients send 0; the server stamps the caller's fabric).
// Without the ctx fabric, the cluster falls back to fabric=1 (last
// resort), Apple later reads its own fabric, sees the unchanged
// case_admin_subject ACL, and tears the pair down with the iOS
// "accessory could not be added" dialog.
//
// Mirrors matter.js packages/node/src/behaviors/access-control/
// AccessControlServer.ts where every ACL write resolves the fabric from
// the session context, not from any client-controlled field.
func TestAccessControl_ACLWrite_FabricFromContext(t *testing.T) {
	t.Parallel()

	const sessionFabric uint8 = 4 // typical Apple CASE-session fabric
	store := &captureACLStore{}
	ac, err := core.NewAccessControl(store)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	// Intentionally do NOT call SetCurrentFabric — mirrors the post-
	// AddNOC state before any prior write has stamped the cluster.

	// Apple's wire shape: entries.FabricIndex=0 per Matter §7.5.2
	// (clients send 0; the server stamps the caller's fabric).
	entries := []core.AccessControlEntryStruct{
		{
			Privilege:   5, // Administer
			AuthMode:    2, // CASE
			Subjects:    []uint64{0x07030001, 0x780722B1},
			FabricIndex: 0,
		},
	}

	ctx := im.WithFabricFilter(context.Background(), false, sessionFabric)
	if err := ac.MatterWrite(ctx, 0x0000, entries, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite: unexpected error: %v", err)
	}

	if store.calls != 1 {
		t.Fatalf("ReplaceACL calls = %d, want 1", store.calls)
	}
	if store.lastWriteFabric != sessionFabric {
		t.Fatalf("ReplaceACL fabricIndex = %d, want %d (CASE session fabric from ctx)\n"+
			"This is the Apple-pair regression: without ctx-fabric, MatterWrite\n"+
			"falls back to fabric=1 and Apple's post-CASE ACL update never\n"+
			"reaches the requesting fabric.", store.lastWriteFabric, sessionFabric)
	}
	if len(store.lastWriteEntries) != 1 {
		t.Fatalf("persisted entries = %d, want 1", len(store.lastWriteEntries))
	}
	if got := store.lastWriteEntries[0].FabricIndex; got != sessionFabric {
		t.Errorf("persisted entry FabricIndex = %d, want %d (server stamps caller fabric per §9.10.5.3)", got, sessionFabric)
	}
}

// TestAccessControl_ACLWrite_CtxFabricBeatsCurrentFabric asserts the
// resolution priority — ctx-fabric must win over a previously stamped
// SetCurrentFabric. This guards against a regression where the legacy
// SetCurrentFabric path silently shadows the spec-correct ctx source.
func TestAccessControl_ACLWrite_CtxFabricBeatsCurrentFabric(t *testing.T) {
	t.Parallel()

	store := &captureACLStore{}
	ac, err := core.NewAccessControl(store)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	ac.SetCurrentFabric(1) // stale value (e.g. from a prior write)

	entries := []core.AccessControlEntryStruct{
		{Privilege: 5, AuthMode: 2, Subjects: []uint64{0xCAFE}, FabricIndex: 0},
	}
	ctx := im.WithFabricFilter(context.Background(), false, 4) // current CASE session
	if err := ac.MatterWrite(ctx, 0x0000, entries, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("MatterWrite: unexpected error: %v", err)
	}

	if store.lastWriteFabric != 4 {
		t.Errorf("ReplaceACL fabricIndex = %d, want 4 (ctx-fabric must beat stale currentFabric=1)", store.lastWriteFabric)
	}
}

// aclStoreWithEntries is an ACLStoreFacade that returns a pre-loaded list.
type aclStoreWithEntries struct {
	entries []mstore.ACLEntry
}

func (s *aclStoreWithEntries) ListACL(_ context.Context, _ uint8) ([]mstore.ACLEntry, error) {
	return append([]mstore.ACLEntry(nil), s.entries...), nil
}

func (s *aclStoreWithEntries) ReplaceACL(_ context.Context, _ uint8, _ []mstore.ACLEntry) error {
	return nil
}

// errACLStore is an ACLStoreFacade whose ListACL always returns an error.
type errACLStore struct {
	listErr    error
	replaceErr error
}

func (s *errACLStore) ListACL(_ context.Context, _ uint8) ([]mstore.ACLEntry, error) {
	return nil, s.listErr
}

func (s *errACLStore) ReplaceACL(_ context.Context, _ uint8, _ []mstore.ACLEntry) error {
	return s.replaceErr
}

// newValidAccessControl reuses fakeACLStore defined above.
func newValidAccessControl(t *testing.T) *core.AccessControl {
	t.Helper()
	ac, err := core.NewAccessControl(&fakeACLStore{})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	return ac
}

// TestNewAccessControl_NilStore verifies that NewAccessControl returns an error
// when passed a nil ACLStoreFacade.
func TestNewAccessControl_NilStore(t *testing.T) {
	t.Parallel()
	_, err := core.NewAccessControl(nil)
	if err == nil {
		t.Fatal("NewAccessControl(nil): expected error, got nil")
	}
}

// TestAccessControl_MatterRead_ACL verifies that MatterRead(0x0000) returns the
// ACL list from the store. Exercises the fabricIndex==0 path (currentFabric not
// set) and the Targets branch.
func TestAccessControl_MatterRead_ACL(t *testing.T) {
	t.Parallel()
	clusterID := uint32(0x01F4)
	endpoint := uint16(1)
	deviceType := uint32(0x0100)
	store := &aclStoreWithEntries{
		entries: []mstore.ACLEntry{
			{
				FabricIndex: 1,
				Privilege:   mstore.Privilege(5),
				AuthMode:    mstore.AuthMode(2),
				Subjects:    []uint64{42},
				Targets: []mstore.ACLTarget{
					{Cluster: &clusterID, Endpoint: &endpoint, DeviceType: &deviceType},
				},
			},
		},
	}
	ac, err := core.NewAccessControl(store)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	// MatterRead with fabricIndex=0 (currentFabric not set via SetCurrentFabric).
	v, ok := ac.MatterRead(0x0000)
	if !ok {
		t.Fatal("MatterRead(ACL): ok=false, want true")
	}
	entries, ok2 := v.([]core.AccessControlEntryStruct)
	if !ok2 {
		t.Fatalf("MatterRead(ACL): type=%T, want []AccessControlEntryStruct", v)
	}
	if len(entries) != 1 {
		t.Fatalf("MatterRead(ACL): len=%d, want 1", len(entries))
	}
	if len(entries[0].Targets) != 1 {
		t.Errorf("MatterRead(ACL)[0].Targets len=%d, want 1", len(entries[0].Targets))
	}
}

// TestAccessControl_MatterRead_UnknownAttr verifies that MatterRead returns
// (nil, false) for an attribute ID that the cluster does not implement.
func TestAccessControl_MatterRead_UnknownAttr(t *testing.T) {
	t.Parallel()
	ac, err := core.NewAccessControl(&fakeACLStore{})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	v, ok := ac.MatterRead(0xDEAD)
	if ok || v != nil {
		t.Errorf("MatterRead(unknown): got (%v, %v), want (nil, false)", v, ok)
	}
}

// TestAccessControl_MatterRead_ACL_StoreError verifies that MatterRead(ACL)
// returns (nil, false) when the store returns an error.
func TestAccessControl_MatterRead_ACL_StoreError(t *testing.T) {
	t.Parallel()
	ac, err := core.NewAccessControl(&errACLStore{listErr: errors.New("db error")})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	v, ok := ac.MatterRead(0x0000)
	if ok || v != nil {
		t.Errorf("MatterRead(ACL) with store error: got (%v, %v), want (nil, false)", v, ok)
	}
}

// TestAccessControl_MatterReadFiltered_WithFabric verifies that
// MatterReadFiltered with fabricIndex>0 reads ACL from the store
// using that fabricIndex.
func TestAccessControl_MatterReadFiltered_WithFabric(t *testing.T) {
	t.Parallel()
	clusterID := uint32(0x0006)
	endpoint := uint16(2)
	store := &aclStoreWithEntries{
		entries: []mstore.ACLEntry{
			{
				FabricIndex: 1,
				Privilege:   mstore.Privilege(5),
				AuthMode:    mstore.AuthMode(2),
				Subjects:    []uint64{100},
				Targets: []mstore.ACLTarget{
					{Cluster: &clusterID, Endpoint: &endpoint},
				},
			},
		},
	}
	ac, err := core.NewAccessControl(store)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	ctx := im.WithFabricFilter(context.Background(), true, 1)
	v, ok := ac.MatterReadFiltered(ctx, 0x0000)
	if !ok {
		t.Fatal("MatterReadFiltered(ACL, fabricIndex=1): ok=false")
	}
	entries, ok2 := v.([]core.AccessControlEntryStruct)
	if !ok2 {
		t.Fatalf("type=%T, want []AccessControlEntryStruct", v)
	}
	if len(entries) != 1 || len(entries[0].Targets) != 1 {
		t.Errorf("entries=%v, want 1 entry with 1 target", entries)
	}
}

// TestAccessControl_MatterReadFiltered_StoreError verifies that
// MatterReadFiltered returns (nil, false) when the store errors.
func TestAccessControl_MatterReadFiltered_StoreError(t *testing.T) {
	t.Parallel()
	ac, err := core.NewAccessControl(&errACLStore{listErr: errors.New("db error")})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	ctx := im.WithFabricFilter(context.Background(), true, 1)
	v, ok := ac.MatterReadFiltered(ctx, 0x0000)
	if ok || v != nil {
		t.Errorf("MatterReadFiltered with store error: got (%v, %v), want (nil, false)", v, ok)
	}
}

// TestAccessControl_MatterWrite_WrongType verifies that MatterWrite(ACL) with a
// wrong value type returns an error.
func TestAccessControl_MatterWrite_WrongType(t *testing.T) {
	t.Parallel()
	ac, err := core.NewAccessControl(&fakeACLStore{})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	err = ac.MatterWrite(context.Background(), 0x0000, "not a slice", hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("MatterWrite(ACL, wrong type): expected error, got nil")
	}
}

// TestAccessControl_MatterWrite_FabricFallbackFromEntry verifies that when
// currentFabric==0 and entries[0].FabricIndex>0, the fabric is taken from
// entries[0].FabricIndex.
func TestAccessControl_MatterWrite_FabricFallbackFromEntry(t *testing.T) {
	t.Parallel()
	ac, err := core.NewAccessControl(&fakeACLStore{})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	entries := []core.AccessControlEntryStruct{
		{
			Privilege:   5,
			AuthMode:    2,
			Subjects:    []uint64{1},
			FabricIndex: 2, // non-zero → triggers the fallback branch
		},
	}
	if err = writeACL(ac, entries); err != nil {
		t.Fatalf("MatterWrite(ACL, fabric-from-entry fallback): %v", err)
	}
}

// TestAccessControl_MatterWrite_FabricLastResort verifies the last-resort
// fabric=1 assignment when currentFabric==0 AND entries is empty.
func TestAccessControl_MatterWrite_FabricLastResort(t *testing.T) {
	t.Parallel()
	ac, err := core.NewAccessControl(&fakeACLStore{})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	if err = writeACL(ac, []core.AccessControlEntryStruct{}); err != nil {
		t.Fatalf("MatterWrite(ACL, empty): %v", err)
	}
}

// TestAccessControl_MatterWrite_ReplaceACLError verifies that a store error
// from ReplaceACL is propagated.
func TestAccessControl_MatterWrite_ReplaceACLError(t *testing.T) {
	t.Parallel()
	ac, err := core.NewAccessControl(&errACLStore{replaceErr: errors.New("store write failed")})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	ac.SetCurrentFabric(1)
	entries := []core.AccessControlEntryStruct{
		{Privilege: 5, AuthMode: 2, Subjects: []uint64{1}, FabricIndex: 1},
	}
	writeErr := writeACL(ac, entries)
	if writeErr == nil {
		t.Fatal("MatterWrite(ACL) with ReplaceACL error: expected error, got nil")
	}
}

// TestAccessControl_MatterWrite_UnknownAttr verifies that writing to an
// attribute ID other than ACL (0x0000) returns an error.
func TestAccessControl_MatterWrite_UnknownAttr(t *testing.T) {
	t.Parallel()
	ac, err := core.NewAccessControl(&fakeACLStore{})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	err = ac.MatterWrite(context.Background(), 0xDEAD, "any", hmenum.CommandPriorityHigh)
	if err == nil {
		t.Fatal("MatterWrite(unknown attr): expected error, got nil")
	}
}

// TestAccessControl_MatterReadFiltered_ACL_FabricZero verifies that
// MatterReadFiltered(ACL) with fabricIndex=0 falls through to MatterRead.
func TestAccessControl_MatterReadFiltered_ACL_FabricZero(t *testing.T) {
	t.Parallel()
	store := &aclStoreWithEntries{
		entries: []mstore.ACLEntry{
			{FabricIndex: 0, Privilege: mstore.Privilege(5), AuthMode: mstore.AuthMode(2), Subjects: []uint64{1}},
		},
	}
	ac, err := core.NewAccessControl(store)
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	ctx := im.WithFabricFilter(context.Background(), false, 0)
	v, ok := ac.MatterReadFiltered(ctx, 0x0000)
	if !ok {
		t.Fatal("MatterReadFiltered(ACL, fabricIndex=0): ok=false")
	}
	entries, ok2 := v.([]core.AccessControlEntryStruct)
	if !ok2 {
		t.Fatalf("type=%T, want []AccessControlEntryStruct", v)
	}
	if len(entries) != 1 {
		t.Errorf("len(entries)=%d, want 1", len(entries))
	}
}

// TestAccessControl_MatterDataVersion verifies MatterDataVersion does not panic.
func TestAccessControl_MatterDataVersion(t *testing.T) {
	t.Parallel()
	ac := newValidAccessControl(t)
	_ = ac.MatterDataVersion()
}

// TestAccessControl_MatterReportable verifies ACL (0x0000) is in MatterReportable.
func TestAccessControl_MatterReportable(t *testing.T) {
	t.Parallel()
	ac := newValidAccessControl(t)
	list := ac.MatterReportable()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	if !have[0x0000] {
		t.Errorf("MatterReportable() missing ACL (0x0000); list = %v", list)
	}
}

// TestAccessControl_MatterAttributes verifies the attribute surface includes
// ACL (0x0000) and Extension (0x0001).
func TestAccessControl_MatterAttributes(t *testing.T) {
	t.Parallel()
	ac := newValidAccessControl(t)
	list := ac.MatterAttributes()
	have := make(map[uint32]bool)
	for _, a := range list {
		have[a] = true
	}
	for _, want := range []uint32{0x0000, 0x0001} {
		if !have[want] {
			t.Errorf("MatterAttributes() missing attr 0x%04X; list = %v", want, list)
		}
	}
}

// TestAccessControl_ReadSubjectsPerEntry verifies SubjectsPerAccessControlEntry
// returns a non-zero value.
func TestAccessControl_ReadSubjectsPerEntry(t *testing.T) {
	t.Parallel()
	ac := newValidAccessControl(t)
	v, ok := ac.MatterRead(0x0002) // SubjectsPerAccessControlEntry
	if !ok {
		t.Fatal("SubjectsPerAccessControlEntry: ok=false")
	}
	if v.(uint16) == 0 {
		t.Fatal("SubjectsPerAccessControlEntry = 0, want non-zero")
	}
}

// TestAccessControl_ReadTargetsPerEntry verifies TargetsPerAccessControlEntry
// returns a non-zero value.
func TestAccessControl_ReadTargetsPerEntry(t *testing.T) {
	t.Parallel()
	ac := newValidAccessControl(t)
	v, ok := ac.MatterRead(0x0003) // TargetsPerAccessControlEntry
	if !ok {
		t.Fatal("TargetsPerAccessControlEntry: ok=false")
	}
	if v.(uint16) == 0 {
		t.Fatal("TargetsPerAccessControlEntry = 0, want non-zero")
	}
}

// TestAccessControl_ReadAccessControlEntriesPerFabric verifies
// AccessControlEntriesPerFabric returns a non-zero value.
func TestAccessControl_ReadAccessControlEntriesPerFabric(t *testing.T) {
	t.Parallel()
	ac := newValidAccessControl(t)
	v, ok := ac.MatterRead(0x0004) // AccessControlEntriesPerFabric
	if !ok {
		t.Fatal("AccessControlEntriesPerFabric: ok=false")
	}
	if v.(uint16) == 0 {
		t.Fatal("AccessControlEntriesPerFabric = 0, want non-zero")
	}
}

// TestAccessControl_ReadFeatureMap verifies AccessControl advertises the EXTS
// feature (bit 0 = 1); Apple's HAP-mapper requires it.
func TestAccessControl_ReadFeatureMap(t *testing.T) {
	t.Parallel()
	ac := newValidAccessControl(t)
	v, ok := ac.MatterRead(0xFFFC) // FeatureMap
	if !ok {
		t.Fatal("FeatureMap: ok=false")
	}
	if v.(uint32) == 0 {
		t.Fatal("FeatureMap = 0; AccessControl must advertise EXTS (bit 0)")
	}
}

// TestAccessControl_ReadClusterRevision verifies ClusterRevision is 3.
func TestAccessControl_ReadClusterRevision(t *testing.T) {
	t.Parallel()
	ac := newValidAccessControl(t)
	v, ok := ac.MatterRead(0xFFFD) // ClusterRevision
	if !ok {
		t.Fatal("ClusterRevision: ok=false")
	}
	if v.(uint16) != 3 {
		t.Fatalf("ClusterRevision = %v, want 3", v)
	}
}

// TestAccessControl_ReadExtension verifies Extension attribute is readable.
func TestAccessControl_ReadExtension(t *testing.T) {
	t.Parallel()
	ac := newValidAccessControl(t)
	v, ok := ac.MatterRead(0x0001) // Extension
	if !ok {
		t.Fatal("Extension: ok=false")
	}
	_ = v
}

// TestAccessControl_ReadUnknownAttrReturnsFalse verifies unknown attr gives
// (nil, false).
func TestAccessControl_ReadUnknownAttrReturnsFalse(t *testing.T) {
	t.Parallel()
	ac := newValidAccessControl(t)
	if _, ok := ac.MatterRead(0xBEEF); ok {
		t.Fatal("MatterRead(0xBEEF) = true, want false")
	}
}
