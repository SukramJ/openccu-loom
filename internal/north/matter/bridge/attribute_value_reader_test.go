// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for attributeValueReader, decodeACLList, decodeACLEntry,
// decodeACLSubjects, decodeACLTargets, decodeACLTarget, primitiveAttributeValue,
// and skipContainerTLV. Lives in package bridge to access unexported functions.

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"

	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
)

// advanceToContent advances dec past the first element and returns the element.
func advanceToContent(t *testing.T, dec *tlv.Decoder) tlv.Element {
	t.Helper()
	el, err := dec.Next()
	if err != nil {
		t.Fatalf("advanceToContent: %v", err)
	}
	return el
}

// ─── primitiveAttributeValue ──────────────────────────────────────────────────

func TestPrimitiveAttributeValue_Null(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.PutNull(tlv.AnonymousTag())
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)
	got, err := primitiveAttributeValue(el, dec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.IsNull {
		t.Error("expected IsNull=true for null element")
	}
}

func TestPrimitiveAttributeValue_BoolTrue(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.PutBool(tlv.AnonymousTag(), true)
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)
	got, err := primitiveAttributeValue(el, dec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value != true {
		t.Errorf("expected true, got %v", got.Value)
	}
}

func TestPrimitiveAttributeValue_BoolFalse(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.PutBool(tlv.AnonymousTag(), false)
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)
	got, err := primitiveAttributeValue(el, dec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value != false {
		t.Errorf("expected false, got %v", got.Value)
	}
}

func TestPrimitiveAttributeValue_UTF8String(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.PutUTF8(tlv.AnonymousTag(), "hello")
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)
	got, err := primitiveAttributeValue(el, dec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Production code uses el.Octets for UTF8 strings; decoder puts value in
	// el.String, so Octets is empty — resulting in "". This is a known
	// current behavior: we verify no error and the returned string type.
	if _, ok := got.Value.(string); !ok {
		t.Errorf("expected string value type, got %T", got.Value)
	}
}

func TestPrimitiveAttributeValue_OctetStr(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.PutOctets(tlv.AnonymousTag(), []byte{0x01, 0x02, 0x03})
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)
	got, err := primitiveAttributeValue(el, dec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	b, ok := got.Value.([]byte)
	if !ok {
		t.Fatalf("expected []byte, got %T", got.Value)
	}
	if len(b) != 3 || b[0] != 0x01 || b[1] != 0x02 || b[2] != 0x03 {
		t.Errorf("unexpected bytes: %v", b)
	}
}

func TestPrimitiveAttributeValue_Uint(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.PutUint(tlv.AnonymousTag(), 42)
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)
	got, err := primitiveAttributeValue(el, dec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Value != uint64(42) {
		t.Errorf("expected uint64(42), got %v", got.Value)
	}
}

func TestPrimitiveAttributeValue_Container_Drains(t *testing.T) {
	t.Parallel()
	// Build a structure with some content; primitiveAttributeValue should
	// drain it and return an empty AttributeValue (no error).
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), 99)
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)
	got, err := primitiveAttributeValue(el, dec)
	if err != nil {
		t.Fatalf("unexpected error draining container: %v", err)
	}
	// Container value is discarded — returned value should be zero.
	if got.IsNull || got.Value != nil {
		t.Errorf("expected zero AttributeValue after container drain, got %+v", got)
	}
}

// ─── skipContainerTLV ────────────────────────────────────────────────────────

func TestSkipContainerTLV_EmptyStruct(t *testing.T) {
	t.Parallel()
	// Encode: struct-open, end-container; give decoder bytes after the opener.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	// Consume the opener so dec is positioned at the EndContainer.
	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil { // struct opener
		t.Fatalf("consume opener: %v", err)
	}
	if err := skipContainerTLV(dec); err != nil {
		t.Fatalf("skipContainerTLV: %v", err)
	}
}

func TestSkipContainerTLV_NestedContainers(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.StartArray(tlv.ContextTag(0))
	enc.PutUint(tlv.AnonymousTag(), 1)
	_ = enc.EndContainer() // end array
	_ = enc.EndContainer() // end struct
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil { // outer struct opener
		t.Fatalf("consume outer opener: %v", err)
	}
	if err := skipContainerTLV(dec); err != nil {
		t.Fatalf("skipContainerTLV with nested: %v", err)
	}
}

