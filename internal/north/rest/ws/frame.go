// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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

	maskBit byte = 0x80
	lenMask byte = 0x7F
)

// maxPayload is the largest frame the server reads in one shot.
// Clients only ever send small control frames (subscribe / pong), so
// a tight cap keeps us safe from runaway allocations.
const maxPayload = 1 << 20 // 1 MiB

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
		length = int64(ext) //nolint:gosec // bounded above by maxPayload
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
		header = append(header, byte(len(payload))) //nolint:gosec // case-guard caps len at 125, fits in a byte
	case len(payload) <= 0xFFFF:
		header = append(header, 126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(len(payload))) //nolint:gosec // length already bounded
	default:
		header = append(header, 127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(len(payload))) //nolint:gosec // length non-negative
	}
	if _, err := w.Write(header); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return w.Flush()
}
