// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
//	Bits 4-2: reserved (0)
//	Bits 1-0: Session Type (0 = unicast, 1 = group)
const (
	// secFlagSessionTypeMask isolates the 2-bit Session Type. Mirrors
	// matter.js MessageCodec.ts SecurityFlag.SessionTypeMask (0b11).
	secFlagSessionTypeMask = 0x03
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

	// MessageExtension carries the Message Extensions block (Core Spec
	// §4.4.1.8) when HasExtension is set. The block is reserved in Matter
	// 1.x, but it is part of the message header and therefore part of the
	// AEAD additional authenticated data ([Header.AAD] feeds the AAD in
	// channel.Session.Decrypt), so it MUST round-trip through encode/decode
	// or authentication of any frame that carries it would fail.
	MessageExtension []byte

	// Raw holds the exact on-the-wire header bytes UnmarshalHeader
	// consumed. It is the AEAD additional authenticated data: matter.js
	// authenticates the raw received header, not a re-encoded copy
	// (packages/protocol/src/protocol/ExchangeManager.ts:196-197 —
	// aad = bytes.slice(0, len - applicationPayload.length)), so a
	// reserved wire bit that does not round-trip through Marshal cannot
	// break authentication. Nil for headers built in memory; [Header.AAD]
	// falls back to [Header.Marshal] in that case.
	Raw []byte
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

	// ErrUnsupportedSessionType is returned when the Security Flags carry
	// a Session Type other than unicast (0) or group (1). Mirrors the
	// guard in matter.js MessageCodec.ts decodeFixedHeader.
	ErrUnsupportedSessionType = errors.New("message: unsupported session type")

	// ErrControlMessage is returned when the Security Flags carry the
	// Control (C) bit. Control messages are not implemented (Matter 1.x
	// reserves them); matter.js MessageCodec.ts decodeFixedHeader rejects
	// them the same way.
	ErrControlMessage = errors.New("message: control messages not supported")

	// ErrExtensionLength is returned when an extension block's uint16
	// length prefix claims more bytes than remain in the buffer. Mirrors
	// the bounds check in matter.js
	// packages/protocol/src/codec/MessageCodec.ts decodePacket /
	// decodePayload, which reject a length exceeding the remaining size.
	ErrExtensionLength = errors.New("message: extension length exceeds buffer")
)

// appendExtension encodes a Matter extension block: a little-endian uint16
// length prefix followed by the bytes. Mirrors the length-delimited shape
// matter.js packages/protocol/src/codec/MessageCodec.ts reads back.
func appendExtension(buf, ext []byte) []byte {
	buf = binary.LittleEndian.AppendUint16(buf, uint16(len(ext))) //nolint:gosec // len bounded by MTU; see #20
	return append(buf, ext...)
}

// readExtension decodes a length-delimited extension block from the front
// of buf and returns the extension bytes plus the total bytes consumed
// (2 + length). It bounds-checks the length against the remaining buffer so
// a hostile or truncated length field cannot drive an out-of-range read —
// mirrors the guard added in matter.js MessageCodec.ts decodePacket /
// decodePayload. A zero-length block returns a nil slice so it round-trips
// against a header built without an explicit extension.
func readExtension(buf []byte, what string) (ext []byte, consumed int, err error) {
	if len(buf) < 2 {
		return nil, 0, fmt.Errorf("%w: %s length prefix", ErrTruncated, what)
	}
	n := int(binary.LittleEndian.Uint16(buf))
	if n > len(buf)-2 {
		return nil, 0, fmt.Errorf("%w: %s claims %d of %d remaining", ErrExtensionLength, what, n, len(buf)-2)
	}
	if n == 0 {
		return nil, 2, nil
	}
	ext = make([]byte, n)
	copy(ext, buf[2:2+n])
	return ext, 2 + n, nil
}

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
	if h.HasExtension {
		buf = appendExtension(buf, h.MessageExtension)
	}
	return buf
}

