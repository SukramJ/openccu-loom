// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// White-box tests for the ClosureControl attribute encodings added to
// defaultAttributeValueWriter. Lives in package bridge because the writer
// is unexported.
//
// These matter more than the shape of the values suggests: the writer's
// default branch encodes anything it does not recognise as TLV null. A
// struct attribute with no case of its own therefore reads as "exists,
// no value" on every controller — a well-formed reply that carries
// nothing, and no error anywhere to say so.
package bridge

import (
	"testing"

	clusterwire "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// decodeClosureStructFields encodes v and returns its context-tagged child
// elements, failing when the value did not encode as a structure.
func decodeClosureStructFields(t *testing.T, v im.AttributeValue) map[uint32]tlv.Element {
	t.Helper()
	enc := tlv.NewEncoder()
	defaultAttributeValueWriter(enc, tlv.AnonymousTag(), v)
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encoder.Bytes: %v", err)
	}
	d := tlv.NewDecoder(b)
	open, err := d.Next()
	if err != nil {
		t.Fatalf("decoder.Next: %v", err)
	}
	if open.Type != tlv.TypeStructure {
		t.Fatalf("encoded as type 0x%02X (isNull=%v), want a structure — an attribute the "+
			"writer does not recognise falls through to the null default", open.Type, open.IsNull)
	}
	out := map[uint32]tlv.Element{}
	for {
		el, nerr := d.Next()
		if nerr != nil {
			t.Fatalf("decoder.Next: %v", nerr)
		}
		if el.IsEndContainer {
			return out
		}
		if el.Tag.Kind == tlv.TagKindContext {
			out[el.Tag.Number] = el
		}
	}
}

// TestDefaultAttrWriter_ClosureOverallCurrentState pins that
// OverallCurrentState encodes as a structure carrying Position and
// SecureState under their spec field tags.
func TestDefaultAttrWriter_ClosureOverallCurrentState(t *testing.T) {
	t.Parallel()
	pos := clusterwire.ClosureCurrentPositionOpenedForVentilation
	secure := false
	fields := decodeClosureStructFields(t, im.AttributeValue{Value: &clusterwire.ClosureOverallCurrentState{
		Position:    &pos,
		SecureState: &secure,
	}})

	position, ok := fields[uint32(clusterwire.ClosureOverallStateFieldPosition)]
	if !ok {
		t.Fatal("no Position field (tag 0) in the encoded struct")
	}
	if position.Uint != uint64(clusterwire.ClosureCurrentPositionOpenedForVentilation) {
		t.Errorf("Position = %d, want %d (OpenedForVentilation)",
			position.Uint, clusterwire.ClosureCurrentPositionOpenedForVentilation)
	}
	secureState, ok := fields[uint32(clusterwire.ClosureOverallStateFieldSecureState)]
	if !ok {
		t.Fatal("no SecureState field (tag 3) in the encoded struct")
	}
	if secureState.Bool {
		t.Error("SecureState = true, want false at the ventilation position")
	}

	// Latch and Speed belong to features this profile does not advertise
	// and must be absent, not null: a null claims the field exists and
	// has no value, which is a different statement about conformance.
	if _, present := fields[uint32(clusterwire.ClosureOverallStateFieldLatch)]; present {
		t.Error("Latch (tag 1) is encoded, but MotionLatching is not advertised")
	}
	if _, present := fields[uint32(clusterwire.ClosureOverallStateFieldSpeed)]; present {
		t.Error("Speed (tag 2) is encoded, but Speed is not advertised")
	}
}