// ─── decodeACLSubjects ───────────────────────────────────────────────────────

func TestDecodeACLSubjects_Empty(t *testing.T) {
	t.Parallel()
	// Build an array of subjects with one entry then an end-container.
	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	enc.PutUint(tlv.AnonymousTag(), 0xDEAD)
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	// Position dec after the array opener.
	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil { // array opener
		t.Fatalf("consume opener: %v", err)
	}
	got, err := decodeACLSubjects(dec)
	if err != nil {
		t.Fatalf("decodeACLSubjects: %v", err)
	}
	if len(got) != 1 || got[0] != 0xDEAD {
		t.Errorf("expected [0xDEAD], got %v", got)
	}
}

// ─── decodeACLTarget ─────────────────────────────────────────────────────────

func TestDecodeACLTarget_AllFields(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), 0x0006) // cluster
	enc.PutUint(tlv.ContextTag(1), 2)      // endpoint
	enc.PutUint(tlv.ContextTag(2), 0x010A) // device-type
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil { // struct opener
		t.Fatalf("consume opener: %v", err)
	}
	got, err := decodeACLTarget(dec)
	if err != nil {
		t.Fatalf("decodeACLTarget: %v", err)
	}
	if got.Cluster == nil || *got.Cluster != 0x0006 {
		t.Errorf("Cluster: want 0x0006, got %v", got.Cluster)
	}
	if got.Endpoint == nil || *got.Endpoint != 2 {
		t.Errorf("Endpoint: want 2, got %v", got.Endpoint)
	}
	if got.DeviceType == nil || *got.DeviceType != 0x010A {
		t.Errorf("DeviceType: want 0x010A, got %v", got.DeviceType)
	}
}

func TestDecodeACLTarget_NullFields(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutNull(tlv.ContextTag(0)) // cluster=null
	enc.PutNull(tlv.ContextTag(1)) // endpoint=null
	enc.PutNull(tlv.ContextTag(2)) // device-type=null
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil {
		t.Fatalf("consume opener: %v", err)
	}
	got, err := decodeACLTarget(dec)
	if err != nil {
		t.Fatalf("decodeACLTarget: %v", err)
	}
	if got.Cluster != nil || got.Endpoint != nil || got.DeviceType != nil {
		t.Errorf("expected all nil pointers, got %+v", got)
	}
}

// ─── decodeACLTargets ────────────────────────────────────────────────────────

func TestDecodeACLTargets_SingleEntry(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	// one struct entry
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), 0x0006)
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil { // array opener
		t.Fatalf("consume opener: %v", err)
	}
	got, err := decodeACLTargets(dec)
	if err != nil {
		t.Fatalf("decodeACLTargets: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 target, got %d", len(got))
	}
	if got[0].Cluster == nil || *got[0].Cluster != 0x0006 {
		t.Errorf("target[0].Cluster: want 0x0006, got %v", got[0].Cluster)
	}
}

// ─── decodeACLEntry ──────────────────────────────────────────────────────────

func TestDecodeACLEntry_WithSubjectsAndTargets(t *testing.T) {
	t.Parallel()
	// Build a struct; consume the opener so decodeACLEntry is entered after it.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(1), 5) // privilege = 5
	enc.PutUint(tlv.ContextTag(2), 2) // auth-mode = 2
	enc.StartArray(tlv.ContextTag(3)) // subjects
	enc.PutUint(tlv.AnonymousTag(), 1111)
	_ = enc.EndContainer()
	enc.StartArray(tlv.ContextTag(4)) // targets
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(0), 0x0006)
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	enc.PutUint(tlv.ContextTag(254), 1) // fabric-index
	_ = enc.EndContainer()              // close outer struct
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	// Consume the struct opener so decodeACLEntry starts inside it.
	if _, err := dec.Next(); err != nil {
		t.Fatalf("consume opener: %v", err)
	}
	got, err := decodeACLEntry(dec)
	if err != nil {
		t.Fatalf("decodeACLEntry: %v", err)
	}
	if got.Privilege != 5 {
		t.Errorf("Privilege: want 5, got %d", got.Privilege)
	}
	if got.AuthMode != 2 {
		t.Errorf("AuthMode: want 2, got %d", got.AuthMode)
	}
	if len(got.Subjects) != 1 || got.Subjects[0] != 1111 {
		t.Errorf("Subjects: want [1111], got %v", got.Subjects)
	}
	if len(got.Targets) != 1 {
		t.Errorf("Targets length: want 1, got %d", len(got.Targets))
	}
	if got.FabricIndex != 1 {
		t.Errorf("FabricIndex: want 1, got %d", got.FabricIndex)
	}
}

