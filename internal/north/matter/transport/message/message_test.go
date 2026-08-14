// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package message

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

// TestHeaderRoundTripUnsecured covers the typical
// commissioning frame: unsecured session, no source/dest, counter only.
func TestHeaderRoundTripUnsecured(t *testing.T) {
	in := Header{
		SessionID:      0,
		MessageCounter: 0xCAFEBABE,
		SessionType:    SessionUnsecured,
	}
	wire := in.Marshal()
	if len(wire) != 8 {
		t.Fatalf("len(wire)=%d, want 8 (no src/dst)", len(wire))
	}
	out, n, err := UnmarshalHeader(wire)
	if err != nil {
		t.Fatalf("Unmarshal err: %v", err)
	}
	if n != 8 {
		t.Errorf("consumed %d bytes, want 8", n)
	}
	out.Raw = nil // decode-only AAD cache; not part of the semantic header
	if !reflect.DeepEqual(out, in) {
		t.Errorf("got %+v, want %+v", out, in)
	}
}

// TestHeaderWithSourceAndDestNodeID covers the unicast secure
// frame shape (8-byte source + 8-byte dest = 24 bytes total).
func TestHeaderWithSourceAndDestNodeID(t *testing.T) {
	in := Header{
		SessionID:       0x1234,
		MessageCounter:  42,
		HasSourceNodeID: true,
		SourceNodeID:    0x0011223344556677,
		DestSize:        DestNodeID,
		DestNodeID:      0x8899AABBCCDDEEFF,
	}
	wire := in.Marshal()
	if len(wire) != 8+8+8 {
		t.Fatalf("len(wire)=%d, want 24", len(wire))
	}
	out, n, err := UnmarshalHeader(wire)
	if err != nil {
		t.Fatalf("Unmarshal err: %v", err)
	}
	if n != 24 {
		t.Errorf("consumed %d, want 24", n)
	}
	out.Raw = nil // decode-only AAD cache; not part of the semantic header
	if !reflect.DeepEqual(out, in) {
		t.Errorf("got %+v, want %+v", out, in)
	}
}

// TestHeaderWithGroupDest covers the 16-bit group case
// (8 + 2 = 10 bytes).
func TestHeaderWithGroupDest(t *testing.T) {
	in := Header{
		MessageCounter: 1,
		DestSize:       DestGroup,
		DestGroupID:    0xABCD,
	}
	wire := in.Marshal()
	if len(wire) != 10 {
		t.Fatalf("len(wire)=%d, want 10", len(wire))
	}
	out, n, err := UnmarshalHeader(wire)
	if err != nil {
		t.Fatalf("Unmarshal err: %v", err)
	}
	out.Raw = nil // decode-only AAD cache; not part of the semantic header
	if n != 10 || !reflect.DeepEqual(out, in) {
		t.Errorf("decoded %d/%+v want 10/%+v", n, out, in)
	}
}

// TestHeaderSecurityFlagsRoundTrip covers the privacy / extension bits
// and the group session type. The Control bit is exercised separately
// (TestHeaderRejectsControlMessage) because decode rejects it.
func TestHeaderSecurityFlagsRoundTrip(t *testing.T) {
	in := Header{
		MessageCounter: 1,
		SessionType:    SessionGroup,
		Privacy:        true,
		HasExtension:   true,
	}
	wire := in.Marshal()
	out, _, err := UnmarshalHeader(wire)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.SessionType != SessionGroup || !out.Privacy || !out.HasExtension {
		t.Fatalf("flags lost: %+v", out)
	}
}

// TestHeaderRejectsControlMessage — the Control (C) security-flag bit is
// rejected on decode. Mirrors matter.js MessageCodec.ts decodeFixedHeader
// ("Control Messages not supported").
func TestHeaderRejectsControlMessage(t *testing.T) {
	wire := Header{MessageCounter: 1}.Marshal()
	wire[3] |= 0x40 // Control bit
	_, _, err := UnmarshalHeader(wire)
	if !errors.Is(err, ErrControlMessage) {
		t.Fatalf("err = %v, want ErrControlMessage", err)
	}
}

