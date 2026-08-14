// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// client_fragmentation_test.go pins RFC 6455 §5.4 message assembly on the
// inbound path: a client library that splits a large `call` across several
// frames must be understood exactly like one that sends it in a single
// frame, and every framing violation must be answered with a close status
// instead of a silently dropped frame.

package ws

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"
	"time"
)

// writeClientFrame frames payload as a masked client frame with an explicit
// opcode and FIN bit, so a test can build a fragmented message.
func writeClientFrame(t *testing.T, w io.Writer, opcode byte, fin bool, payload []byte) {
	t.Helper()
	first := opcode & opMask
	if fin {
		first |= finBit
	}
	header := []byte{first}
	mask := [4]byte{0x37, 0x1a, 0x5c, 0x02}
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload))|maskBit) //nolint:gosec // bounded by the branch guard
	case len(payload) <= 0xFFFF:
		header = append(header, 126|maskBit, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(len(payload))) //nolint:gosec // bounded by the branch guard
	default:
		header = append(header, 127|maskBit, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(len(payload)))
	}
	header = append(header, mask[:]...)
	if _, err := w.Write(header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	if _, err := w.Write(masked); err != nil {
		t.Fatalf("write payload: %v", err)
	}
}

// readServerFrame reads exactly one server frame, control frames included —
// readServerText filters those out, and these tests are about them.
func readServerFrame(t *testing.T, r *bufio.Reader) (opcode byte, payload []byte) {
	t.Helper()
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		t.Fatalf("read header: %v", err)
	}
	opcode = header[0] & opMask
	length := int(header[1] & lenMask)
	switch length {
	case 126:
		var ext uint16
		if err := binary.Read(r, binary.BigEndian, &ext); err != nil {
			t.Fatalf("len16: %v", err)
		}
		length = int(ext)
	case 127:
		var ext uint64
		if err := binary.Read(r, binary.BigEndian, &ext); err != nil {
			t.Fatalf("len64: %v", err)
		}
		length = int(ext) //nolint:gosec // server frames are bounded by maxPayload
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return opcode, payload
}

// awaitCloseCode reads frames until the server's close frame arrives and
// returns its status code.
func awaitCloseCode(t *testing.T, r *bufio.Reader) uint16 {
	t.Helper()
	for {
		op, payload := readServerFrame(t, r)
		if op != opClose {
			continue
		}
		if len(payload) < 2 {
			t.Fatalf("close frame without a status code: %q", payload)
		}
		return binary.BigEndian.Uint16(payload[:2])
	}
}

// TestReadPumpAssemblesFragmentedCall proves a `call` split across a
// non-final text frame plus a continuation is dispatched once, with the
// joined payload — before the fix the first fragment failed json.Unmarshal
// and the continuation matched no case, so the caller never saw a result.
func TestReadPumpAssemblesFragmentedCall(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	conn := dialWS(t, server)
	waitForRegistered(t, hub)

	body, err := json.Marshal(map[string]any{
		"op":      "call",
		"id":      "frag-1",
		"command": "system.health",
		"args":    json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	split := len(body) / 2
	writeClientFrame(t, conn.conn, opText, false, body[:split])
	writeClientFrame(t, conn.conn, opContinuation, true, body[split:])

	var res outboundResult
	conn.recv(&res)
	if res.ID != "frag-1" {
		t.Fatalf("result id=%q want frag-1", res.ID)
	}
	if res.Error != nil {
		t.Fatalf("unexpected error: %+v", res.Error)
	}
}

// TestReadPumpAnswersPingBetweenFragments proves an interleaved control
// frame is answered inline without disturbing the open message, as RFC 6455
// §5.4 requires.
func TestReadPumpAnswersPingBetweenFragments(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	conn := dialWS(t, server)
	waitForRegistered(t, hub)

	body := []byte(`{"op":"call","id":"frag-2","command":"system.health","args":{}}`)
	split := 20
	writeClientFrame(t, conn.conn, opText, false, body[:split])
	writeClientFrame(t, conn.conn, opPing, true, []byte("beat"))
	writeClientFrame(t, conn.conn, opContinuation, true, body[split:])

	var sawPong bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		op, payload := readServerFrame(t, conn.br)
		switch op {
		case opPong:
			if !bytes.Equal(payload, []byte("beat")) {
				t.Fatalf("pong payload=%q want %q", payload, "beat")
			}
			sawPong = true
		case opText:
			var res outboundResult
			if err := json.Unmarshal(payload, &res); err != nil || res.Op != "result" {
				continue
			}
			if res.ID != "frag-2" {
				t.Fatalf("result id=%q want frag-2", res.ID)
			}
			if !sawPong {
				t.Fatal("result arrived but the interleaved ping was never answered")
			}
			return
		}
	}
	t.Fatal("assembled call never produced a result")
}

// TestReadPumpRejectsContinuationWithoutOpenMessage proves a stray
// continuation fails the connection with 1002 instead of being dropped.
func TestReadPumpRejectsContinuationWithoutOpenMessage(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	conn := dialWS(t, server)
	waitForRegistered(t, hub)

	writeClientFrame(t, conn.conn, opContinuation, true, []byte(`{"op":"pong"}`))

	if code := awaitCloseCode(t, conn.br); code != closeProtocolError {
		t.Fatalf("close code=%d want %d", code, closeProtocolError)
	}
}

// TestReadPumpRejectsInterleavedDataFrame proves a second non-final data
// frame while a message is still open is a protocol error (RFC 6455 §5.4).
func TestReadPumpRejectsInterleavedDataFrame(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	conn := dialWS(t, server)
	waitForRegistered(t, hub)

	writeClientFrame(t, conn.conn, opText, false, []byte(`{"op":`))
	writeClientFrame(t, conn.conn, opText, true, []byte(`{"op":"pong"}`))

	if code := awaitCloseCode(t, conn.br); code != closeProtocolError {
		t.Fatalf("close code=%d want %d", code, closeProtocolError)
	}
}

// TestReadPumpBoundsFragmentAccumulation proves fragmentation cannot be used
// to walk past the per-frame payload cap: the assembled message is bounded by
// the same maxPayload and an overrun closes with 1009 rather than allocating.
func TestReadPumpBoundsFragmentAccumulation(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	conn := dialWS(t, server)
	waitForRegistered(t, hub)

	chunk := bytes.Repeat([]byte("A"), 600*1024)
	writeClientFrame(t, conn.conn, opText, false, chunk)
	writeClientFrame(t, conn.conn, opContinuation, true, chunk)

	if code := awaitCloseCode(t, conn.br); code != closeMessageTooBig {
		t.Fatalf("close code=%d want %d", code, closeMessageTooBig)
	}
}

// TestReadPumpRejectsBinaryFrames proves the text-JSON-only wire contract is
// stated on the wire: a binary frame is answered with 1003 rather than being
// dropped by a missing switch case.
func TestReadPumpRejectsBinaryFrames(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	conn := dialWS(t, server)
	waitForRegistered(t, hub)

	writeClientFrame(t, conn.conn, opBinary, true, []byte(`{"op":"pong"}`))

	if code := awaitCloseCode(t, conn.br); code != closeUnsupportedData {
		t.Fatalf("close code=%d want %d", code, closeUnsupportedData)
	}
}