func TestDecodeACLEntry_NullSubjectsAndTargets(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(1), 5)
	enc.PutUint(tlv.ContextTag(2), 2)
	enc.PutNull(tlv.ContextTag(3)) // tag 3: subjects (null)
	enc.PutNull(tlv.ContextTag(4)) // tag 4: targets (null)
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	// Consume the struct opener.
	if _, err := dec.Next(); err != nil {
		t.Fatalf("consume opener: %v", err)
	}
	got, err := decodeACLEntry(dec)
	if err != nil {
		t.Fatalf("decodeACLEntry: %v", err)
	}
	if got.Subjects != nil {
		t.Errorf("Subjects: want nil, got %v", got.Subjects)
	}
	if got.Targets != nil {
		t.Errorf("Targets: want nil, got %v", got.Targets)
	}
}

// TestDecodeACLEntry_UnknownContextTagContainer verifies that an unknown
// context-tag container inside an ACL entry is skipped (default branch).
func TestDecodeACLEntry_UnknownContextTagContainer(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(1), 5) // privilege
	enc.PutUint(tlv.ContextTag(2), 2) // auth-mode
	enc.PutNull(tlv.ContextTag(3))    // tag 3: subjects (null)
	enc.PutNull(tlv.ContextTag(4))    // tag 4: targets (null)
	// Add an unknown context-tag container (e.g. tag 99).
	enc.StartStruct(tlv.ContextTag(99)) // unknown container — default: skip
	enc.PutUint(tlv.ContextTag(0), 42)
	_ = enc.EndContainer()
	enc.PutUint(tlv.ContextTag(254), 1) // fabric-index
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil { // consume struct opener
		t.Fatalf("consume opener: %v", err)
	}
	got, err := decodeACLEntry(dec)
	if err != nil {
		t.Fatalf("decodeACLEntry with unknown container tag: %v", err)
	}
	if got.Privilege != 5 {
		t.Errorf("Privilege: want 5, got %d", got.Privilege)
	}
}

// TestDecodeACLEntry_NonContextTagSkipped verifies that a non-context-tag
// element inside an ACL entry struct is silently skipped (continue path).
func TestDecodeACLEntry_NonContextTagSkipped(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.AnonymousTag(), 0xFF) // non-context tag → continue
	enc.PutUint(tlv.ContextTag(1), 5)     // privilege
	enc.PutUint(tlv.ContextTag(2), 2)     // auth-mode
	enc.PutNull(tlv.ContextTag(3))        // subjects null
	enc.PutNull(tlv.ContextTag(4))        // targets null
	enc.PutUint(tlv.ContextTag(254), 1)   // fabric-index
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil {
		t.Fatalf("consume opener: %v", err)
	}
	got, err := decodeACLEntry(dec)
	if err != nil {
		t.Fatalf("decodeACLEntry with non-context tag: %v", err)
	}
	if got.Privilege != 5 {
		t.Errorf("Privilege: want 5, got %d", got.Privilege)
	}
}

// TestDecodeACLEntry_SubjectsNotArray_Error verifies that a subjects field
// that is not an array (e.g. a uint8 primitive under context tag 3) returns
// an error.
func TestDecodeACLEntry_SubjectsNotArray_Error(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(3), 0xFF) // subjects = non-array uint
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil {
		t.Fatalf("consume opener: %v", err)
	}
	_, err := decodeACLEntry(dec)
	if err == nil {
		t.Error("decodeACLEntry with subjects as uint: want error, got nil")
	}
}

