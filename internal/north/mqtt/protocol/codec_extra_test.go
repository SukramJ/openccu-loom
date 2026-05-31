// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package protocol

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// ---------------------------------------------------------------------------
// UnsubscribePacket.Encode (currently 0 %)
// ---------------------------------------------------------------------------

func TestUnsubscribeEncode(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	p := &UnsubscribePacket{PacketID: 5, TopicFilter: "h/gone"}
	if err := p.Encode(&buf); err != nil {
		t.Fatalf("encode: %v", err)
	}
	b := buf.Bytes()
	if len(b) == 0 {
		t.Fatal("empty output")
	}
	// Fixed header high nibble must be PacketUnsubscribe (10 = 0xA).
	if PacketType(b[0]>>4) != PacketUnsubscribe {
		t.Errorf("packet type = %d, want %d (Unsubscribe)", b[0]>>4, PacketUnsubscribe)
	}
}

// ---------------------------------------------------------------------------
// DecodeConnack — short body error path (currently 66.7 %)
// ---------------------------------------------------------------------------

func TestDecodeConnackShortBody(t *testing.T) {
	t.Parallel()
	_, err := DecodeConnack([]byte{0x00}) // only 1 byte, need 2
	if err == nil {
		t.Fatal("expected error for short connack body")
	}
}

// ---------------------------------------------------------------------------
// DecodePuback — short body error path (currently 66.7 %)
// ---------------------------------------------------------------------------

func TestDecodePubackShortBody(t *testing.T) {
	t.Parallel()
	_, err := DecodePuback([]byte{0x12}) // only 1 byte, need 2
	if err == nil {
		t.Fatal("expected error for short puback body")
	}
}

// ---------------------------------------------------------------------------
// DecodePublish — missing packet-id error (currently 85.7 %)
// ---------------------------------------------------------------------------

func TestDecodePublishMissingPacketID(t *testing.T) {
	t.Parallel()
	// Build a body that has a valid topic string but no packet-id bytes.
	// header QoS=1 → parser expects 2-byte packet-id after topic.
	var body bytes.Buffer
	writeString(&body, "a/b")
	// Deliberately do not append the 2-byte packet-id.
	header := byte(PacketPublish)<<4 | (1 << 1) // QoS=1
	_, err := DecodePublish(header, body.Bytes())
	if err == nil {
		t.Fatal("expected error when packet-id bytes are missing")
	}
}

// ---------------------------------------------------------------------------
// ReadFrame — error when body read fails (currently 72.7 %)
// ---------------------------------------------------------------------------

type errReader struct {
	header []byte
	read   int
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.read < len(r.header) {
		n := copy(p, r.header[r.read:])
		r.read += n
		return n, nil
	}
	return 0, errors.New("body read error")
}

func TestReadFrameBodyReadError(t *testing.T) {
	t.Parallel()
	// Craft a reader that supplies a valid fixed header + remaining-length
	// of 5, but then errors out when the body is requested.
	// fixed header = 0x30 (PUBLISH), remaining length = 5 → two bytes: 0x30 0x05
	r := &errReader{header: []byte{0x30, 0x05}}
	_, err := ReadFrame(r)
	if err == nil {
		t.Fatal("expected error when body read fails")
	}
}

// ---------------------------------------------------------------------------
// ReadFrame — EOF on header byte (currently 72.7 %)
// ---------------------------------------------------------------------------

func TestReadFrameEOF(t *testing.T) {
	t.Parallel()
	_, err := ReadFrame(bytes.NewReader(nil))
	if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected EOF-like error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// readRemainingLength — malformed (5 continuation bytes → error)
// ---------------------------------------------------------------------------

func TestReadRemainingLengthMalformed(t *testing.T) {
	t.Parallel()
	// 4 bytes all with MSB=1 → no termination after 4 iterations → error.
	malformed := bytes.NewReader([]byte{0x80, 0x80, 0x80, 0x80})
	_, err := readRemainingLength(malformed)
	if err == nil {
		t.Fatal("expected error for malformed remaining length")
	}
}

// ---------------------------------------------------------------------------
// readString — short header / short body error paths (currently 66.7 %)
// ---------------------------------------------------------------------------

func TestReadStringShortHeader(t *testing.T) {
	t.Parallel()
	_, _, err := readString([]byte{0x00}) // only 1 byte, need at least 2
	if err == nil {
		t.Fatal("expected error for short string header")
	}
}

func TestReadStringShortBody(t *testing.T) {
	t.Parallel()
	// length=4, but only 1 byte of body follows.
	_, _, err := readString([]byte{0x00, 0x04, 'a'})
	if err == nil {
		t.Fatal("expected error for short string body")
	}
}

// ---------------------------------------------------------------------------
// writePacket — writer error propagation (currently 77.8 %)
// ---------------------------------------------------------------------------

type failWriter struct {
	failAfter int
	written   int
}

func (w *failWriter) Write(p []byte) (int, error) {
	if w.written >= w.failAfter {
		return 0, errors.New("write error")
	}
	n := len(p)
	if w.written+n > w.failAfter {
		n = w.failAfter - w.written
	}
	w.written += n
	return n, nil
}

func TestWritePacketHeaderWriteError(t *testing.T) {
	t.Parallel()
	// Fail immediately on first byte.
	w := &failWriter{failAfter: 0}
	err := writePacket(w, byte(PacketPingreq)<<4, nil)
	if err == nil {
		t.Fatal("expected error when header write fails")
	}
}

func TestWritePacketLengthWriteError(t *testing.T) {
	t.Parallel()
	// Allow the 1-byte header to go through, fail on the length byte.
	w := &failWriter{failAfter: 1}
	err := writePacket(w, byte(PacketPingreq)<<4, nil)
	if err == nil {
		t.Fatal("expected error when remaining-length write fails")
	}
}
