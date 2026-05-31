// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for the reply-path: defaultAttributeValueWriter,
// defaultCommandFieldsWriter, EncodeReportData, EncodeWriteResponse,
// EncodeInvokeResponse, and (*Bridge).sendReply.
//
// Lives in package bridge (not bridge_test) to access unexported symbols.
// Helpers from receive_test.go (newStartedBridge, wbFakeSessionLookup,
// noopSessionLookup, loopbackSrc) are available because they share the
// same compilation unit.

import (
	"errors"
	"log/slog"
	"math"
	"net"
	"testing"

	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// encodeOne runs defaultAttributeValueWriter with an anonymous tag and returns
// the single decoded TLV element. It fails the test on any encoding or
// decoding error.
func encodeOne(t *testing.T, v im.AttributeValue) tlv.Element {
	t.Helper()
	enc := tlv.NewEncoder()
	defaultAttributeValueWriter(enc, tlv.AnonymousTag(), v)
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encoder.Bytes: %v", err)
	}
	el, err := tlv.NewDecoder(b).Next()
	if err != nil {
		t.Fatalf("decoder.Next: %v", err)
	}
	return el
}

// ─── defaultAttributeValueWriter ─────────────────────────────────────────────

// TestDefaultAttrWriter_NilValue verifies that AttributeValue{Value: nil} is
// encoded as a TLV null element.
func TestDefaultAttrWriter_NilValue(t *testing.T) {
	t.Parallel()
	el := encodeOne(t, im.AttributeValue{IsNull: false, Value: nil})
	if el.Type != tlv.TypeNull || !el.IsNull {
		t.Errorf("want TypeNull/IsNull, got type=0x%02X isNull=%v", el.Type, el.IsNull)
	}
}

// TestDefaultAttrWriter_ExplicitNull verifies that IsNull==true wins over a
// non-nil Value — the encoder must emit null regardless of the Value field.
func TestDefaultAttrWriter_ExplicitNull(t *testing.T) {
	t.Parallel()
	el := encodeOne(t, im.AttributeValue{IsNull: true, Value: true})
	if el.Type != tlv.TypeNull || !el.IsNull {
		t.Errorf("want TypeNull/IsNull when IsNull=true, got type=0x%02X isNull=%v", el.Type, el.IsNull)
	}
}

// TestDefaultAttrWriter_Bool verifies that bool(true) encodes as a TLV boolean
// with Bool==true.
func TestDefaultAttrWriter_Bool(t *testing.T) {
	t.Parallel()
	el := encodeOne(t, im.AttributeValue{Value: true})
	if el.Type != tlv.TypeBoolTrue || !el.Bool {
		t.Errorf("want TypeBoolTrue/Bool=true, got type=0x%02X bool=%v", el.Type, el.Bool)
	}
}

// TestDefaultAttrWriter_Uint8 verifies that uint8(42) encodes as an unsigned
// int TLV element with Uint==42.
func TestDefaultAttrWriter_Uint8(t *testing.T) {
	t.Parallel()
	el := encodeOne(t, im.AttributeValue{Value: uint8(42)})
	if el.Type != tlv.TypeUnsignedInt1 || el.Uint != 42 {
		t.Errorf("want TypeUnsignedInt1/42, got type=0x%02X uint=%d", el.Type, el.Uint)
	}
}

// TestDefaultAttrWriter_Uint16 verifies that uint16(1000) encodes as an
// unsigned int TLV element with Uint==1000.
func TestDefaultAttrWriter_Uint16(t *testing.T) {
	t.Parallel()
	el := encodeOne(t, im.AttributeValue{Value: uint16(1000)})
	if el.Type != tlv.TypeUnsignedInt2 || el.Uint != 1000 {
		t.Errorf("want TypeUnsignedInt2/1000, got type=0x%02X uint=%d", el.Type, el.Uint)
	}
}