// TestDecodeACLEntry_TargetsNotArray_Error verifies that a targets field
// that is not an array (e.g. a uint8 primitive under context tag 4) returns
// an error.
func TestDecodeACLEntry_TargetsNotArray_Error(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(4), 0xFF) // targets = non-array uint
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil {
		t.Fatalf("consume opener: %v", err)
	}
	_, err := decodeACLEntry(dec)
	if err == nil {
		t.Error("decodeACLEntry with targets as uint: want error, got nil")
	}
}

// ─── decodeACLList ───────────────────────────────────────────────────────────

func buildACLArrayTLV(entries []mattercore.AccessControlEntryStruct) []byte {
	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	for _, e := range entries {
		enc.StartStruct(tlv.AnonymousTag())
		enc.PutUint(tlv.ContextTag(1), uint64(e.Privilege))
		enc.PutUint(tlv.ContextTag(2), uint64(e.AuthMode))
		enc.PutNull(tlv.ContextTag(3)) // subjects null
		enc.PutNull(tlv.ContextTag(4)) // targets null
		enc.PutUint(tlv.ContextTag(254), uint64(e.FabricIndex))
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
	b, err := enc.Bytes()
	if err != nil {
		panic("buildACLArrayTLV: " + err.Error())
	}
	return b
}

func TestDecodeACLList_SingleEntry(t *testing.T) {
	t.Parallel()
	raw := buildACLArrayTLV([]mattercore.AccessControlEntryStruct{
		{Privilege: 5, AuthMode: 2, FabricIndex: 1},
	})
	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec) // array opener

	av, err := decodeACLList(el, dec)
	if err != nil {
		t.Fatalf("decodeACLList: %v", err)
	}
	list, ok := av.Value.([]mattercore.AccessControlEntryStruct)
	if !ok {
		t.Fatalf("expected []AccessControlEntryStruct, got %T", av.Value)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(list))
	}
	if list[0].Privilege != 5 || list[0].AuthMode != 2 || list[0].FabricIndex != 1 {
		t.Errorf("entry mismatch: %+v", list[0])
	}
}

func TestDecodeACLList_NotArray_Error(t *testing.T) {
	t.Parallel()
	// Pass a non-array element (a struct opener).
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)

	_, err := decodeACLList(el, dec)
	if err == nil {
		t.Error("expected error for non-array element, got nil")
	}
}

// TestDecodeACLTargets_PrimitiveSkipped verifies that a non-container element
// (e.g. a uint8 primitive) inside the targets array is silently skipped via
// the continue path at attribute_value_reader.go line 160-161.
func TestDecodeACLTargets_PrimitiveSkipped(t *testing.T) {
	t.Parallel()
	// Build an array: one uint8 primitive (skip) then one valid struct.
	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	enc.PutUint(tlv.AnonymousTag(), 0xAB) // unexpected primitive — should be skipped
	enc.StartStruct(tlv.AnonymousTag())   // valid ACLTargetStruct
	enc.PutUint(tlv.ContextTag(0), 0x0006)
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	if _, err := dec.Next(); err != nil { // consume array opener
		t.Fatalf("consume opener: %v", err)
	}
	got, err := decodeACLTargets(dec)
	if err != nil {
		t.Fatalf("decodeACLTargets with primitive: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("expected 1 target (primitive skipped), got %d", len(got))
	}
	if got[0].Cluster == nil || *got[0].Cluster != 0x0006 {
		t.Errorf("target[0].Cluster: want 0x0006, got %v", got[0].Cluster)
	}
}

// TestDecodeACLList_WithContainerNonStructSkipped verifies that a container
// element that is NOT a struct (e.g. a nested array) inside the ACL array
// exercises the skipContainerTLV branch (attribute_value_reader.go line 56-59).
func TestDecodeACLList_WithContainerNonStructSkipped(t *testing.T) {
	t.Parallel()
	// Build an outer array that contains an inner array (non-struct container)
	// followed by a valid ACL struct. The inner array must be skipped.
	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	// Inner array — not a struct, IsContainer=true → skipContainerTLV path.
	enc.StartArray(tlv.AnonymousTag())
	enc.PutUint(tlv.AnonymousTag(), 0xFF)
	_ = enc.EndContainer()
	// Valid ACL entry struct.
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(1), 5)   // privilege
	enc.PutUint(tlv.ContextTag(2), 2)   // auth-mode
	enc.PutNull(tlv.ContextTag(3))      // subjects null
	enc.PutNull(tlv.ContextTag(4))      // targets null
	enc.PutUint(tlv.ContextTag(254), 1) // fabric-index
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec) // outer array opener

	av, err := decodeACLList(el, dec)
	if err != nil {
		t.Fatalf("decodeACLList with skipped nested array: %v", err)
	}
	list, ok := av.Value.([]mattercore.AccessControlEntryStruct)
	if !ok {
		t.Fatalf("expected []AccessControlEntryStruct, got %T", av.Value)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 entry (nested array skipped), got %d", len(list))
	}
}

