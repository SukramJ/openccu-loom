// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package message

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Message Flags bit layout (Core Spec §4.4.1.1).
//
//	Bits 7-4: Version (always 0 in this protocol revision)
//	Bit  3:   reserved (0)
//	Bit  2:   S — Source Node ID present
//	Bits 1-0: DSIZ — Destination Node ID size encoding
const (
	msgFlagVersionMask = 0xF0
	msgFlagVersion0    = 0x00
	msgFlagSourcePres  = 0x04
	msgFlagDSizMask    = 0x03
)

// DestSize encodes the Destination Node ID width.
type DestSize uint8

// DestSize values.
const (
	DestNone   DestSize = 0b00 // no destination node id
	DestNodeID DestSize = 0b01 // 64-bit destination node id
	DestGroup  DestSize = 0b10 // 16-bit group destination
)

// Security Flags bit layout (Core Spec §4.4.1.3).
//
//	Bit  7:   P — Privacy enhancement
//	Bit  6:   C — Control message
//	Bit  5:   MX — Message Extensions present
//	Bits 4-0: Session Type (0 = unicast unencrypted, 1 = group)
const (
	secFlagSessionTypeMask = 0x1F
	secFlagControl         = 0x40
	secFlagPrivacy         = 0x80
	secFlagExtensions      = 0x20
)

// SessionType values.
type SessionType uint8

// SessionType values.
const (
	SessionUnsecured SessionType = 0
	SessionGroup     SessionType = 1
)

// Header is the decoded form of the Matter unencrypted message
// header. The header always consumes 8 bytes; SourceNodeID and
// DestNodeID add another 0–10 bytes depending on Flags.
type Header struct {
	// SessionID identifies the secure session — 0 for unsecured.
	SessionID uint16

	// MessageCounter is the per-session monotonic counter.
	MessageCounter uint32

	// HasSourceNodeID flips the Flags S bit when set.
	HasSourceNodeID bool
	SourceNodeID    uint64

	DestSize     DestSize
	DestNodeID   uint64 // 64-bit destination
	DestGroupID  uint16 // 16-bit group
	SessionType  SessionType
	Privacy      bool
	Control      bool
	HasExtension bool
}

// Errors.
var (
	// ErrTruncated is returned when the wire payload ends before a
	// complete header is read.
	ErrTruncated = errors.New("message: truncated header")

	// ErrUnsupportedVersion is returned for non-zero version bits.
	ErrUnsupportedVersion = errors.New("message: unsupported header version")

	// ErrReservedDestSize is returned when the DSIZ field carries the
	// reserved 0b11 value.
	ErrReservedDestSize = errors.New("message: reserved DSIZ value")
)

// Marshal encodes the message header into a fresh byte slice.
func (h Header) Marshal() []byte {
	buf := make([]byte, 0, 18)
	flags := byte(msgFlagVersion0)
	if h.HasSourceNodeID {
		flags |= msgFlagSourcePres
	}
	flags |= byte(h.DestSize) & msgFlagDSizMask
	buf = append(buf, flags)
	buf = binary.LittleEndian.AppendUint16(buf, h.SessionID)

	secFlags := byte(h.SessionType) & secFlagSessionTypeMask
	if h.Privacy {
		secFlags |= secFlagPrivacy
	}
	if h.Control {
		secFlags |= secFlagControl
	}
	if h.HasExtension {
		secFlags |= secFlagExtensions
	}
	buf = append(buf, secFlags)
	buf = binary.LittleEndian.AppendUint32(buf, h.MessageCounter)

	if h.HasSourceNodeID {
		buf = binary.LittleEndian.AppendUint64(buf, h.SourceNodeID)
	}
	switch h.DestSize {
	case DestNone:
		// no bytes
	case DestNodeID:
		buf = binary.LittleEndian.AppendUint64(buf, h.DestNodeID)
	case DestGroup:
		buf = binary.LittleEndian.AppendUint16(buf, h.DestGroupID)
	}
	return buf
}

