// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box wire-shape test for the DoorLock LockOperation event TLV
// encoding added to defaultAttributeValueWriter. Lives in package
// bridge (not bridge_test) to access the unexported writer, matching
// the pattern in reply_test.go / reply_value_writer_test.go.

import (
	"testing"

	matterlock "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/lock"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// decodeStructFields decodes b as a single TLV Structure and returns
// its immediate fields keyed by context-tag number. Fails the test on
// any decode error or if the outer element is not a Structure.
func decodeStructFields(t *testing.T, b []byte) map[uint32]tlv.Element {
	t.Helper()
	dec := tlv.NewDecoder(b)

	header, err := dec.Next()
	if err != nil {
		t.Fatalf("decoder.Next (struct header): %v", err)
	}
	if header.Type != tlv.TypeStructure || !header.IsContainer {
		t.Fatalf("struct header: want TypeStructure/IsContainer, got type=0x%02X isContainer=%v", header.Type, header.IsContainer)
	}

	fields := make(map[uint32]tlv.Element)
	for {
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("decoder.Next (field): %v", err)
		}
		if el.IsEndContainer {
			break
		}
		fields[el.Tag.Number] = el
	}
	return fields
}

// TestDefaultAttributeValueWriter_LockOperationEvent_AllFieldsSet
// verifies the DoorLock §5.2.10.3 LockOperation TLV shape when
// FabricIndex and SourceNode are populated (UserIndex stays nil — no
// USR/PIN-credential support in this projection). Field tags mirror
// matter.js door-lock-cluster.element.ts:181-195:
//
//	[0] LockOperationType enum8, [1] OperationSource enum8,
//	[2] UserIndex uint16 nullable, [3] FabricIndex fabric-idx nullable,
//	[4] SourceNode node-id nullable.
func TestDefaultAttributeValueWriter_LockOperationEvent_AllFieldsSet(t *testing.T) {
	t.Parallel()

	fabricIndex := uint8(3)
	sourceNode := uint64(0x1122334455667788)
	v := matterlock.LockOperationEvent{
		LockOperationType: 1, // Unlock
		OperationSource:   7, // Remote
		UserIndex:         nil,
		FabricIndex:       &fabricIndex,
		SourceNode:        &sourceNode,
	}

	b := verifyNonEmpty(t, "LockOperationEvent (all fields set)", callAttributeWriter(im.AttributeValue{Value: v}))
	fields := decodeStructFields(t, b)

	if el, ok := fields[0]; !ok || el.Uint != 1 {
		t.Errorf("tag 0 (LockOperationType) = %+v, want Uint=1", el)
	}
	if el, ok := fields[1]; !ok || el.Uint != 7 {
		t.Errorf("tag 1 (OperationSource) = %+v, want Uint=7", el)
	}
	if el, ok := fields[2]; !ok || !el.IsNull {
		t.Errorf("tag 2 (UserIndex) = %+v, want IsNull=true", el)
	}
	if el, ok := fields[3]; !ok || el.IsNull || el.Uint != uint64(fabricIndex) {
		t.Errorf("tag 3 (FabricIndex) = %+v, want Uint=%d IsNull=false", el, fabricIndex)
	}
	if el, ok := fields[4]; !ok || el.IsNull || el.Uint != sourceNode {
		t.Errorf("tag 4 (SourceNode) = %+v, want Uint=%d IsNull=false", el, sourceNode)
	}
}

// TestDefaultAttributeValueWriter_LockOperationEvent_NullableFieldsNil
// verifies that UserIndex, FabricIndex and SourceNode all encode as
// TLV null when unset — the PASE / no-PIN-credential case matter.js
// DoorLockServer.ts:911-939 falls back to.
func TestDefaultAttributeValueWriter_LockOperationEvent_NullableFieldsNil(t *testing.T) {
	t.Parallel()

	v := matterlock.LockOperationEvent{
		LockOperationType: 0, // Lock
		OperationSource:   7, // Remote
		UserIndex:         nil,
		FabricIndex:       nil,
		SourceNode:        nil,
	}

	b := verifyNonEmpty(t, "LockOperationEvent (nullable fields nil)", callAttributeWriter(im.AttributeValue{Value: v}))
	fields := decodeStructFields(t, b)

	if el, ok := fields[0]; !ok || el.Uint != 0 {
		t.Errorf("tag 0 (LockOperationType) = %+v, want Uint=0", el)
	}
	if el, ok := fields[1]; !ok || el.Uint != 7 {
		t.Errorf("tag 1 (OperationSource) = %+v, want Uint=7", el)
	}
	for tag := uint32(2); tag <= 4; tag++ {
		el, ok := fields[tag]
		if !ok || !el.IsNull {
			t.Errorf("tag %d = %+v, want IsNull=true", tag, el)
		}
	}
}