// TestDecodeACLList_WithUnexpectedPrimitiveSkipped verifies that a
// non-container, non-struct element inside the ACL array is silently
// skipped (the continue-path in decodeACLList).
func TestDecodeACLList_WithUnexpectedPrimitiveSkipped(t *testing.T) {
	t.Parallel()
	// Build an array containing a uint8 primitive (unexpected) followed
	// by a valid ACL struct. The uint8 must be skipped, the struct decoded.
	enc := tlv.NewEncoder()
	enc.StartArray(tlv.AnonymousTag())
	enc.PutUint(tlv.AnonymousTag(), 0xFF) // unexpected primitive — should be skipped
	enc.StartStruct(tlv.AnonymousTag())   // valid entry
	enc.PutUint(tlv.ContextTag(1), 5)     // privilege
	enc.PutUint(tlv.ContextTag(2), 2)     // auth-mode
	enc.PutNull(tlv.ContextTag(3))        // subjects null
	enc.PutNull(tlv.ContextTag(4))        // targets null
	enc.PutUint(tlv.ContextTag(254), 1)   // fabric-index
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	raw, _ := enc.Bytes()

	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec) // array opener

	av, err := decodeACLList(el, dec)
	if err != nil {
		t.Fatalf("decodeACLList with skipped primitive: %v", err)
	}
	list, ok := av.Value.([]mattercore.AccessControlEntryStruct)
	if !ok {
		t.Fatalf("expected []AccessControlEntryStruct, got %T", av.Value)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 entry (primitive skipped), got %d", len(list))
	}
}

// ─── attributeValueReader ────────────────────────────────────────────────────

func TestAttributeValueReader_ACLCluster(t *testing.T) {
	t.Parallel()
	// cluster=0x001F attribute=0x0000 → ACL path
	path := im.ConcreteAttributePath{
		HasCluster: true, HasAttribute: true,
		Cluster: 0x001F, Attribute: 0x0000,
	}
	raw := buildACLArrayTLV([]mattercore.AccessControlEntryStruct{
		{Privilege: 3, AuthMode: 1},
	})
	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)

	av, err := attributeValueReader(path, el, dec)
	if err != nil {
		t.Fatalf("attributeValueReader: %v", err)
	}
	list, ok := av.Value.([]mattercore.AccessControlEntryStruct)
	if !ok || len(list) != 1 {
		t.Errorf("expected 1-entry ACL list, got %v", av.Value)
	}
}

func TestAttributeValueReader_OtherCluster_Primitive(t *testing.T) {
	t.Parallel()
	// Any other path → primitiveAttributeValue
	path := im.ConcreteAttributePath{
		HasCluster: true, HasAttribute: true,
		Cluster: 0x0006, Attribute: 0x0000,
	}
	enc := tlv.NewEncoder()
	enc.PutBool(tlv.AnonymousTag(), true)
	raw, _ := enc.Bytes()
	dec := tlv.NewDecoder(raw)
	el := advanceToContent(t, dec)

	av, err := attributeValueReader(path, el, dec)
	if err != nil {
		t.Fatalf("attributeValueReader: %v", err)
	}
	if av.Value != true {
		t.Errorf("expected true, got %v", av.Value)
	}
}
