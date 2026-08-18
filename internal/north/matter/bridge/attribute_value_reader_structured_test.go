// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// Parity tests for the structured write-decode path: GroupKeyManagement
// GroupKeyMap and AccessControl Extension. Both are mounted, writable,
// list-typed attributes; before this fix the generic primitive branch
// drained their TLV container to nil, so the cluster server's MatterWrite
// type assertion failed and every write answered FAILURE. The tests go
// through the wire-decode path (attributeValueReader → decode*List), not a
// hand-built Go slice, then hand the decoded value to the real cluster
// MatterWrite — the seam the defect lived on.

import (
	"bytes"
	"context"
	"testing"

	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	matterstore "github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// buildGroupKeyMapArrayTLV mirrors the read-direction encoder in
// reply.go's []GroupKeyMapStruct case and matter.js
// group-key-management.element.ts:106-111 (GroupId [1], GroupKeySetId [2],
// FabricIndex [254]).
func buildGroupKeyMapArrayTLV(entries []mattercore.GroupKeyMapStruct) []byte {
	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	for _, m := range entries {
		enc.StartStruct(tlv.AnonymousTag())
		enc.PutUint16(tlv.ContextTag(1), m.GroupID)
		enc.PutUint16(tlv.ContextTag(2), m.GroupKeySetID)
		enc.PutUint(tlv.ContextTag(254), uint64(m.FabricIndex))
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
	b, err := enc.Bytes()
	if err != nil {
		panic("buildGroupKeyMapArrayTLV: " + err.Error())
	}
	return b
}

// buildExtensionArrayTLV mirrors matter.js access-control.element.ts:204-208
// (Data [1] octstr, FabricIndex [254]).
func buildExtensionArrayTLV(entries []mattercore.AccessControlExtensionEntry) []byte {
	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	for _, e := range entries {
		enc.StartStruct(tlv.AnonymousTag())
		enc.PutOctets(tlv.ContextTag(1), e.Data)
		enc.PutUint(tlv.ContextTag(254), uint64(e.FabricIndex))
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
	b, err := enc.Bytes()
	if err != nil {
		panic("buildExtensionArrayTLV: " + err.Error())
	}
	return b
}

// TestAttributeValueReader_GroupKeyMap_DecodesAndClusterAccepts pins the
// write-decode path for GroupKeyManagement.GroupKeyMap (0x003F/0x0000).
//
// Bite check: deleting the GroupKeyMap case from attributeValueReader (so it
// falls through to primitiveAttributeValue) makes av.Value nil, the
// []GroupKeyMapStruct assertion fail, and — through MatterWrite — the write
// reject. Both halves of the test fail.
func TestAttributeValueReader_GroupKeyMap_DecodesAndClusterAccepts(t *testing.T) {
	t.Parallel()
	const fabric uint8 = 1
	path := im.ConcreteAttributePath{
		HasCluster: true, HasAttribute: true,
		Cluster: 0x003F, Attribute: 0x0000,
	}
	raw := buildGroupKeyMapArrayTLV([]mattercore.GroupKeyMapStruct{
		{GroupID: 0x0007, GroupKeySetID: 0x002A, FabricIndex: fabric},
	})
	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)

	av, err := attributeValueReader(path, el, dec)
	if err != nil {
		t.Fatalf("attributeValueReader: %v", err)
	}
	list, ok := av.Value.([]mattercore.GroupKeyMapStruct)
	if !ok {
		t.Fatalf("decoded to %T (want []GroupKeyMapStruct); a nil value fails the cluster type assertion → FAILURE", av.Value)
	}
	if len(list) != 1 || list[0].GroupID != 0x0007 || list[0].GroupKeySetID != 0x002A || list[0].FabricIndex != fabric {
		t.Fatalf("decoded entry mismatch: %+v", list)
	}

	g, err := mattercore.NewGroupKeyManagement(fakeGroupStore{}, mattercore.GroupKeyMgmtConfig{})
	if err != nil {
		t.Fatalf("NewGroupKeyManagement: %v", err)
	}
	ctx := im.WithFabricFilter(context.Background(), true, fabric)
	if err := g.MatterWrite(ctx, 0x0000, av.Value, 0); err != nil {
		t.Fatalf("GroupKeyManagement.MatterWrite rejected the wire-decoded value: %v", err)
	}
}

// TestAttributeValueReader_Extension_DecodesAndClusterAccepts pins the
// write-decode path for AccessControl.Extension (0x001F/0x0001).
//
// Bite check: deleting the Extension case from attributeValueReader makes
// av.Value nil, the []AccessControlExtensionEntry assertion fail, and the
// write reject.
func TestAttributeValueReader_Extension_DecodesAndClusterAccepts(t *testing.T) {
	t.Parallel()
	const fabric uint8 = 2
	path := im.ConcreteAttributePath{
		HasCluster: true, HasAttribute: true,
		Cluster: 0x001F, Attribute: 0x0001,
	}
	// Vendor-opaque octet string. Must itself decode as a well-formed TLV
	// List (matter.js AccessControlServer.ts:424-441
	// extensionEntryValidator) — a minimal empty List (0x17 open, 0x18
	// EndOfContainer) satisfies that while still exercising the same
	// decode-then-write seam this test targets.
	dataEnc := tlv.NewEncoder()
	dataEnc.StartList(tlv.AnonymousTag())
	if err := dataEnc.EndContainer(); err != nil {
		t.Fatalf("EndContainer Data list: %v", err)
	}
	data, err := dataEnc.Bytes()
	if err != nil {
		t.Fatalf("dataEnc.Bytes: %v", err)
	}
	raw := buildExtensionArrayTLV([]mattercore.AccessControlExtensionEntry{
		{Data: data, FabricIndex: fabric},
	})
	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)

	av, err := attributeValueReader(path, el, dec)
	if err != nil {
		t.Fatalf("attributeValueReader: %v", err)
	}
	list, ok := av.Value.([]mattercore.AccessControlExtensionEntry)
	if !ok {
		t.Fatalf("decoded to %T (want []AccessControlExtensionEntry); a nil value fails the cluster type assertion → FAILURE", av.Value)
	}
	if len(list) != 1 || !bytes.Equal(list[0].Data, data) {
		t.Fatalf("decoded entry mismatch: %+v", list)
	}

	a, err := mattercore.NewAccessControl(fakeACLStore{})
	if err != nil {
		t.Fatalf("NewAccessControl: %v", err)
	}
	ctx := im.WithFabricFilter(context.Background(), true, fabric)
	if err := a.MatterWrite(ctx, 0x0001, av.Value, 0); err != nil {
		t.Fatalf("AccessControl.MatterWrite(Extension) rejected the wire-decoded value: %v", err)
	}
}

// fakeGroupStore is a no-op GroupStoreFacade: the parity test only needs
// MatterWrite to accept the decoded value, not to persist it.
type fakeGroupStore struct{}

func (fakeGroupStore) UpsertGroupKeySet(context.Context, matterstore.GroupKeySet) error { return nil }

func (fakeGroupStore) GetGroupKeySet(context.Context, uint8, uint16) (matterstore.GroupKeySet, error) {
	return matterstore.GroupKeySet{}, nil
}

func (fakeGroupStore) ListGroupKeySets(context.Context, uint8) ([]matterstore.GroupKeySet, error) {
	return nil, nil
}

func (fakeGroupStore) RemoveGroupKeySet(context.Context, uint8, uint16) error { return nil }

func (fakeGroupStore) SetGroupKeyMapping(context.Context, matterstore.GroupKeyMapping) error {
	return nil
}

func (fakeGroupStore) RemoveGroupKeyMapping(context.Context, uint8, uint16) error { return nil }

func (fakeGroupStore) ListGroupKeyMappings(context.Context, uint8) ([]matterstore.GroupKeyMapping, error) {
	return nil, nil
}

// fakeACLStore is a no-op ACLStoreFacade.
type fakeACLStore struct{}

func (fakeACLStore) ListACL(context.Context, uint8) ([]matterstore.ACLEntry, error) { return nil, nil }

func (fakeACLStore) ReplaceACL(context.Context, uint8, []matterstore.ACLEntry) error { return nil }