// TestHeaderRejectsUnsupportedSessionType — a Session Type of 2 or 3
// (reserved) is rejected on decode. Mirrors matter.js MessageCodec.ts
// decodeFixedHeader ("Unsupported session type").
func TestHeaderRejectsUnsupportedSessionType(t *testing.T) {
	for _, st := range []byte{2, 3} {
		wire := Header{MessageCounter: 1}.Marshal()
		wire[3] = (wire[3] &^ 0x03) | st
		_, _, err := UnmarshalHeader(wire)
		if !errors.Is(err, ErrUnsupportedSessionType) {
			t.Fatalf("sessionType=%d: err = %v, want ErrUnsupportedSessionType", st, err)
		}
	}
}

// TestHeaderAADIsRawReceivedBytes — after decode, Header.AAD returns the
// exact received header bytes (not a re-encoded copy), mirroring matter.js
// authenticating the raw received header (ExchangeManager.ts:196-197).
func TestHeaderAADIsRawReceivedBytes(t *testing.T) {
	in := Header{
		SessionID:       0x1234,
		MessageCounter:  7,
		HasSourceNodeID: true,
		SourceNodeID:    0x1122334455667788,
	}
	wire := in.Marshal()
	out, n, err := UnmarshalHeader(wire)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !bytes.Equal(out.AAD(), wire[:n]) {
		t.Fatalf("AAD = % X, want raw % X", out.AAD(), wire[:n])
	}
	// A header built in memory (Raw nil) falls back to Marshal.
	if !bytes.Equal(in.AAD(), in.Marshal()) {
		t.Fatalf("in-memory AAD must equal Marshal()")
	}
}

// TestHeaderTruncatedFlags rejects a 4-byte buffer (no
// counter).
func TestHeaderTruncatedFlags(t *testing.T) {
	_, _, err := UnmarshalHeader([]byte{0x00, 0x00, 0x00, 0x00})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

// TestHeaderTruncatedSourceNodeID rejects a buffer that flags
// a source but truncates before the 8 source bytes.
func TestHeaderTruncatedSourceNodeID(t *testing.T) {
	in := Header{HasSourceNodeID: true, SourceNodeID: 1}
	wire := in.Marshal()
	// Truncate to the first 8 bytes (loses source).
	_, _, err := UnmarshalHeader(wire[:8])
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

// TestHeaderRejectsNonZeroVersion catches forward-incompatible
// version bits.
func TestHeaderRejectsNonZeroVersion(t *testing.T) {
	wire := Header{}.Marshal()
	wire[0] |= 0x10 // bit 4 of version field
	_, _, err := UnmarshalHeader(wire)
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err = %v, want ErrUnsupportedVersion", err)
	}
}

// TestHeaderRejectsReservedDestSize catches the 0b11 reserved
// DSIZ encoding.
func TestHeaderRejectsReservedDestSize(t *testing.T) {
	wire := Header{}.Marshal()
	wire[0] |= 0x03 // DSIZ = 0b11 (reserved)
	_, _, err := UnmarshalHeader(wire)
	if !errors.Is(err, ErrReservedDestSize) {
		t.Fatalf("err = %v, want ErrReservedDestSize", err)
	}
}

// --- Protocol Header ---

// TestProtocolHeaderRoundTripMinimum covers the smallest 6-byte form
// (no vendor, no ack).
func TestProtocolHeaderRoundTripMinimum(t *testing.T) {
	in := ProtocolHeader{
		Initiator:  true,
		Opcode:     0x10,
		ExchangeID: 0xABCD,
		ProtocolID: 0x0001,
	}
	wire := in.Marshal()
	if len(wire) != 6 {
		t.Fatalf("len(wire)=%d, want 6", len(wire))
	}
	out, n, err := UnmarshalProtocolHeader(wire)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != 6 || !reflect.DeepEqual(out, in) {
		t.Errorf("decoded %d/%+v want 6/%+v", n, out, in)
	}
}

// TestProtocolHeaderWithAckRoundTrip covers a reliable response
// carrying both NeedsAck and HasAck.
func TestProtocolHeaderWithAckRoundTrip(t *testing.T) {
	in := ProtocolHeader{
		NeedsAck:   true,
		HasAck:     true,
		Opcode:     0x40,
		ExchangeID: 1,
		ProtocolID: 2,
		AckCounter: 0xDEADBEEF,
	}
	wire := in.Marshal()
	if len(wire) != 6+4 {
		t.Fatalf("len(wire)=%d, want 10", len(wire))
	}
	out, _, err := UnmarshalProtocolHeader(wire)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("got %+v, want %+v", out, in)
	}
}

