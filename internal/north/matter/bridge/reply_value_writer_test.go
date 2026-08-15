// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for defaultAttributeValueWriter and defaultCommandFieldsWriter.
// These functions are pure TLV encoders; we verify they produce non-empty output
// for each supported type and that the produced bytes round-trip through the
// TLV decoder correctly for the key cases.
// Lives in package bridge to access unexported functions.

import (
	"testing"

	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	mattermeasure "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/measurement"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// verifyNonEmpty encodes using enc and verifies at least one byte was produced.
func verifyNonEmpty(t *testing.T, name string, enc *tlv.Encoder) []byte {
	t.Helper()
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("%s: encoder error: %v", name, err)
	}
	if len(b) == 0 {
		t.Errorf("%s: produced empty bytes", name)
	}
	return b
}

// ─── defaultAttributeValueWriter ─────────────────────────────────────────────

func callAttributeWriter(v im.AttributeValue) *tlv.Encoder {
	enc := tlv.NewEncoder()
	defaultAttributeValueWriter(enc, tlv.AnonymousTag(), v)
	return enc
}

func TestDefaultAttributeValueWriter_Null(t *testing.T) {
	t.Parallel()
	enc := callAttributeWriter(im.AttributeValue{IsNull: true})
	verifyNonEmpty(t, "null", enc)
}

func TestDefaultAttributeValueWriter_NilValue(t *testing.T) {
	t.Parallel()
	enc := callAttributeWriter(im.AttributeValue{})
	verifyNonEmpty(t, "nil value", enc)
}

func TestDefaultAttributeValueWriter_Bool(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "bool", callAttributeWriter(im.AttributeValue{Value: true}))
}

func TestDefaultAttributeValueWriter_Uint8(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "uint8", callAttributeWriter(im.AttributeValue{Value: uint8(42)}))
}

func TestDefaultAttributeValueWriter_Uint16(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "uint16", callAttributeWriter(im.AttributeValue{Value: uint16(1000)}))
}

func TestDefaultAttributeValueWriter_Uint32(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "uint32", callAttributeWriter(im.AttributeValue{Value: uint32(100000)}))
}

func TestDefaultAttributeValueWriter_Uint64(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "uint64", callAttributeWriter(im.AttributeValue{Value: uint64(123456789)}))
}

func TestDefaultAttributeValueWriter_Int8(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "int8", callAttributeWriter(im.AttributeValue{Value: int8(-1)}))
}

func TestDefaultAttributeValueWriter_Int16(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "int16", callAttributeWriter(im.AttributeValue{Value: int16(-300)}))
}

func TestDefaultAttributeValueWriter_Int32(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "int32", callAttributeWriter(im.AttributeValue{Value: int32(-70000)}))
}

func TestDefaultAttributeValueWriter_Int64(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "int64", callAttributeWriter(im.AttributeValue{Value: int64(-1234567890)}))
}

func TestDefaultAttributeValueWriter_Float32(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "float32", callAttributeWriter(im.AttributeValue{Value: float32(3.14)}))
}

func TestDefaultAttributeValueWriter_Float64(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "float64", callAttributeWriter(im.AttributeValue{Value: float64(2.71828)}))
}

func TestDefaultAttributeValueWriter_String(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "string", callAttributeWriter(im.AttributeValue{Value: "hello"}))
}

func TestDefaultAttributeValueWriter_BoundedString(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "BoundedString", callAttributeWriter(im.AttributeValue{Value: tlv.BoundedString{Value: "hi", MaxBytes: 32}}))
}

func TestDefaultAttributeValueWriter_ByteSlice(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "[]byte", callAttributeWriter(im.AttributeValue{Value: []byte{0xDE, 0xAD}}))
}

