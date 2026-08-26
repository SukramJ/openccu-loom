// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"testing"
)

// buildMaskedFrame builds a well-formed RFC-6455 client frame with mask.
func buildMaskedFrame(t *testing.T, opcode byte, payload []byte) []byte {
	t.Helper()
	mask := [4]byte{0x01, 0x02, 0x03, 0x04}

	var buf bytes.Buffer
	buf.WriteByte(finBit | (opcode & opMask))
	switch {
	case len(payload) < 126:
		buf.WriteByte(byte(len(payload)) | maskBit) //nolint:gosec // bounded
	case len(payload) <= 0xFFFF:
		buf.WriteByte(126 | maskBit)
		ext := make([]byte, 2)
		binary.BigEndian.PutUint16(ext, uint16(len(payload))) //nolint:gosec // bounded
		buf.Write(ext)
	default:
		buf.WriteByte(127 | maskBit)
		ext := make([]byte, 8)
		binary.BigEndian.PutUint64(ext, uint64(len(payload))) //nolint:gosec // bounded
		buf.Write(ext)
	}
	buf.Write(mask[:])
	for i, b := range payload {
		buf.WriteByte(b ^ mask[i%4])
	}
	return buf.Bytes()
}

func TestReadFrameSmallPayload(t *testing.T) {
	payload := []byte(`{"op":"pong"}`)
	raw := buildMaskedFrame(t, opText, payload)
	r := bufio.NewReader(bytes.NewReader(raw))
	f, err := readFrame(r)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if f.opcode != opText {
		t.Fatalf("opcode=%d want %d", f.opcode, opText)
	}
	if !f.fin {
		t.Fatal("expected fin=true")
	}
	if !bytes.Equal(f.payload, payload) {
		t.Fatalf("payload=%q want %q", f.payload, payload)
	}
}

func TestReadFrameMediumPayload(t *testing.T) {
	// 200-byte payload → triggers the 126 extended-length branch.
	payload := bytes.Repeat([]byte("X"), 200)
	raw := buildMaskedFrame(t, opBinary, payload)
	r := bufio.NewReader(bytes.NewReader(raw))
	f, err := readFrame(r)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if len(f.payload) != 200 {
		t.Fatalf("payload len=%d want 200", len(f.payload))
	}
}

func TestReadFrameUnmaskedClientFrameErrors(t *testing.T) {
	// Unmasked frame — server must reject.
	var buf bytes.Buffer
	buf.WriteByte(finBit | opText)
	buf.WriteByte(5) // len=5, no mask bit
	buf.Write([]byte("hello"))
	r := bufio.NewReader(&buf)
	_, err := readFrame(r)
	if err == nil {
		t.Fatal("expected error for unmasked client frame")
	}
}

func TestReadFrameReservedBitsError(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteByte(finBit | rsvBit | opText) // rsvBit set → error
	buf.WriteByte(5 | maskBit)
	buf.Write([]byte{0, 0, 0, 0}) // mask
	buf.Write([]byte("hello"))
	r := bufio.NewReader(&buf)
	_, err := readFrame(r)
	if err == nil {
		t.Fatal("expected error when reserved bits are set")
	}
}

func TestWriteFrameSmallPayload(t *testing.T) {
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	payload := []byte("hello")
	if err := writeFrame(bw, opText, payload); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	// Verify first two bytes: FIN+opText, len=5 (no mask).
	data := buf.Bytes()
	if data[0] != (finBit | opText) {
		t.Fatalf("byte0=%02x want %02x", data[0], finBit|opText)
	}
	if data[1] != 5 {
		t.Fatalf("byte1=%d want 5", data[1])
	}
	if string(data[2:]) != "hello" {
		t.Fatalf("payload=%q want 'hello'", data[2:])
	}
}

func TestWriteFrameMediumPayload(t *testing.T) {
	payload := bytes.Repeat([]byte("X"), 200)
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	if err := writeFrame(bw, opBinary, payload); err != nil {
		t.Fatalf("writeFrame: %v", err)
	}
	data := buf.Bytes()
	// Byte 1 should be 126 (extended 16-bit length).
	if data[1] != 126 {
		t.Fatalf("byte1=%d want 126 for 200-byte payload", data[1])
	}
	length := binary.BigEndian.Uint16(data[2:4])
	if length != 200 {
		t.Fatalf("length=%d want 200", length)
	}
}

func TestWriteFrameLargePayload(t *testing.T) {
	// 70000 bytes → triggers the 127 extended 64-bit length branch.
	payload := bytes.Repeat([]byte("Y"), 70000)
	var buf bytes.Buffer
	bw := bufio.NewWriter(&buf)
	if err := writeFrame(bw, opBinary, payload); err != nil {
		t.Fatalf("writeFrame large: %v", err)
	}
	data := buf.Bytes()
	if data[1] != 127 {
		t.Fatalf("byte1=%d want 127 for large payload", data[1])
	}
	length := binary.BigEndian.Uint64(data[2:10])
	if length != 70000 {
		t.Fatalf("length=%d want 70000", length)
	}
}
