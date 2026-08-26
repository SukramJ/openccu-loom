// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
)

// RFC 6455 opcode values.
const (
	opContinuation byte = 0x0
	opText         byte = 0x1
	opBinary       byte = 0x2
	opClose        byte = 0x8
	opPing         byte = 0x9
	opPong         byte = 0xA
)

const (
	finBit byte = 0x80
	rsvBit byte = 0x70
	opMask byte = 0x0F
	// ctrlBit distinguishes control opcodes (0x8-0xF) from data opcodes
	// (0x0-0x7) — RFC 6455 §5.5.
	ctrlBit byte = 0x8

	maskBit byte = 0x80
	lenMask byte = 0x7F
)

// maxPayload is the largest payload the server accepts, both per frame and
// per assembled message: a client that fragments a large `call` must not be
// able to walk past the cap one frame at a time. It bounds the allocation a
// single connection can drive.
const maxPayload = 1 << 20 // 1 MiB

// maxControlPayload is the RFC 6455 §5.5 ceiling for a control frame.
const maxControlPayload = 125

// Close status codes the server emits when it fails a connection
// (RFC 6455 §7.4.1). Without them a client sees a bare TCP teardown and
// cannot tell a protocol violation from a network drop.
const (
	closeProtocolError   uint16 = 1002
	closeUnsupportedData uint16 = 1003
	closeMessageTooBig   uint16 = 1009
)

// frame is one decoded WebSocket frame.
type frame struct {
	opcode  byte
	fin     bool
	payload []byte
}

// readFrame parses one frame from r. Returns io.EOF at the stream
// end and an error for malformed framing.
func readFrame(r *bufio.Reader) (frame, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return frame{}, err
	}
	if header[0]&rsvBit != 0 {
		return frame{}, errors.New("ws: reserved bits must be zero")
	}
	fin := header[0]&finBit != 0
	opcode := header[0] & opMask
	masked := header[1]&maskBit != 0
	length := int64(header[1] & lenMask)

	if opcode&ctrlBit != 0 {
		// RFC 6455 §5.5: control frames MUST NOT be fragmented and carry at
		// most 125 payload bytes. Accepting a non-final control frame would
		// let a ping masquerade as the start of a message and corrupt the
		// reassembly state readPump keeps across frames.
		if !fin {
			return frame{}, errors.New("ws: control frame must not be fragmented")
		}
		if length > maxControlPayload {
			return frame{}, errors.New("ws: control frame payload too large")
		}
	}

	switch length {
	case 126:
		var ext uint16
		if err := binary.Read(r, binary.BigEndian, &ext); err != nil {
			return frame{}, err
		}
		length = int64(ext)
	case 127:
		var ext uint64
		if err := binary.Read(r, binary.BigEndian, &ext); err != nil {
			return frame{}, err
		}
		if ext > uint64(maxPayload) {
			return frame{}, errors.New("ws: frame too large")
		}
		length = int64(ext) //nolint:gosec // bounded above by maxPayload; see #20
	}
	if length < 0 || length > maxPayload {
		return frame{}, errors.New("ws: frame too large")
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(r, mask[:]); err != nil {
			return frame{}, err
		}
	} else {
		// RFC 6455 §5.1: a server MUST close the connection upon
		// receiving an unmasked frame from a client.
		return frame{}, errors.New("ws: client frame must be masked")
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return frame{}, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return frame{opcode: opcode, fin: fin, payload: payload}, nil
}

// writeFrame emits a single server→client frame. Server frames are
// never masked.
func writeFrame(w *bufio.Writer, opcode byte, payload []byte) error {
	header := []byte{finBit | (opcode & opMask)}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload))) //nolint:gosec // case-guard caps len at 125, fits in a byte; see #20
	case len(payload) <= 0xFFFF:
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(len(payload))) //nolint:gosec // length already bounded; see #20
	default:
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(len(payload))) //nolint:gosec // length non-negative; see #20
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}