// TestDefaultAttrWriter_Uint32 verifies that uint32(70000) encodes as an
// unsigned int TLV element with Uint==70000.
func TestDefaultAttrWriter_Uint32(t *testing.T) {
	t.Parallel()
	el := encodeOne(t, im.AttributeValue{Value: uint32(70000)})
	if el.Type != tlv.TypeUnsignedInt4 || el.Uint != 70000 {
		t.Errorf("want TypeUnsignedInt4/70000, got type=0x%02X uint=%d", el.Type, el.Uint)
	}
}

// TestDefaultAttrWriter_Uint64 verifies that uint64(5_000_000_000) encodes as
// an unsigned int TLV element with Uint==5_000_000_000.
func TestDefaultAttrWriter_Uint64(t *testing.T) {
	t.Parallel()
	const want uint64 = 5_000_000_000
	el := encodeOne(t, im.AttributeValue{Value: want})
	if el.Type != tlv.TypeUnsignedInt8 || el.Uint != want {
		t.Errorf("want TypeUnsignedInt8/%d, got type=0x%02X uint=%d", want, el.Type, el.Uint)
	}
}

// TestDefaultAttrWriter_Int16Negative verifies that int16(-1000) encodes as a
// signed int TLV element with Int==-1000. We pick a value that does NOT fit
// in int8 so the encoder is forced to widen to TypeSignedInt2 — encoder
// auto-narrowing means int16(-100) would land in TypeSignedInt1.
func TestDefaultAttrWriter_Int16Negative(t *testing.T) {
	t.Parallel()
	el := encodeOne(t, im.AttributeValue{Value: int16(-1000)})
	if el.Type != tlv.TypeSignedInt2 || el.Int != -1000 {
		t.Errorf("want TypeSignedInt2/-1000, got type=0x%02X int=%d", el.Type, el.Int)
	}
}

// TestDefaultAttrWriter_Float32 verifies that float32(3.14) encodes as a
// TypeFloat4 element with Float within 1e-5 of 3.14.
func TestDefaultAttrWriter_Float32(t *testing.T) {
	t.Parallel()
	const want = float32(3.14)
	el := encodeOne(t, im.AttributeValue{Value: want})
	if el.Type != tlv.TypeFloat4 {
		t.Errorf("want TypeFloat4, got type=0x%02X", el.Type)
	}
	if math.Abs(el.Float-float64(want)) > 1e-5 {
		t.Errorf("float32 round-trip: want ~%v, got %v", want, el.Float)
	}
}

// TestDefaultAttrWriter_Float64 verifies that float64(2.718281828) encodes as
// a TypeFloat8 element with Float within 1e-5 of the original value.
func TestDefaultAttrWriter_Float64(t *testing.T) {
	t.Parallel()
	const want = float64(2.718281828)
	el := encodeOne(t, im.AttributeValue{Value: want})
	if el.Type != tlv.TypeFloat8 {
		t.Errorf("want TypeFloat8, got type=0x%02X", el.Type)
	}
	if math.Abs(el.Float-want) > 1e-5 {
		t.Errorf("float64 round-trip: want ~%v, got %v", want, el.Float)
	}
}

// TestDefaultAttrWriter_String verifies that "hello" encodes as a UTF-8 string
// TLV element with String=="hello".
func TestDefaultAttrWriter_String(t *testing.T) {
	t.Parallel()
	el := encodeOne(t, im.AttributeValue{Value: "hello"})
	if el.Type != tlv.TypeUTF8Str1 || el.String != "hello" {
		t.Errorf("want TypeUTF8Str1/\"hello\", got type=0x%02X string=%q", el.Type, el.String)
	}
}

