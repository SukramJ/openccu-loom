// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// RFC 6455 opcodes and header bits, restated here because this is a
// black-box test of the wire contract — it deliberately does not reach into
// the ws package's unexported framing constants.
const (
	wsOpContinuation byte = 0x0
	wsOpText         byte = 0x1
	wsFinBit         byte = 0x80
	wsOpcodeMask     byte = 0x0F
	wsMaskBit        byte = 0x80
	wsLenMask        byte = 0x7F
)

// dialContractWS performs the RFC 6455 handshake against server and returns
// the raw connection plus a buffered reader positioned at the first frame.
func dialContractWS(t *testing.T, server *httptest.Server) (net.Conn, *bufio.Reader) {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse server url: %v", err)
	}
	conn, err := net.Dial("tcp", parsed.Host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		t.Fatalf("rand: %v", err)
	}
	req := "GET /ws HTTP/1.1\r\n" +
		"Host: " + parsed.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + base64.StdEncoding.EncodeToString(key) + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read handshake response: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("handshake status=%d want %d", resp.StatusCode, http.StatusSwitchingProtocols)
	}
	return conn, br
}

// writeContractFrame emits one masked client frame with an explicit FIN bit.
func writeContractFrame(t *testing.T, w io.Writer, opcode byte, fin bool, payload []byte) {
	t.Helper()
	first := opcode & wsOpcodeMask
	if fin {
		first |= wsFinBit
	}
	if len(payload) >= 126 {
		t.Fatalf("test payload %d bytes exceeds the short-length form", len(payload))
	}
	mask := [4]byte{0x11, 0x22, 0x33, 0x44}
	header := []byte{first, byte(len(payload)) | wsMaskBit, mask[0], mask[1], mask[2], mask[3]} //nolint:gosec // guarded above
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

// readContractFrame reads one server frame, control frames included.
func readContractFrame(t *testing.T, r *bufio.Reader) (opcode byte, payload []byte) {
	t.Helper()
	var header [2]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		t.Fatalf("read header: %v", err)
	}
	opcode = header[0] & wsOpcodeMask
	length := int(header[1] & wsLenMask)
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
		length = int(ext) //nolint:gosec // server frames are bounded by the server's own payload cap
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		t.Fatalf("read payload: %v", err)
	}
	return opcode, payload
}

// TestWebSocketAcceptsFragmentedClientMessages pins RFC 6455 §5.4 on the
// north-bound WebSocket boundary: a client is free to split one logical
// message across a non-final data frame plus continuations, and the server
// must act on the assembled message.
//
// It sits alongside the §5.1 masking rule the server already enforces. The
// two are the same contract seen from both sides — the server may reject a
// client frame that breaks the spec, and it may not reject one that follows
// it. Without this, a client library that fragments above a size threshold
// gets its commands silently discarded and waits forever on the correlation.
func TestWebSocketAcceptsFragmentedClientMessages(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	server := httptest.NewServer(ws.Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	conn, br := dialContractWS(t, server)
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	// A subscribe is the smallest command with an observable answer: the
	// server acks it on the wire.
	body := []byte(`{"op":"subscribe","topics":["system.ccu.status"]}`)
	split := 20
	writeContractFrame(t, conn, wsOpText, false, body[:split])
	writeContractFrame(t, conn, wsOpContinuation, true, body[split:])

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		opcode, payload := readContractFrame(t, br)
		if opcode != wsOpText {
			continue
		}
		var ack struct {
			Op     string   `json:"op"`
			Topics []string `json:"topics"`
		}
		if err := json.Unmarshal(payload, &ack); err != nil {
			continue
		}
		if ack.Op != "subscribed" {
			continue
		}
		if len(ack.Topics) != 1 || ack.Topics[0] != "system.ccu.status" {
			t.Fatalf("ack topics=%v want [system.ccu.status]", ack.Topics)
		}
		return
	}
	t.Fatal("fragmented subscribe was never acknowledged")
}