func TestDefaultAttributeValueWriter_BasicCommissioningInfoStruct(t *testing.T) {
	t.Parallel()
	v := mattercore.BasicCommissioningInfoStruct{
		FailSafeExpiryLengthSeconds:  900,
		MaxCumulativeFailsafeSeconds: 1800,
	}
	verifyNonEmpty(t, "BasicCommissioningInfoStruct", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_CapabilityMinimaStruct(t *testing.T) {
	t.Parallel()
	v := mattercore.CapabilityMinimaStruct{
		CaseSessionsPerFabric:  3,
		SubscriptionsPerFabric: 3,
	}
	verifyNonEmpty(t, "CapabilityMinimaStruct", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_ProductAppearanceStruct(t *testing.T) {
	t.Parallel()
	v := mattercore.ProductAppearanceStruct{Finish: 1, PrimaryColor: 2}
	verifyNonEmpty(t, "ProductAppearanceStruct", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_ACLEntrySlice(t *testing.T) {
	t.Parallel()
	v := []mattercore.AccessControlEntryStruct{
		{Privilege: 5, AuthMode: 2, FabricIndex: 1, Subjects: []uint64{1000}},
		{Privilege: 3, AuthMode: 1, Subjects: nil, Targets: nil},
	}
	verifyNonEmpty(t, "[]AccessControlEntryStruct", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_UnknownType_FallsThrough(t *testing.T) {
	t.Parallel()
	// An unrecognized type should produce a null (fall-through path).
	type unknownType struct{ X int }
	enc := callAttributeWriter(im.AttributeValue{Value: unknownType{X: 1}})
	verifyNonEmpty(t, "unknown type fallthrough", enc)
}

func TestDefaultAttributeValueWriter_NetworkInterfaceStruct_NullableNil(t *testing.T) {
	t.Parallel()
	// OffPremiseServicesReachableIPv4/IPv6 are nil (nullable).
	v := []mattercore.NetworkInterfaceStruct{
		{
			Name:            "eth0",
			IsOperational:   true,
			HardwareAddress: []byte{0x00, 0x1A, 0x2B, 0x3C, 0x4D, 0x5E},
			IPv4Addresses:   [][]byte{{192, 168, 1, 1}},
			IPv6Addresses:   [][]byte{{0xFE, 0x80, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}},
			InterfaceType:   mattercore.InterfaceTypeEthernet,
		},
	}
	verifyNonEmpty(t, "[]NetworkInterfaceStruct (nullable nil)", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_NetworkInterfaceStruct_NullableSet(t *testing.T) {
	t.Parallel()
	// OffPremiseServicesReachableIPv4/IPv6 non-nil to exercise both branches.
	trueBool := true
	falseBool := false
	v := []mattercore.NetworkInterfaceStruct{
		{
			Name:                            "wlan0",
			IsOperational:                   false,
			OffPremiseServicesReachableIPv4: &trueBool,
			OffPremiseServicesReachableIPv6: &falseBool,
			HardwareAddress:                 []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF},
			InterfaceType:                   mattercore.InterfaceTypeWiFi,
		},
	}
	verifyNonEmpty(t, "[]NetworkInterfaceStruct (nullable set)", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_FabricDescriptorSlice(t *testing.T) {
	t.Parallel()
	v := []mattercore.FabricDescriptorStruct{
		{
			RootPublicKey: make([]byte, 65),
			VendorID:      0x1234,
			FabricID:      0xDEADBEEFCAFEBABE,
			NodeID:        0x0000000000000001,
			Label:         "home",
			FabricIndex:   1,
		},
	}
	verifyNonEmpty(t, "[]FabricDescriptorStruct", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_NOCSlice_NoICAC(t *testing.T) {
	t.Parallel()
	v := []mattercore.NOCStruct{
		{NOC: []byte{0xAA, 0xBB}, FabricIndex: 1}, // ICAC empty → null
	}
	verifyNonEmpty(t, "[]NOCStruct (no ICAC)", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_NOCSlice_WithICAC(t *testing.T) {
	t.Parallel()
	v := []mattercore.NOCStruct{
		{NOC: []byte{0xAA}, ICAC: []byte{0xBB, 0xCC}, FabricIndex: 2},
	}
	verifyNonEmpty(t, "[]NOCStruct (with ICAC)", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_ByteSliceSlice(t *testing.T) {
	t.Parallel()
	v := [][]byte{{0xDE, 0xAD}, {0xBE, 0xEF}}
	verifyNonEmpty(t, "[][]byte", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_AnySlice_Empty(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "[]any (empty)", callAttributeWriter(im.AttributeValue{Value: []any{}}))
}

func TestDefaultAttributeValueWriter_Uint32Slice(t *testing.T) {
	t.Parallel()
	v := []uint32{0x0028, 0xFFF8, 0xFFF9, 0xFFFA, 0xFFFB, 0xFFFC}
	verifyNonEmpty(t, "[]uint32", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_Uint16Slice(t *testing.T) {
	t.Parallel()
	v := []uint16{2, 3, 4}
	verifyNonEmpty(t, "[]uint16", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_NetworkInfoStructSlice(t *testing.T) {
	t.Parallel()
	v := []mattercore.NetworkInfoStruct{
		{NetworkID: []byte{0x01, 0x02, 0x03, 0x04}, Connected: true},
		{NetworkID: []byte{0xFF}, Connected: false},
	}
	verifyNonEmpty(t, "[]NetworkInfoStruct", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_GroupKeyMapStructSlice(t *testing.T) {
	t.Parallel()
	v := []mattercore.GroupKeyMapStruct{
		{GroupID: 1, GroupKeySetID: 2, FabricIndex: 1},
	}
	verifyNonEmpty(t, "[]GroupKeyMapStruct", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_GroupInfoMapStructSlice(t *testing.T) {
	t.Parallel()
	v := []mattercore.GroupInfoMapStruct{
		{GroupID: 1, Endpoints: []uint16{2, 3}, GroupName: "lights", FabricIndex: 1},
	}
	verifyNonEmpty(t, "[]GroupInfoMapStruct", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_TargetStructSlice(t *testing.T) {
	t.Parallel()
	v := []mattercore.TargetStruct{
		{Node: 0x1234, Group: 0, Endpoint: 1, Cluster: 0x0006, FabricIndex: 1},
		{FabricIndex: 2}, // all optional fields zero
	}
	verifyNonEmpty(t, "[]TargetStruct", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_DeviceTypeStructSlice(t *testing.T) {
	t.Parallel()
	v := []mattercore.DeviceTypeStruct{
		{DeviceType: 0x0100, Revision: 2},
		{DeviceType: 0x0013, Revision: 1},
	}
	verifyNonEmpty(t, "[]DeviceTypeStruct", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_AccuracyStructSlice(t *testing.T) {
	t.Parallel()
	v := []mattermeasure.AccuracyStruct{
		{
			MeasurementType:  0x0001,
			Measured:         true,
			MinMeasuredValue: -1000,
			MaxMeasuredValue: 1000,
			AccuracyRanges: []mattermeasure.AccuracyRangeStruct{
				{RangeMin: -1000, RangeMax: 1000},
			},
		},
	}
	verifyNonEmpty(t, "[]AccuracyStruct", callAttributeWriter(im.AttributeValue{Value: v}))
}

// TestDefaultAttributeValueWriter_AccuracyStructSignedMeasuredValues pins
// the wire type of MeasurementAccuracyStruct tags 2 and 3. matter.js
// packages/model/src/standard/elements/measurement-accuracy-struct.element.ts
// declares them MinMeasuredValue / MaxMeasuredValue, both int64 — a typed
// controller decoding them as signed (chip's TLVReader::Get(int64_t&)
// accepts only the Int element types) fails with WRONG_TLV_TYPE on an
// unsigned element and loses a conformance-M attribute of both electrical
// clusters.
func TestDefaultAttributeValueWriter_AccuracyStructSignedMeasuredValues(t *testing.T) {
	t.Parallel()
	v := []mattermeasure.AccuracyStruct{
		{
			MeasurementType:  0x0008,
			Measured:         true,
			MinMeasuredValue: -1000,
			MaxMeasuredValue: 1000,
			AccuracyRanges: []mattermeasure.AccuracyRangeStruct{
				{RangeMin: -1000, RangeMax: 1000},
			},
		},
	}
	b := verifyNonEmpty(t, "[]AccuracyStruct", callAttributeWriter(im.AttributeValue{Value: v}))

	signed := map[tlv.ElementType]bool{
		tlv.TypeSignedInt1: true, tlv.TypeSignedInt2: true,
		tlv.TypeSignedInt4: true, tlv.TypeSignedInt8: true,
	}
	// Walk the flat element stream; the first struct entered is the
	// AccuracyStruct itself, so its context tags 2 and 3 are the first
	// occurrences of those tag numbers.
	seen := map[uint32]tlv.ElementType{}
	dec := tlv.NewDecoder(b)
	for {
		el, err := dec.Next()
		if err != nil {
			break
		}
		if el.IsContainer || el.IsEndContainer || el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		if _, dup := seen[el.Tag.Number]; !dup {
			seen[el.Tag.Number] = el.Type
		}
	}
	for _, tag := range []uint32{2, 3} {
		typ, ok := seen[tag]
		if !ok {
			t.Fatalf("MeasurementAccuracyStruct context tag %d absent from the encoded payload", tag)
		}
		if !signed[typ] {
			t.Errorf("MeasurementAccuracyStruct tag %d encoded as element type 0x%02X, want a signed-int type (int64 per matter.js)", tag, uint8(typ))
		}
	}
}

func TestDefaultAttributeValueWriter_EnergyMeasurementStruct(t *testing.T) {
	t.Parallel()
	// CumulativeEnergyImported must encode as EnergyMeasurementStruct
	// (anonymous struct container carrying Energy at context tag 0) —
	// a bare int64 is rejected by chip-tool's typed StructDecodeIterator
	// with "Wrong TLV type". Matter §2.14.5.2; matter.js
	// electrical-energy-measurement.element.ts:88-96.
	b := verifyNonEmpty(t, "EnergyMeasurementStruct",
		callAttributeWriter(im.AttributeValue{Value: mattermeasure.EnergyMeasurementStruct{Energy: 1500000}}))
	// TLV control byte 0x15 = structure, anonymous tag. A signed-int
	// encoding (control 0x00-0x03) here would regress to the bare-int
	// wire shape.
	if b[0] != 0x15 {
		t.Fatalf("EnergyMeasurementStruct leading control byte = 0x%02X, want 0x15 (anonymous structure)", b[0])
	}
}

func TestDefaultAttributeValueWriter_StartUpEvent(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "StartUpEvent", callAttributeWriter(im.AttributeValue{Value: mattercore.StartUpEvent{SoftwareVersion: 42}}))
}

func TestDefaultAttributeValueWriter_ShutDownEvent(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "ShutDownEvent", callAttributeWriter(im.AttributeValue{Value: mattercore.ShutDownEvent{}}))
}

func TestDefaultAttributeValueWriter_LeaveEvent(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "LeaveEvent", callAttributeWriter(im.AttributeValue{Value: mattercore.LeaveEvent{FabricIndex: 3}}))
}

func TestDefaultAttributeValueWriter_BootReasonEvent(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "BootReasonEvent", callAttributeWriter(im.AttributeValue{Value: mattercore.BootReasonEvent{BootReason: 2}}))
}

func TestDefaultAttributeValueWriter_ReachableChangedEvent(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "ReachableChangedEvent", callAttributeWriter(im.AttributeValue{Value: mattercore.ReachableChangedEvent{ReachableNewValue: true}}))
}

func TestDefaultAttributeValueWriter_AccessControlEntryChangedEvent_NilLatestValue(t *testing.T) {
	t.Parallel()
	nodeID := uint64(0xABCD)
	passcodeID := uint16(7)
	v := mattercore.AccessControlEntryChangedEvent{
		AdminNodeID:     &nodeID,
		AdminPasscodeID: &passcodeID,
		ChangeType:      mattercore.AccessControlChangeTypeAdded,
		LatestValue:     nil,
		FabricIndex:     1,
	}
	verifyNonEmpty(t, "AccessControlEntryChangedEvent (nil LatestValue)", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_AccessControlEntryChangedEvent_WithLatestValue(t *testing.T) {
	t.Parallel()
	lv := &mattercore.AccessControlEntryStruct{
		Privilege:   5,
		AuthMode:    2,
		Subjects:    []uint64{1000, 2000},
		FabricIndex: 1,
	}
	v := mattercore.AccessControlEntryChangedEvent{
		AdminNodeID:     nil,
		AdminPasscodeID: nil,
		ChangeType:      mattercore.AccessControlChangeTypeChanged,
		LatestValue:     lv,
		FabricIndex:     1,
	}
	verifyNonEmpty(t, "AccessControlEntryChangedEvent (with LatestValue)", callAttributeWriter(im.AttributeValue{Value: v}))
}

func TestDefaultAttributeValueWriter_AccessControlEntryChangedEvent_WithTargets(t *testing.T) {
	t.Parallel()
	cluster := uint32(0x0006)
	endpoint := uint16(1)
	devType := uint32(0x0100)
	lv := &mattercore.AccessControlEntryStruct{
		Privilege: 3,
		AuthMode:  1,
		Targets: []mattercore.ACLTargetStruct{
			{Cluster: &cluster, Endpoint: &endpoint, DeviceType: &devType},
			{Cluster: nil, Endpoint: nil, DeviceType: nil},
		},
		FabricIndex: 2,
	}
	v := mattercore.AccessControlEntryChangedEvent{
		ChangeType:  mattercore.AccessControlChangeTypeRemoved,
		LatestValue: lv,
		FabricIndex: 2,
	}
	verifyNonEmpty(t, "AccessControlEntryChangedEvent (with targets)", callAttributeWriter(im.AttributeValue{Value: v}))
}

// ─── defaultCommandFieldsWriter ───────────────────────────────────────────────

func callCommandWriter(v any) *tlv.Encoder {
	enc := tlv.NewEncoder()
	defaultCommandFieldsWriter(enc, tlv.AnonymousTag(), v)
	return enc
}

func TestDefaultCommandFieldsWriter_ArmFailSafeResponse(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "ArmFailSafeResponse", callCommandWriter(mattercore.ArmFailSafeResponse{ErrorCode: 0, DebugText: "ok"}))
}

func TestDefaultCommandFieldsWriter_SetRegulatoryConfigResponse(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "SetRegulatoryConfigResponse", callCommandWriter(mattercore.SetRegulatoryConfigResponse{ErrorCode: 1}))
}

func TestDefaultCommandFieldsWriter_CommissioningCompleteResponse(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "CommissioningCompleteResponse", callCommandWriter(mattercore.CommissioningCompleteResponse{}))
}

func TestDefaultCommandFieldsWriter_AttestationResponse(t *testing.T) {
	t.Parallel()
	v := mattercore.AttestationResponse{
		AttestationElements:  []byte{0xAA},
		AttestationSignature: []byte{0xBB},
	}
	verifyNonEmpty(t, "AttestationResponse", callCommandWriter(v))
}

func TestDefaultCommandFieldsWriter_CertificateChainResponse(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "CertificateChainResponse", callCommandWriter(mattercore.CertificateChainResponse{Certificate: []byte{0xCC}}))
}

func TestDefaultCommandFieldsWriter_CSRResponse(t *testing.T) {
	t.Parallel()
	v := mattercore.CSRResponse{
		NOCSRElements:        []byte{0xDD},
		AttestationSignature: []byte{0xEE},
	}
	verifyNonEmpty(t, "CSRResponse", callCommandWriter(v))
}

func TestDefaultCommandFieldsWriter_NOCResponse_NoOptional(t *testing.T) {
	t.Parallel()
	verifyNonEmpty(t, "NOCResponse (no optional)", callCommandWriter(mattercore.NOCResponse{StatusCode: 0}))
}

func TestDefaultCommandFieldsWriter_NOCResponse_WithFabricAndDebug(t *testing.T) {
	t.Parallel()
	v := mattercore.NOCResponse{StatusCode: 0, FabricIndex: 2, DebugText: "added"}
	verifyNonEmpty(t, "NOCResponse (with optional)", callCommandWriter(v))
}

func TestDefaultCommandFieldsWriter_DefaultStatusOnly(t *testing.T) {
	t.Parallel()
	// Unknown type → status-only empty struct.
	verifyNonEmpty(t, "status-only default", callCommandWriter(nil))
}