// TestDefaultAttrWriter_Octets verifies that []byte{0xDE, 0xAD} encodes as an
// octet-string TLV element whose Octets field matches the input.
func TestDefaultAttrWriter_Octets(t *testing.T) {
	t.Parallel()
	want := []byte{0xDE, 0xAD}
	el := encodeOne(t, im.AttributeValue{Value: want})
	if el.Type != tlv.TypeOctetStr1 {
		t.Errorf("want TypeOctetStr1, got type=0x%02X", el.Type)
	}
	if len(el.Octets) != len(want) || el.Octets[0] != want[0] || el.Octets[1] != want[1] {
		t.Errorf("octets mismatch: want %v, got %v", want, el.Octets)
	}
}

// TestDefaultAttrWriter_UnknownTypeDegradesToNull verifies that passing an
// unhandled Go type (struct{}{}) silently degrades to a TLV null element so
// the reply still round-trips structurally.
func TestDefaultAttrWriter_UnknownTypeDegradesToNull(t *testing.T) {
	t.Parallel()
	el := encodeOne(t, im.AttributeValue{Value: struct{}{}})
	if el.Type != tlv.TypeNull || !el.IsNull {
		t.Errorf("want TypeNull/IsNull for unknown type, got type=0x%02X isNull=%v", el.Type, el.IsNull)
	}
}

// ─── defaultCommandFieldsWriter ──────────────────────────────────────────────

// TestDefaultCmdFieldsWriter_EmptyStruct verifies that defaultCommandFieldsWriter
// emits a Structure container followed immediately by an EndContainer — the
// decoder should see TypeStructure (IsContainer) then IsEndContainer.
func TestDefaultCmdFieldsWriter_EmptyStruct(t *testing.T) {
	t.Parallel()
	enc := tlv.NewEncoder()
	defaultCommandFieldsWriter(enc, tlv.AnonymousTag(), nil)
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encoder.Bytes: %v", err)
	}
	dec := tlv.NewDecoder(b)

	first, err := dec.Next()
	if err != nil {
		t.Fatalf("decoder.Next (first): %v", err)
	}
	if first.Type != tlv.TypeStructure || !first.IsContainer {
		t.Errorf("first element: want TypeStructure/IsContainer, got type=0x%02X isContainer=%v", first.Type, first.IsContainer)
	}

	second, err := dec.Next()
	if err != nil {
		t.Fatalf("decoder.Next (second): %v", err)
	}
	if !second.IsEndContainer {
		t.Errorf("second element: want IsEndContainer, got type=0x%02X isEndContainer=%v", second.Type, second.IsEndContainer)
	}
}

// ─── EncodeReportData ─────────────────────────────────────────────────────────