// TestDefaultAttrWriter_ClosureOverallCurrentStateNullFields pins that an
// unobserved position encodes as TLV null inside the struct rather than
// as the zero enum value, which reads as FullyClosed.
func TestDefaultAttrWriter_ClosureOverallCurrentStateNullFields(t *testing.T) {
	t.Parallel()
	fields := decodeClosureStructFields(t, im.AttributeValue{Value: &clusterwire.ClosureOverallCurrentState{}})

	position, ok := fields[uint32(clusterwire.ClosureOverallStateFieldPosition)]
	if !ok {
		t.Fatal("no Position field (tag 0) in the encoded struct")
	}
	if !position.IsNull {
		t.Errorf("Position encoded as %d, want null — the zero value reads as FullyClosed", position.Uint)
	}
	secureState, ok := fields[uint32(clusterwire.ClosureOverallStateFieldSecureState)]
	if !ok {
		t.Fatal("no SecureState field (tag 3) in the encoded struct")
	}
	if !secureState.IsNull {
		t.Error("SecureState encoded as a value, want null")
	}
}

// TestDefaultAttrWriter_ClosureOverallTargetState pins the target-state
// struct encoding.
func TestDefaultAttrWriter_ClosureOverallTargetState(t *testing.T) {
	t.Parallel()
	pos := clusterwire.ClosureTargetPositionMoveToVentilationPosition
	fields := decodeClosureStructFields(t, im.AttributeValue{
		Value: &clusterwire.ClosureOverallTargetState{Position: &pos},
	})
	position, ok := fields[uint32(clusterwire.ClosureOverallStateFieldPosition)]
	if !ok {
		t.Fatal("no Position field (tag 0) in the encoded struct")
	}
	if position.Uint != uint64(clusterwire.ClosureTargetPositionMoveToVentilationPosition) {
		t.Errorf("Position = %d, want %d (MoveToVentilationPosition)",
			position.Uint, clusterwire.ClosureTargetPositionMoveToVentilationPosition)
	}
}

// TestDefaultAttrWriter_ClosureErrorListEncodesAsAnArray pins that
// CurrentErrorList reaches the wire as a TLV array.
//
// ClosureErrorList is a named slice type precisely so it does not land in
// the writer's `case []byte`: []uint8 is []byte in Go, and an octet
// string where a controller expects an array is a wire-shape error that
// some stacks accept quietly and others abort the read on.
func TestDefaultAttrWriter_ClosureErrorListEncodesAsAnArray(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	defaultAttributeValueWriter(enc, tlv.AnonymousTag(), im.AttributeValue{
		Value: clusterwire.ClosureErrorList{
			clusterwire.ClosureErrorPhysicallyBlocked,
			clusterwire.ClosureErrorBlockedBySensor,
		},
	})
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encoder.Bytes: %v", err)
	}
	d := tlv.NewDecoder(b)
	open, err := d.Next()
	if err != nil {
		t.Fatalf("decoder.Next: %v", err)
	}
	if open.Type != tlv.TypeArray {
		t.Fatalf("encoded as type 0x%02X, want an array (0x%02X)", open.Type, tlv.TypeArray)
	}
	var got []uint64
	for {
		el, nerr := d.Next()
		if nerr != nil {
			t.Fatalf("decoder.Next: %v", nerr)
		}
		if el.IsEndContainer {
			break
		}
		got = append(got, el.Uint)
	}
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Errorf("entries = %v, want [0 1]", got)
	}
}

// TestDefaultAttrWriter_ClosureNilStructEncodesAsNull pins that a nil
// struct pointer encodes as a null attribute — the shape a quality-X
// attribute takes when the whole struct is unknown.
func TestDefaultAttrWriter_ClosureNilStructEncodesAsNull(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		value any
	}{
		{"OverallCurrentState", (*clusterwire.ClosureOverallCurrentState)(nil)},
		{"OverallTargetState", (*clusterwire.ClosureOverallTargetState)(nil)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			el := encodeOne(t, im.AttributeValue{Value: tc.value})
			if el.Type != tlv.TypeNull || !el.IsNull {
				t.Errorf("type = 0x%02X isNull=%v, want a null element", el.Type, el.IsNull)
			}
		})
	}
}