// UnmarshalHeader decodes a Matter message header. Returns the
// decoded header and the number of bytes consumed; the caller can
// slice the buffer past that boundary to get the Protocol Header +
// payload.
func UnmarshalHeader(buf []byte) (Header, int, error) {
	const fixed = 1 + 2 + 1 + 4 // flags + session + secflags + counter
	if len(buf) < fixed {
		return Header{}, 0, fmt.Errorf("%w: need %d bytes, got %d", ErrTruncated, fixed, len(buf))
	}
	flags := buf[0]
	if flags&msgFlagVersionMask != msgFlagVersion0 {
		return Header{}, 0, fmt.Errorf("%w: flags=0x%02X", ErrUnsupportedVersion, flags)
	}
	h := Header{
		SessionID:       binary.LittleEndian.Uint16(buf[1:]),
		MessageCounter:  binary.LittleEndian.Uint32(buf[4:]),
		HasSourceNodeID: flags&msgFlagSourcePres != 0,
		DestSize:        DestSize(flags & msgFlagDSizMask),
	}
	secFlags := buf[3]
	h.SessionType = SessionType(secFlags & secFlagSessionTypeMask)
	h.Privacy = secFlags&secFlagPrivacy != 0
	h.Control = secFlags&secFlagControl != 0
	h.HasExtension = secFlags&secFlagExtensions != 0

	pos := fixed
	if h.HasSourceNodeID {
		if len(buf) < pos+8 {
			return Header{}, 0, fmt.Errorf("%w: source node id", ErrTruncated)
		}
		h.SourceNodeID = binary.LittleEndian.Uint64(buf[pos:])
		pos += 8
	}
	switch h.DestSize {
	case DestNone:
		// nothing
	case DestNodeID:
		if len(buf) < pos+8 {
			return Header{}, 0, fmt.Errorf("%w: dest node id", ErrTruncated)
		}
		h.DestNodeID = binary.LittleEndian.Uint64(buf[pos:])
		pos += 8
	case DestGroup:
		if len(buf) < pos+2 {
			return Header{}, 0, fmt.Errorf("%w: dest group", ErrTruncated)
		}
		h.DestGroupID = binary.LittleEndian.Uint16(buf[pos:])
		pos += 2
	default:
		return Header{}, 0, fmt.Errorf("%w: 0b%02b", ErrReservedDestSize, h.DestSize)
	}
	return h, pos, nil
}

// Exchange Flags bit layout (Core Spec §4.4.2.1).
//
//	Bit 7-5: reserved
//	Bit 4:   V — Vendor (VID present)
//	Bit 3:   SX — Secured Extensions present
//	Bit 2:   R — Reliability bit (peer should ack)
//	Bit 1:   A — Acknowledgement counter present
//	Bit 0:   I — Initiator
const (
	exchFlagInitiator   = 0x01
	exchFlagAck         = 0x02
	exchFlagReliability = 0x04
	exchFlagSecuredExt  = 0x08
	exchFlagVendor      = 0x10
)

// ProtocolHeader is the decoded form of the Matter Protocol Header.
type ProtocolHeader struct {
	Initiator     bool
	NeedsAck      bool
	HasAck        bool
	HasSecuredExt bool
	HasVendorID   bool
	Opcode        uint8
	ExchangeID    uint16
	ProtocolID    uint16
	VendorID      uint16
	AckCounter    uint32 // valid when HasAck
}

// Marshal encodes the protocol header into a fresh byte slice.
func (h ProtocolHeader) Marshal() []byte {
	buf := make([]byte, 0, 12)
	flags := byte(0)
	if h.Initiator {
		flags |= exchFlagInitiator
	}
	if h.HasAck {
		flags |= exchFlagAck
	}
	if h.NeedsAck {
		flags |= exchFlagReliability
	}
	if h.HasSecuredExt {
		flags |= exchFlagSecuredExt
	}
	if h.HasVendorID {
		flags |= exchFlagVendor
	}
	buf = append(buf, flags, h.Opcode)
	buf = binary.LittleEndian.AppendUint16(buf, h.ExchangeID)
	buf = binary.LittleEndian.AppendUint16(buf, h.ProtocolID)
	if h.HasVendorID {
		buf = binary.LittleEndian.AppendUint16(buf, h.VendorID)
	}
	if h.HasAck {
		buf = binary.LittleEndian.AppendUint32(buf, h.AckCounter)
	}
	return buf
}

// UnmarshalProtocolHeader decodes a Matter Protocol Header. Returns
// the header and bytes consumed.
func UnmarshalProtocolHeader(buf []byte) (ProtocolHeader, int, error) {
	const fixed = 1 + 1 + 2 + 2 // flags + opcode + exchID + protID
	if len(buf) < fixed {
		return ProtocolHeader{}, 0, fmt.Errorf("%w: protocol header needs %d bytes, got %d", ErrTruncated, fixed, len(buf))
	}
	flags := buf[0]
	h := ProtocolHeader{
		Initiator:     flags&exchFlagInitiator != 0,
		NeedsAck:      flags&exchFlagReliability != 0,
		HasAck:        flags&exchFlagAck != 0,
		HasSecuredExt: flags&exchFlagSecuredExt != 0,
		HasVendorID:   flags&exchFlagVendor != 0,
		Opcode:        buf[1],
		ExchangeID:    binary.LittleEndian.Uint16(buf[2:]),
		ProtocolID:    binary.LittleEndian.Uint16(buf[4:]),
	}
	pos := fixed
	if h.HasVendorID {
		if len(buf) < pos+2 {
			return ProtocolHeader{}, 0, fmt.Errorf("%w: vendor id", ErrTruncated)
		}
		h.VendorID = binary.LittleEndian.Uint16(buf[pos:])
		pos += 2
	}
	if h.HasAck {
		if len(buf) < pos+4 {
			return ProtocolHeader{}, 0, fmt.Errorf("%w: ack counter", ErrTruncated)
		}
		h.AckCounter = binary.LittleEndian.Uint32(buf[pos:])
		pos += 4
	}
	return h, pos, nil
}