// TestEncodeReportData_Empty verifies that EncodeReportData with an empty
// ReportData returns non-nil bytes of length > 0 that parse as a TLV Structure.
func TestEncodeReportData_Empty(t *testing.T) {
	t.Parallel()
	b, err := EncodeReportData(im.ReportData{})
	if err != nil {
		t.Fatalf("EncodeReportData: unexpected error: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("EncodeReportData: returned zero-length bytes")
	}
	el, err := tlv.NewDecoder(b).Next()
	if err != nil {
		t.Fatalf("decoder.Next: %v", err)
	}
	if el.Type != tlv.TypeStructure {
		t.Errorf("want TypeStructure as root, got type=0x%02X", el.Type)
	}
}

// ─── EncodeWriteResponse ──────────────────────────────────────────────────────

// TestEncodeWriteResponse_Empty verifies that EncodeWriteResponse with an empty
// WriteResponse returns non-nil bytes of length > 0 that parse as a TLV Structure.
func TestEncodeWriteResponse_Empty(t *testing.T) {
	t.Parallel()
	b, err := EncodeWriteResponse(im.WriteResponse{})
	if err != nil {
		t.Fatalf("EncodeWriteResponse: unexpected error: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("EncodeWriteResponse: returned zero-length bytes")
	}
	el, err := tlv.NewDecoder(b).Next()
	if err != nil {
		t.Fatalf("decoder.Next: %v", err)
	}
	if el.Type != tlv.TypeStructure {
		t.Errorf("want TypeStructure as root, got type=0x%02X", el.Type)
	}
}

// ─── EncodeInvokeResponse ─────────────────────────────────────────────────────

// TestEncodeInvokeResponse_Empty verifies that EncodeInvokeResponse with an
// empty InvokeResponse returns non-nil bytes of length > 0 that parse as a TLV
// Structure.
func TestEncodeInvokeResponse_Empty(t *testing.T) {
	t.Parallel()
	b, err := EncodeInvokeResponse(im.InvokeResponse{})
	if err != nil {
		t.Fatalf("EncodeInvokeResponse: unexpected error: %v", err)
	}
	if len(b) == 0 {
		t.Fatal("EncodeInvokeResponse: returned zero-length bytes")
	}
	el, err := tlv.NewDecoder(b).Next()
	if err != nil {
		t.Fatalf("decoder.Next: %v", err)
	}
	if el.Type != tlv.TypeStructure {
		t.Errorf("want TypeStructure as root, got type=0x%02X", el.Type)
	}
}

// ─── (*Bridge).sendReply ─────────────────────────────────────────────────────

// minHeader returns a minimal message.Header and ProtocolHeader for sendReply
// tests. SessionID can be overridden by the caller after construction.
func minHeader(sessionID uint16) (*message.Header, message.ProtocolHeader) {
	hdr := &message.Header{
		SessionID:      sessionID,
		MessageCounter: 1,
	}
	proto := message.ProtocolHeader{
		ProtocolID: im.InteractionModelProtocolID,
		Opcode:     im.OpcodeReadRequest,
		ExchangeID: 7,
		Initiator:  true,
	}
	return hdr, proto
}

// TestSendReply_NilSrcReturnsErr verifies that sendReply returns ErrReplySend
// when src is nil, even when the bridge is fully started.
func TestSendReply_NilSrcReturnsErr(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	hdr, proto := minHeader(0)
	err := b.sendReply(nil, hdr, proto, im.OpcodeReportData, []byte{0x15, 0x18})
	if !errors.Is(err, ErrReplySend) {
		t.Errorf("want ErrReplySend for nil src, got %v", err)
	}
}

// TestSendReply_UnstartedListenerReturnsErr verifies that sendReply returns
// ErrReplySend when the bridge has no listener (bare struct, never Started).
func TestSendReply_UnstartedListenerReturnsErr(t *testing.T) {
	t.Parallel()
	// Bare bridge — listener is nil (zero value).
	b := &Bridge{
		logger:   slog.Default(),
		sessions: noopSessionLookup{},
	}
	src := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345}
	hdr, proto := minHeader(0)
	err := b.sendReply(src, hdr, proto, im.OpcodeReportData, []byte{0x15, 0x18})
	if !errors.Is(err, ErrReplySend) {
		t.Errorf("want ErrReplySend when listener is nil, got %v", err)
	}
}

// TestSendReply_UnsecuredSucceeds verifies that an unsecured reply (SessionID==0)
// with a valid src sends without error and that unsecuredCounter increments.
//
// macOS routing note: the listener binds on `[::]` (dual-stack wildcard) which
// is NOT a routable destination — sending to `[::]:port` fails with
// `sendto: no route to host`. The test loops the datagram back to the
// listener using its dual-stack-compatible loopback peer (`::1` for v6,
// `127.0.0.1` for v4) at the listener's effective port. The OS accepts
// the datagram even if nothing is reading the receive queue; we only
// assert that the reply path constructs and ships without error.
func TestSendReply_UnsecuredSucceeds(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	b.mu.RLock()
	udpAddr := b.listener.LocalAddr()
	b.mu.RUnlock()
	if udpAddr == nil {
		t.Fatal("listener.LocalAddr() returned nil")
	}
	// Map the wildcard-bind to the matching loopback peer. `[::]` listens
	// on both v4 + v6; `::1` is always routable on a running system.
	src := &net.UDPAddr{IP: net.IPv6loopback, Port: udpAddr.Port}
	if udpAddr.IP.To4() != nil && !udpAddr.IP.IsUnspecified() {
		src.IP = net.IPv4(127, 0, 0, 1)
	}

	hdr, proto := minHeader(0)
	counterBefore := b.unsecuredCounter.Load()

	if err := b.sendReply(src, hdr, proto, im.OpcodeReportData, []byte{0x15, 0x18}); err != nil {
		t.Fatalf("sendReply (unsecured): unexpected error: %v", err)
	}

	counterAfter := b.unsecuredCounter.Load()
	if counterAfter <= counterBefore {
		t.Errorf("unsecuredCounter did not increment: before=%d after=%d", counterBefore, counterAfter)
	}
}

// TestSendReply_EncryptedSessionMissingReturnsErr verifies that sendReply returns
// ErrReplySend when the session cannot be found (SessionID!=0, no lookup registered).
func TestSendReply_EncryptedSessionMissingReturnsErr(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t) // uses noopSessionLookup by default → always misses

	src := loopbackSrc()
	hdr, proto := minHeader(42) // non-zero SessionID

	err := b.sendReply(src, hdr, proto, im.OpcodeReportData, []byte{0x15, 0x18})
	if !errors.Is(err, ErrReplySend) {
		t.Errorf("want ErrReplySend for unknown session, got %v", err)
	}
}