// vendorProtocolHeaderFixture pins the wire bytes of a V-flagged protocol
// header against the field order of Core Spec §4.4.3: exchange flags,
// protocol opcode, exchange id, vendor id (only when V is set), protocol id.
// A round-trip through Marshal/Unmarshal alone cannot see a symmetric
// transposition of the two 16-bit fields, so the fixture is the assertion
// that matters.
var (
	vendorProtocolHeaderFixture = []byte{0x10, 0x05, 0x03, 0x00, 0xF1, 0xFF, 0x04, 0x00}
	vendorProtocolHeaderDecoded = ProtocolHeader{
		Opcode:      0x05,
		ExchangeID:  3,
		ProtocolID:  4,
		HasVendorID: true,
		VendorID:    0xFFF1,
	}
)

// TestProtocolHeaderWithVendorEncodesWireOrder locks the encode side of a
// vendor-specific protocol message against the fixture.
func TestProtocolHeaderWithVendorEncodesWireOrder(t *testing.T) {
	wire := vendorProtocolHeaderDecoded.Marshal()
	if !bytes.Equal(wire, vendorProtocolHeaderFixture) {
		t.Fatalf("wire = % X, want % X", wire, vendorProtocolHeaderFixture)
	}
}

// TestProtocolHeaderWithVendorDecodesWireOrder feeds the fixture bytes back
// in: a peer's vendor-qualified header must not surface its vendor id as the
// protocol id, or the bridge routes a vendor payload into the Interaction
// Model dispatcher.
func TestProtocolHeaderWithVendorDecodesWireOrder(t *testing.T) {
	out, n, err := UnmarshalProtocolHeader(vendorProtocolHeaderFixture)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if n != len(vendorProtocolHeaderFixture) {
		t.Errorf("consumed %d bytes, want %d", n, len(vendorProtocolHeaderFixture))
	}
	if !reflect.DeepEqual(out, vendorProtocolHeaderDecoded) {
		t.Errorf("got %+v, want %+v", out, vendorProtocolHeaderDecoded)
	}
}