// AAD returns the additional authenticated data that binds this header
// to its AEAD tag. When the header came off the wire ([Header.Raw]
// populated by UnmarshalHeader) the exact received bytes are returned;
// otherwise the header is re-encoded via Marshal. Mirrors matter.js
// authenticating the raw received header bytes
// (ExchangeManager.ts:196-197) while still letting the outbound path
// bind against a freshly-marshaled header.
func (h Header) AAD() []byte {
	if len(h.Raw) > 0 {
		return h.Raw
	}
	return h.Marshal()
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
	// Reject unsupported session types and control messages, mirroring
	// matter.js MessageCodec.ts decodeFixedHeader (reject sessionType not
	// in {Unicast, Group}; reject the Control bit — Matter 1.x reserves
	// control messages). Both guards run on the cleartext prefix.
	if h.SessionType != SessionUnsecured && h.SessionType != SessionGroup {
		return Header{}, 0, fmt.Errorf("%w: %d", ErrUnsupportedSessionType, h.SessionType)
	}
	if secFlags&secFlagControl != 0 {
		return Header{}, 0, ErrControlMessage
	}
	h.Privacy = secFlags&secFlagPrivacy != 0
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
	if h.HasExtension {
		ext, n, err := readExtension(buf[pos:], "message extensions")
		if err != nil {
			return Header{}, 0, err
		}
		h.MessageExtension = ext
		pos += n
	}
	// Capture the exact received header bytes for AEAD authentication —
	// see [Header.Raw]. A copy so a later in-place decrypt of the caller's
	// buffer cannot corrupt the AAD.
	h.Raw = append([]byte(nil), buf[:pos]...)
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

	// SecuredExtension carries the Secured Extensions block (Core Spec
	// §4.4.2.1) when HasSecuredExt is set. Reserved in Matter 1.x; it sits
	// inside the encrypted payload between the protocol header and the
	// application payload, so it must be consumed to locate the payload.
	SecuredExtension []byte
}

// Marshal encodes the protocol header into a fresh byte slice.
//
// Field order follows Core Spec §4.4.3: exchange flags, protocol opcode,
// exchange id, vendor id (only when the V flag is set), protocol id, ack
// counter (only when the A flag is set). The vendor id precedes the protocol
// id because the two together form the 32-bit protocol identifier
// vendorID*0x10000 + protocolID.
// Mirrors matter.js packages/protocol/src/codec/MessageCodec.ts:encodePayloadHeader.
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
	if h.HasVendorID {
		buf = binary.LittleEndian.AppendUint16(buf, h.VendorID)
	}
	buf = binary.LittleEndian.AppendUint16(buf, h.ProtocolID)
	if h.HasAck {
		buf = binary.LittleEndian.AppendUint32(buf, h.AckCounter)
	}
	if h.HasSecuredExt {
		buf = appendExtension(buf, h.SecuredExtension)
	}
	return buf
}

// UnmarshalProtocolHeader decodes a Matter Protocol Header. Returns
// the header and bytes consumed.
//
// The vendor id sits between the exchange id and the protocol id, so a
// V-flagged header needs eight fixed bytes rather than six; both 16-bit
// reads therefore carry their own bounds check.
// Mirrors matter.js packages/protocol/src/codec/MessageCodec.ts:decodePayloadHeader.
func UnmarshalProtocolHeader(buf []byte) (ProtocolHeader, int, error) {
	const base = 1 + 1 + 2 // flags + opcode + exchID
	if len(buf) < base {
		return ProtocolHeader{}, 0, fmt.Errorf("%w: protocol header needs %d bytes, got %d", ErrTruncated, base, len(buf))
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
	}
	pos := base
	if h.HasVendorID {
		if len(buf) < pos+2 {
			return ProtocolHeader{}, 0, fmt.Errorf("%w: vendor id", ErrTruncated)
		}
		h.VendorID = binary.LittleEndian.Uint16(buf[pos:])
		pos += 2
	}
	if len(buf) < pos+2 {
		return ProtocolHeader{}, 0, fmt.Errorf("%w: protocol id", ErrTruncated)
	}
	h.ProtocolID = binary.LittleEndian.Uint16(buf[pos:])
	pos += 2
	if h.HasAck {
		if len(buf) < pos+4 {
			return ProtocolHeader{}, 0, fmt.Errorf("%w: ack counter", ErrTruncated)
		}
		h.AckCounter = binary.LittleEndian.Uint32(buf[pos:])
		pos += 4
	}
	if h.HasSecuredExt {
		ext, n, err := readExtension(buf[pos:], "secured extensions")
		if err != nil {
			return ProtocolHeader{}, 0, err
		}
		h.SecuredExtension = ext
		pos += n
	}
	return h, pos, nil
}