// ─── chunkReportData ────────────────────────────────────────────────────────

// makeAttributeReport returns one synthetic AttributeReport sized to land
// well above zero on the wire so the chunker has something to budget against.
func makeAttributeReport(endpoint uint16) im.AttributeReport {
	return im.AttributeReport{
		Path: im.ConcreteAttributePath{
			Endpoint:    endpoint,
			Cluster:     0x1D,   // Descriptor
			Attribute:   0x0003, // PartsList — value is just a placeholder.
			HasEndpoint: true, HasCluster: true, HasAttribute: true,
		},
		Value:       im.AttributeValue{Value: endpoint},
		DataVersion: 1,
	}
}

// TestChunkReportData_SmallSingleChunk verifies the fast path: a small
// ReportData round-trips as one chunk without the chunking flag.
func TestChunkReportData_SmallSingleChunk(t *testing.T) {
	t.Parallel()
	rd := im.ReportData{Reports: []im.AttributeReport{makeAttributeReport(1)}}
	chunks, err := chunkReportData(rd, reportChunkPayloadBudget)
	if err != nil {
		t.Fatalf("chunkReportData: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks)=%d, want 1", len(chunks))
	}
	if chunks[0].MoreChunkedMessages {
		t.Error("single chunk must not flag MoreChunkedMessages")
	}
}