// TestProtocolHeaderTruncated catches incomplete fixed portion.
func TestProtocolHeaderTruncated(t *testing.T) {
	_, _, err := UnmarshalProtocolHeader([]byte{0x00, 0x00, 0x00})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

// TestProtocolHeaderTruncatedAckCounter catches missing ack-counter
// bytes after the A flag is set.
func TestProtocolHeaderTruncatedAckCounter(t *testing.T) {
	in := ProtocolHeader{HasAck: true, AckCounter: 1, Opcode: 1, ProtocolID: 1}
	wire := in.Marshal()
	_, _, err := UnmarshalProtocolHeader(wire[:6]) // chop the ack-counter
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

// --- Extensions (Core Spec §4.4.1.8 message / §4.4.2.1 secured) ---

// TestHeaderMessageExtensionRoundTrip locks the message-extension block
// through encode/decode. The block is reserved in Matter 1.x but is part
// of the message header (and thus the AEAD AAD), so it must survive a
// round-trip byte-for-byte.
func TestHeaderMessageExtensionRoundTrip(t *testing.T) {
	in := Header{
		MessageCounter:   7,
		HasExtension:     true,
		MessageExtension: []byte{0xDE, 0xAD, 0xBE, 0xEF},
	}
	wire := in.Marshal()
	// 8 fixed + 2 length prefix + 4 data.
	if len(wire) != 8+2+4 {
		t.Fatalf("len(wire)=%d, want 14", len(wire))
	}
	out, n, err := UnmarshalHeader(wire)
	if err != nil {
		t.Fatalf("Unmarshal err: %v", err)
	}
	if n != len(wire) {
		t.Errorf("consumed %d, want %d", n, len(wire))
	}
	out.Raw = nil // decode-only AAD cache; not part of the semantic header
	if !reflect.DeepEqual(out, in) {
		t.Errorf("got %+v, want %+v", out, in)
	}
}

// TestHeaderExtensionThenProtocolHeader is the regression for the decode
// gap in issue #6: when the extension block is present, the protocol
// header MUST be located after it, not at the extension's first byte.
func TestHeaderExtensionThenProtocolHeader(t *testing.T) {
	hdr := Header{MessageCounter: 1, HasExtension: true, MessageExtension: []byte{0x01, 0x02}}
	proto := ProtocolHeader{Opcode: 0x42, ExchangeID: 0x1234, ProtocolID: 0x0001}
	frame := append(hdr.Marshal(), proto.Marshal()...)

	out, n, err := UnmarshalHeader(frame)
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	if !bytes.Equal(out.MessageExtension, hdr.MessageExtension) {
		t.Fatalf("MessageExtension=%x, want %x", out.MessageExtension, hdr.MessageExtension)
	}
	gotProto, _, err := UnmarshalProtocolHeader(frame[n:])
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader at offset %d: %v", n, err)
	}
	if gotProto.Opcode != proto.Opcode || gotProto.ExchangeID != proto.ExchangeID {
		t.Errorf("protocol header decoded as %+v, want opcode/exch %#x/%#x — offset slipped past the extension",
			gotProto, proto.Opcode, proto.ExchangeID)
	}
}

// TestHeaderMessageExtensionLengthOverflow rejects a length prefix that
// claims more bytes than the buffer holds — the bounds check mirrored
// from matter.js MessageCodec.ts.
func TestHeaderMessageExtensionLengthOverflow(t *testing.T) {
	in := Header{MessageCounter: 1, HasExtension: true, MessageExtension: []byte{0xAA, 0xBB}}
	wire := in.Marshal()
	// Inflate the uint16 length prefix (immediately after the 8-byte fixed
	// header) to exceed the 2 bytes that actually follow it.
	wire[8] = 0xFF
	wire[9] = 0xFF
	_, _, err := UnmarshalHeader(wire)
	if !errors.Is(err, ErrExtensionLength) {
		t.Fatalf("err = %v, want ErrExtensionLength", err)
	}
}

// TestHeaderMessageExtensionTruncatedLength rejects a header whose
// extension flag is set but which ends before the 2-byte length prefix.
func TestHeaderMessageExtensionTruncatedLength(t *testing.T) {
	in := Header{MessageCounter: 1, HasExtension: true}
	wire := in.Marshal()
	_, _, err := UnmarshalHeader(wire[:8]) // drop the length prefix
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

// TestProtocolHeaderSecuredExtensionRoundTrip locks the secured-extension
// block through encode/decode.
func TestProtocolHeaderSecuredExtensionRoundTrip(t *testing.T) {
	in := ProtocolHeader{
		Opcode:           0x05,
		ExchangeID:       9,
		ProtocolID:       1,
		HasSecuredExt:    true,
		SecuredExtension: []byte{0x11, 0x22, 0x33},
	}
	wire := in.Marshal()
	// 6 fixed + 2 length prefix + 3 data.
	if len(wire) != 6+2+3 {
		t.Fatalf("len(wire)=%d, want 11", len(wire))
	}
	out, n, err := UnmarshalProtocolHeader(wire)
	if err != nil {
		t.Fatalf("Unmarshal err: %v", err)
	}
	if n != len(wire) {
		t.Errorf("consumed %d, want %d", n, len(wire))
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("got %+v, want %+v", out, in)
	}
}

// TestProtocolHeaderSecuredExtensionLengthOverflow rejects an inflated
// secured-extension length prefix.
func TestProtocolHeaderSecuredExtensionLengthOverflow(t *testing.T) {
	in := ProtocolHeader{Opcode: 1, ProtocolID: 1, HasSecuredExt: true, SecuredExtension: []byte{0x01}}
	wire := in.Marshal()
	// The length prefix sits right after the 6-byte fixed portion.
	wire[6] = 0xFF
	wire[7] = 0xFF
	_, _, err := UnmarshalProtocolHeader(wire)
	if !errors.Is(err, ErrExtensionLength) {
		t.Fatalf("err = %v, want ErrExtensionLength", err)
	}
}

// TestProtocolHeaderTruncatedVendorID catches missing vendor bytes. The
// vendor id sits directly behind the exchange id, so a buffer that stops
// inside it has five bytes.
func TestProtocolHeaderTruncatedVendorID(t *testing.T) {
	in := ProtocolHeader{HasVendorID: true, VendorID: 1, Opcode: 1, ProtocolID: 1}
	wire := in.Marshal()
	_, _, err := UnmarshalProtocolHeader(wire[:5])
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

// TestProtocolHeaderTruncatedProtocolIDAfterVendor catches a V-flagged
// header whose vendor id is complete but whose protocol id is not: the
// fixed six-byte pre-check is satisfied, yet eight bytes are required.
func TestProtocolHeaderTruncatedProtocolIDAfterVendor(t *testing.T) {
	in := ProtocolHeader{HasVendorID: true, VendorID: 1, Opcode: 1, ProtocolID: 1}
	wire := in.Marshal()
	_, _, err := UnmarshalProtocolHeader(wire[:7])
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

// TestHeaderTruncatedDestNodeID rejects a buffer that sets DSIZ=DestNodeID but
// has fewer than 8 bytes for the destination (exercises line 169-171 of
// message.go — the DestNodeID truncation path in UnmarshalHeader).
func TestHeaderTruncatedDestNodeID(t *testing.T) {
	t.Parallel()
	in := Header{DestSize: DestNodeID, DestNodeID: 0x0011223344556677, MessageCounter: 1}
	wire := in.Marshal()
	// wire has 8 (base) + 8 (dest) = 16 bytes; truncate to 12 (only 4 dest bytes).
	_, _, err := UnmarshalHeader(wire[:12])
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated (dest node id); got %v", err, err)
	}
}

// TestHeaderTruncatedDestGroup rejects a buffer that sets DSIZ=DestGroup but
// has only 1 byte for the destination group (exercises line 175-177 of
// message.go — the DestGroup truncation path in UnmarshalHeader).
func TestHeaderTruncatedDestGroup(t *testing.T) {
	t.Parallel()
	in := Header{DestSize: DestGroup, DestGroupID: 0xABCD, MessageCounter: 1}
	wire := in.Marshal()
	// wire has 8 (base) + 2 (dest group) = 10 bytes; truncate to 9 (only 1 dest byte).
	_, _, err := UnmarshalHeader(wire[:9])
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated (dest group); got %v", err, err)
	}
}

// TestProtocolHeaderWithSecuredExt verifies that setting HasSecuredExt=true
// produces a non-zero secured-extension flag in the serialised frame
// (exercises line 229-231 of message.go — the HasSecuredExt branch in
// ProtocolHeader.Marshal).
func TestProtocolHeaderWithSecuredExt(t *testing.T) {
	t.Parallel()
	in := ProtocolHeader{
		Opcode:        0x01,
		ExchangeID:    10,
		ProtocolID:    0x0001,
		HasSecuredExt: true,
	}
	wire := in.Marshal()
	if len(wire) < 1 {
		t.Fatal("empty wire output")
	}
	// The exchange flags byte is wire[0]; bit 3 (exchFlagSecuredExt = 0x08) must be set.
	if wire[0]&0x08 == 0 {
		t.Errorf("wire[0]=0x%02X missing HasSecuredExt flag (bit3)", wire[0])
	}
}
