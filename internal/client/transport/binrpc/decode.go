// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package binrpc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"golang.org/x/text/encoding/charmap"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// Request is the parsed form of a BIN-RPC request.
type Request struct {
	Method string
	Params []xmlrpc.Value
}

// Response is the parsed form of a BIN-RPC response. Exactly one of
// Value / Fault is non-nil on success.
type Response struct {
	Value xmlrpc.Value
	Fault *hmerr.XMLRPCFault
}

// ReadRequest reads and parses a single BIN-RPC request. The caller is
// responsible for any deadline on r; the bytes consumed from r never
// exceed [MaxMessageSize] plus 8-byte header.
func ReadRequest(r io.Reader) (*Request, error) {
	msgType, payload, err := readFrame(r)
	if err != nil {
		return nil, err
	}
	if msgType != msgTypeRequest {
		return nil, fmt.Errorf("binrpc: expected request (0x%02X), got 0x%02X", msgTypeRequest, msgType)
	}
	pr := &bytesReader{b: payload}
	method, err := readRawString(pr)
	if err != nil {
		return nil, fmt.Errorf("binrpc: read method: %w", err)
	}
	var count uint32
	if err := binary.Read(pr, binary.BigEndian, &count); err != nil {
		return nil, fmt.Errorf("binrpc: read param count: %w", err)
	}
	params, err := readNValues(pr, int(count), 0)
	if err != nil {
		return nil, err
	}
	if pr.remaining() != 0 {
		return nil, fmt.Errorf("binrpc: %d trailing bytes after request payload", pr.remaining())
	}
	return &Request{Method: method, Params: params}, nil
}

// ReadResponse reads a BIN-RPC response or fault from r.
func ReadResponse(r io.Reader) (*Response, error) {
	msgType, payload, err := readFrame(r)
	if err != nil {
		return nil, err
	}
	pr := &bytesReader{b: payload}
	switch msgType {
	case msgTypeResponse:
		v, err := readValue(pr, 0)
		if err != nil {
			return nil, err
		}
		if pr.remaining() != 0 {
			return nil, fmt.Errorf("binrpc: %d trailing bytes after response payload", pr.remaining())
		}
		return &Response{Value: v}, nil
	case msgTypeFault:
		v, err := readValue(pr, 0)
		if err != nil {
			return nil, fmt.Errorf("binrpc: read fault: %w", err)
		}
		code, err := xmlrpc.StructField[xmlrpc.IntValue](v, "faultCode")
		if err != nil {
			return nil, fmt.Errorf("binrpc: fault: %w", err)
		}
		msg, err := xmlrpc.StructField[xmlrpc.StringValue](v, "faultString")
		if err != nil {
			return nil, fmt.Errorf("binrpc: fault: %w", err)
		}
		return &Response{Fault: &hmerr.XMLRPCFault{Code: int(code), Message: string(msg)}}, nil
	default:
		return nil, fmt.Errorf("binrpc: unexpected message type 0x%02X", msgType)
	}
}

// readFrame validates the marker and returns (msgType, payload).
func readFrame(r io.Reader) (msgType uint8, payload []byte, err error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, fmt.Errorf("binrpc: read header: %w", err)
	}
	if hdr[0] != marker[0] || hdr[1] != marker[1] || hdr[2] != marker[2] {
		return 0, nil, fmt.Errorf("binrpc: bad marker %q", hdr[:3])
	}
	msgType = hdr[3]
	size := binary.BigEndian.Uint32(hdr[4:])
	if int64(size) > MaxMessageSize {
		return 0, nil, fmt.Errorf("binrpc: payload size %d exceeds limit %d", size, MaxMessageSize)
	}
	// Grow the buffer with the bytes that actually arrive rather than
	// committing the attacker-declared size up front. A crafted 8-byte
	// header can claim size == MaxMessageSize (10 MiB) while sending no
	// body; make([]byte, size) would zero-allocate the full 10 MiB per
	// connection before the first payload byte is read, so N stalled
	// connections pin N×10 MiB. io.CopyN reads in bounded chunks and
	// errors (ErrUnexpectedEOF) if fewer than size bytes arrive, matching
	// the previous io.ReadFull semantics, but a lying header now costs
	// only initialPayloadCap until real bytes back the claim.
	var buf bytes.Buffer
	buf.Grow(int(min(int64(size), initialPayloadCap)))
	if _, err := io.CopyN(&buf, r, int64(size)); err != nil {
		return 0, nil, fmt.Errorf("binrpc: read payload: %w", err)
	}
	return msgType, buf.Bytes(), nil
}