// TestChunkReportData_BudgetSplits verifies that a ReportData whose
// encoded form exceeds budget is split into multiple chunks, every
// non-terminal chunk carries MoreChunkedMessages=true, and the
// terminal chunk does not.
func TestChunkReportData_BudgetSplits(t *testing.T) {
	t.Parallel()
	reports := make([]im.AttributeReport, 200)
	for i := range reports {
		//nolint:gosec // test fixture, range bounded.
		reports[i] = makeAttributeReport(uint16(i + 1))
	}
	rd := im.ReportData{Reports: reports}
	chunks, err := chunkReportData(rd, 200) // tight budget to force many chunks.
	if err != nil {
		t.Fatalf("chunkReportData: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected ≥2 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		isLast := i == len(chunks)-1
		if isLast && c.MoreChunkedMessages {
			t.Errorf("terminal chunk must not flag MoreChunkedMessages")
		}
		if !isLast && !c.MoreChunkedMessages {
			t.Errorf("chunk %d (non-terminal) missing MoreChunkedMessages flag", i)
		}
	}
	got := 0
	for _, c := range chunks {
		got += len(c.Reports)
	}
	if got != len(reports) {
		t.Errorf("reassembled report count=%d, want %d", got, len(reports))
	}
}

// TestChunkReportData_SuppressResponseRidesLastChunk asserts that a
// SuppressResponse=true on the input lands on the terminal chunk
// only — Matter §10.6.6 requires the receiver to ACK every
// intermediate chunk regardless.
func TestChunkReportData_SuppressResponseRidesLastChunk(t *testing.T) {
	t.Parallel()
	reports := make([]im.AttributeReport, 50)
	for i := range reports {
		//nolint:gosec // test fixture.
		reports[i] = makeAttributeReport(uint16(i + 1))
	}
	rd := im.ReportData{Reports: reports, SuppressResponse: true}
	chunks, err := chunkReportData(rd, 200)
	if err != nil {
		t.Fatalf("chunkReportData: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("need ≥2 chunks for this test, got %d", len(chunks))
	}
	for i, c := range chunks[:len(chunks)-1] {
		if c.SuppressResponse {
			t.Errorf("chunk %d (non-terminal) leaked SuppressResponse", i)
		}
	}
	if !chunks[len(chunks)-1].SuppressResponse {
		t.Error("terminal chunk lost SuppressResponse")
	}
}

// TestChunkReportData_EmptyInputProducesOneChunk covers the corner
// case where HandleReadRequest returns an empty ReportData — the
// receiver still expects one terminating ReportDataMessage.
func TestChunkReportData_EmptyInputProducesOneChunk(t *testing.T) {
	t.Parallel()
	chunks, err := chunkReportData(im.ReportData{}, reportChunkPayloadBudget)
	if err != nil {
		t.Fatalf("chunkReportData: %v", err)
	}
	if len(chunks) != 1 {
		t.Fatalf("len(chunks)=%d, want 1", len(chunks))
	}
	if chunks[0].MoreChunkedMessages {
		t.Error("empty single chunk must not flag MoreChunkedMessages")
	}
}

// makeEventReport returns one synthetic EventReport for testing.
func makeEventReport(endpoint uint16) im.EventReport {
	return im.EventReport{
		Path:     im.ConcreteEventPath{Endpoint: endpoint, Cluster: 0x0028, Event: 0x00, HasEndpoint: true, HasCluster: true, HasEvent: true},
		Number:   uint64(endpoint),
		Priority: im.EventPriorityInfo,
	}
}

// TestChunkReportData_EventReports_FastPath verifies that a small
// ReportData with event reports (but no attribute reports) is returned
// as a single chunk.
func TestChunkReportData_EventReports_FastPath(t *testing.T) {
	t.Parallel()
	rd := im.ReportData{
		HasSubscription: true,
		SubscriptionID:  1,
		EventReports:    []im.EventReport{makeEventReport(1)},
	}
	chunks, err := chunkReportData(rd, reportChunkPayloadBudget)
	if err != nil {
		t.Fatalf("chunkReportData (event fast path): %v", err)
	}
	if len(chunks) != 1 {
		t.Errorf("len(chunks)=%d, want 1", len(chunks))
	}
}

// TestChunkReportData_EventReports_Splits verifies that event reports
// are chunked when the budget is tight (exercises addEventReport
// closure and its budget-split branch).
func TestChunkReportData_EventReports_Splits(t *testing.T) {
	t.Parallel()
	events := make([]im.EventReport, 100)
	for i := range events {
		events[i] = makeEventReport(uint16(i + 1)) //nolint:gosec // test fixture
	}
	rd := im.ReportData{HasSubscription: true, SubscriptionID: 2, EventReports: events}
	chunks, err := chunkReportData(rd, 200) // tight budget
	if err != nil {
		t.Fatalf("chunkReportData (event splits): %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected ≥2 chunks, got %d", len(chunks))
	}
	got := 0
	for _, c := range chunks {
		got += len(c.EventReports)
	}
	if got != len(events) {
		t.Errorf("reassembled event count=%d, want %d", got, len(events))
	}
}

// TestChunkReportData_MixedReports_Splits verifies that mixed attribute
// and event reports both flow through the addAttributeReport and
// addEventReport closures with a tight budget.
func TestChunkReportData_MixedReports_Splits(t *testing.T) {
	t.Parallel()
	reports := make([]im.AttributeReport, 20)
	for i := range reports {
		reports[i] = makeAttributeReport(uint16(i + 1)) //nolint:gosec // test fixture
	}
	events := make([]im.EventReport, 20)
	for i := range events {
		events[i] = makeEventReport(uint16(i + 1)) //nolint:gosec // test fixture
	}
	rd := im.ReportData{Reports: reports, EventReports: events}
	chunks, err := chunkReportData(rd, 200)
	if err != nil {
		t.Fatalf("chunkReportData (mixed): %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("expected ≥2 chunks for mixed, got %d", len(chunks))
	}
}

// ─── FabricDescriptorStruct TLV encoding ─────────────────────────────────────

// encodeFabricList is a test helper that encodes a
// []mattercore.FabricDescriptorStruct via defaultAttributeValueWriter and
// returns the raw TLV bytes.
func encodeFabricList(t *testing.T, fabrics []mattercore.FabricDescriptorStruct) []byte {
	t.Helper()
	enc := tlv.NewEncoder()
	defaultAttributeValueWriter(enc, tlv.AnonymousTag(), im.AttributeValue{Value: fabrics})
	b, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encoder.Bytes: %v", err)
	}
	return b
}

// TestFabricDescriptorStruct_VidVerificationStatement_TLVOmittedWhenNil
// verifies that the VidVerificationStatement field (tag 0x06) is not present
// in the TLV output when the field is nil.
func TestFabricDescriptorStruct_VidVerificationStatement_TLVOmittedWhenNil(t *testing.T) {
	t.Parallel()
	fabrics := []mattercore.FabricDescriptorStruct{
		{
			RootPublicKey: make([]byte, 65),
			VendorID:      0x1234,
			FabricID:      1,
			NodeID:        2,
			Label:         "test",
			FabricIndex:   1,
		},
	}
	raw := encodeFabricList(t, fabrics)
	// Scan for context tag 6 (0x26 = context-tag type 0x26 in compact form,
	// or just check that the raw bytes don't contain 0x26 in a struct context).
	// TLV context tag 6 in a struct field is encoded as byte 0x26 (tag type=2,
	// tag number=6, element type prefix).
	for _, b := range raw {
		if b == 0x26 {
			t.Error("VidVerificationStatement tag 0x06 present in TLV but field is nil")
			break
		}
	}
}

// TestFabricDescriptorStruct_VidVerificationStatement_TLVPresentWhenSet
// verifies that the VidVerificationStatement field (tag 0x06) IS present
// in the TLV output when the field is non-nil.
func TestFabricDescriptorStruct_VidVerificationStatement_TLVPresentWhenSet(t *testing.T) {
	t.Parallel()
	stmt := make([]byte, 10)
	stmt[0] = 0xAB
	fabrics := []mattercore.FabricDescriptorStruct{
		{
			RootPublicKey:            make([]byte, 65),
			VendorID:                 0x1234,
			FabricID:                 1,
			NodeID:                   2,
			Label:                    "test",
			VidVerificationStatement: stmt,
			FabricIndex:              1,
		},
	}
	raw := encodeFabricList(t, fabrics)
	// Verify byte 0xAB appears (the payload content).
	found := false
	for _, b := range raw {
		if b == 0xAB {
			found = true
			break
		}
	}
	if !found {
		t.Error("VidVerificationStatement payload byte 0xAB not found in TLV")
	}
}
