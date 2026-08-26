// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package binrpc

// Wire-format constants. BIN-RPC packets are big-endian throughout.
//
// Every message is framed by an 8-byte header:
//
//	'B' 'i' 'n' <msgType:u8> <payloadSize:u32>
//
// followed by payloadSize bytes. Requests additionally prefix the
// method name (length-only string, no type tag) before the parameter
// array.
const (
	// Message types.
	msgTypeRequest  uint8 = 0x00
	msgTypeResponse uint8 = 0x01
	msgTypeFault    uint8 = 0xFF

	// Value type tags. Written as u32 big-endian.
	typeInt    uint32 = 0x01
	typeBool   uint32 = 0x02
	typeString uint32 = 0x03
	typeDouble uint32 = 0x04
	typeArray  uint32 = 0x100
	typeStruct uint32 = 0x101

	// mantissaScale is the denominator in BIN-RPC's double
	// representation: value = mantissa * 2^exp / 2^30.
	mantissaScale float64 = 1 << 30

	// MaxMessageSize bounds any BIN-RPC message we accept on read, in
	// bytes. Applied to both the client (response) and server (request).
	MaxMessageSize int64 = 10 * 1024 * 1024

	// initialPayloadCap is the buffer capacity readFrame reserves up
	// front for a frame payload. The buffer then grows with the bytes
	// that actually arrive, so a header declaring a large size but
	// sending no body costs at most this much per connection rather than
	// the full declared size. 64 KiB comfortably holds a real
	// CUxD/CCU callback frame without an early re-grow.
	initialPayloadCap int64 = 64 * 1024

	// maxDecodeDepth bounds how deeply nested arrays/structs may be in
	// a single decoded value. Without it, a crafted message of ~1.3M
	// nested arrays (≈10 MB, under MaxMessageSize) drives readValue into
	// unbounded recursion and crashes the process with a non-recoverable
	// "stack overflow" fatal error. Real CCU/CUxD paramsets nest only a
	// few levels (a struct of values, an array of structs); 64 is far
	// above any legitimate Homematic response.
	maxDecodeDepth = 64

	// minValueWireBytes and minMemberWireBytes are the smallest number of
	// wire bytes an array element / struct member is guaranteed to consume
	// before it can be satisfied: every value starts with a 4-byte type tag,
	// and every struct member additionally starts with a 4-byte name-length
	// field. Element/member counts are bounded by remaining()/min so a
	// crafted count cannot (a) truncate past the guard on 32-bit builds and
	// panic make(), nor (b) drive make() to pre-allocate a slice far larger
	// than the payload can ever fill (each xmlrpc.Member/Value slot is
	// ~16-32 bytes, so an unbounded count amplifies allocation ~4-8x). The
	// bound never rejects a legitimate message: a real payload holding N
	// elements always carries at least min*N bytes.
	minValueWireBytes  = 4
	minMemberWireBytes = 8
)

// marker is the fixed 3-byte preamble of every BIN-RPC packet.
var marker = [3]byte{'B', 'i', 'n'}