// readValue reads one type-tagged value. depth tracks array/struct
// nesting; it errors past [maxDecodeDepth] so a crafted deeply-nested
// message cannot drive unbounded recursion into a stack-overflow crash.
func readValue(r *bytesReader, depth int) (xmlrpc.Value, error) {
	if depth > maxDecodeDepth {
		return nil, fmt.Errorf("binrpc: nesting exceeds max depth %d", maxDecodeDepth)
	}
	var tag uint32
	if err := binary.Read(r, binary.BigEndian, &tag); err != nil {
		return nil, fmt.Errorf("binrpc: read type tag: %w", err)
	}
	switch tag {
	case typeInt:
		var n int32
		if err := binary.Read(r, binary.BigEndian, &n); err != nil {
			return nil, err
		}
		return xmlrpc.IntValue(n), nil
	case typeBool:
		var b uint8
		if err := binary.Read(r, binary.BigEndian, &b); err != nil {
			return nil, err
		}
		return xmlrpc.BoolValue(b != 0), nil
	case typeString:
		s, err := readRawString(r)
		if err != nil {
			return nil, err
		}
		return xmlrpc.StringValue(s), nil
	case typeDouble:
		var mant, exp int32
		if err := binary.Read(r, binary.BigEndian, &mant); err != nil {
			return nil, err
		}
		if err := binary.Read(r, binary.BigEndian, &exp); err != nil {
			return nil, err
		}
		f := math.Pow(2, float64(exp)) * float64(mant) / mantissaScale
		// A crafted or malfunctioning peer can drive this computation
		// non-finite (a large exponent overflows to +/-Inf; mant==0 with
		// a large exponent is Inf*0 = NaN). encodeDouble rejects both on
		// the way out; nothing on this read path did, so a non-finite
		// value could reach the model and break every north-bound JSON
		// encoding of the batch or paramset carrying it.
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("binrpc: non-finite double (mantissa=%d exponent=%d)", mant, exp)
		}
		return xmlrpc.DoubleValue(f), nil
	case typeArray:
		var count uint32
		if err := binary.Read(r, binary.BigEndian, &count); err != nil {
			return nil, err
		}
		vs, err := readNValues(r, int(count), depth+1)
		if err != nil {
			return nil, err
		}
		return xmlrpc.ArrayValue(vs), nil
	case typeStruct:
		var count uint32
		if err := binary.Read(r, binary.BigEndian, &count); err != nil {
			return nil, err
		}
		// int(count) can wrap negative on 32-bit builds for count > 2^31,
		// so validate the non-negative domain and bound by the minimum wire
		// footprint per member before allocating; otherwise make() either
		// panics (len out of range) or over-allocates ~8x the payload.
		n := int(count)
		if n < 0 || n > r.remaining()/minMemberWireBytes {
			return nil, fmt.Errorf("binrpc: struct member count %d exceeds remaining %d bytes", count, r.remaining())
		}
		members := make([]xmlrpc.Member, n)
		for i := range n {
			name, err := readRawString(r)
			if err != nil {
				return nil, fmt.Errorf("binrpc: struct member %d name: %w", i, err)
			}
			val, err := readValue(r, depth+1)
			if err != nil {
				return nil, fmt.Errorf("binrpc: struct member %d value: %w", i, err)
			}
			members[i] = xmlrpc.Member{Name: name, Value: val}
		}
		return xmlrpc.StructValue{Members: members}, nil
	default:
		return nil, fmt.Errorf("binrpc: unknown value type tag 0x%X", tag)
	}
}

func readNValues(r *bytesReader, n, depth int) ([]xmlrpc.Value, error) {
	if n < 0 {
		return nil, fmt.Errorf("binrpc: negative value count %d", n)
	}
	// Bound by the minimum wire footprint per value so a crafted count
	// cannot pre-allocate a slice far larger than the payload can fill.
	if n > r.remaining()/minValueWireBytes {
		return nil, fmt.Errorf("binrpc: value count %d exceeds remaining %d bytes", n, r.remaining())
	}
	out := make([]xmlrpc.Value, n)
	for i := range n {
		v, err := readValue(r, depth)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

// readRawString reads `<length:u32><ISO-8859-1 bytes>`.
func readRawString(r *bytesReader) (string, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return "", err
	}
	raw, err := r.readN(int(length))
	if err != nil {
		return "", err
	}
	dec := charmap.ISO8859_1.NewDecoder()
	utf8, err := dec.Bytes(raw)
	if err != nil {
		return "", fmt.Errorf("binrpc: decode ISO-8859-1: %w", err)
	}
	return string(utf8), nil
}

// bytesReader is a minimal in-memory io.Reader we can ask for
// "remaining bytes" without reflection. The stdlib's bytes.Reader would
// work too, but carries an offset tracker we don't need.
type bytesReader struct {
	b   []byte
	off int
}

func (r *bytesReader) Read(p []byte) (int, error) {
	if r.off >= len(r.b) {
		return 0, io.EOF
	}
	n := copy(p, r.b[r.off:])
	r.off += n
	return n, nil
}

func (r *bytesReader) readN(n int) ([]byte, error) {
	if n < 0 {
		return nil, errors.New("binrpc: negative read length")
	}
	// Compare against remaining() rather than r.off+n: on 32-bit builds a
	// large n (from a truncated uint32 length) can make r.off+n overflow
	// negative and slip past the bound into an out-of-range slice panic.
	if n > r.remaining() {
		return nil, fmt.Errorf("binrpc: truncated: need %d bytes, have %d", n, r.remaining())
	}
	out := r.b[r.off : r.off+n]
	r.off += n
	return out, nil
}

func (r *bytesReader) remaining() int { return len(r.b) - r.off }
