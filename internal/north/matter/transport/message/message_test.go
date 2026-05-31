// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package message

import (
	"errors"
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
	if out != in {
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
	if out != in {
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
	if n != 10 || out != in {
		t.Errorf("decoded %d/%+v want 10/%+v", n, out, in)
	}
}

// TestHeaderSecurityFlagsRoundTrip covers the privacy /
// control / extension bits.
func TestHeaderSecurityFlagsRoundTrip(t *testing.T) {
	in := Header{
		MessageCounter: 1,
		SessionType:    SessionGroup,
		Privacy:        true,
		Control:        true,
		HasExtension:   true,
	}
	wire := in.Marshal()
	out, _, err := UnmarshalHeader(wire)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.SessionType != SessionGroup || !out.Privacy || !out.Control || !out.HasExtension {
		t.Fatalf("flags lost: %+v", out)
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
	if n != 6 || out != in {
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
	if out != in {
		t.Errorf("got %+v, want %+v", out, in)
	}
}

// TestProtocolHeaderWithVendorRoundTrip covers a vendor-specific
// protocol message.
func TestProtocolHeaderWithVendorRoundTrip(t *testing.T) {
	in := ProtocolHeader{
		Opcode:      0x05,
		ExchangeID:  3,
		ProtocolID:  4,
		HasVendorID: true,
		VendorID:    0xFFF1,
	}
	wire := in.Marshal()
	if len(wire) != 6+2 {
		t.Fatalf("len(wire)=%d, want 8", len(wire))
	}
	out, _, err := UnmarshalProtocolHeader(wire)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out != in {
		t.Errorf("got %+v, want %+v", out, in)
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

// TestProtocolHeaderTruncatedVendorID catches missing vendor bytes.
func TestProtocolHeaderTruncatedVendorID(t *testing.T) {
	in := ProtocolHeader{HasVendorID: true, VendorID: 1, Opcode: 1, ProtocolID: 1}
	wire := in.Marshal()
	_, _, err := UnmarshalProtocolHeader(wire[:6])
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
