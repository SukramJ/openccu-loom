// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package harness

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // required by RFC 6455 handshake
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// WSClient is a minimal RFC 6455 WebSocket client tailored for the
// E2E test suite. It speaks the openccu-loom command envelope used by
// /api/v1/events:
//
//	→ {"op":"call",   "id":"<uuid>", "command":"<name>", "args":{...}}
//	← {"op":"result", "id":"<uuid>", "data":{...}}
//	← {"op":"result", "id":"<uuid>", "error":{"code":"...","message":"..."}}
//
// Hand-rolled to avoid pulling in a third-party WS dep — the server
// in internal/north/rest/ws is hand-rolled too and the wire format
// is small. If the test surface grows beyond what this client
// supports, switch to a real client (with an ADR).
type WSClient struct {
	conn   net.Conn
	br     *bufio.Reader
	bw     *bufio.Writer
	mu     sync.Mutex // serialises writes (frame masking is per-write)
	closed chan struct{}
}

// Dial opens a WebSocket against the daemon's /api/v1/events endpoint
// using the cookies stored in `rest`'s jar (so a prior LoginSession
// authorises the upgrade). Caller owns the lifetime; close via Close.
func (c *RESTClient) DialWS(path string) (*WSClient, error) {
	if path == "" {
		path = "/api/v1/events"
	}
	wsURL := WSURL(c.base) // ws://host:port/api/ws — but we override
	u, err := url.Parse(c.base + path)
	if err != nil {
		return nil, fmt.Errorf("parse ws url: %w", err)
	}
	_ = wsURL

	// 1. Open TCP.
	conn, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	// 2. Build handshake request.
	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("rand: %w", err)
	}
	clientKey := base64.StdEncoding.EncodeToString(keyBytes)

	// Cookies for session auth.
	var cookieHeader string
	if c.hc.Jar != nil {
		jarCookies := c.hc.Jar.Cookies(u)
		if len(jarCookies) > 0 {
			parts := make([]string, 0, len(jarCookies))
			for _, ck := range jarCookies {
				parts = append(parts, ck.Name+"="+ck.Value)
			}
			cookieHeader = "Cookie: " + strings.Join(parts, "; ") + "\r\n"
		}
	}
	req := "GET " + u.Path + " HTTP/1.1\r\n" +
		"Host: " + u.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + clientKey + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		cookieHeader +
		"\r\n"

	if _, err := conn.Write([]byte(req)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("write handshake: %w", err)
	}

	// 3. Read handshake response.
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("read handshake: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, fmt.Errorf("ws upgrade failed: status=%d", resp.StatusCode)
	}
	wantAccept := acceptKey(clientKey)
	if got := resp.Header.Get("Sec-WebSocket-Accept"); got != wantAccept {
		_ = conn.Close()
		return nil, fmt.Errorf("ws upgrade: bad accept token: %s", got)
	}

	w := &WSClient{
		conn:   conn,
		br:     br,
		bw:     bufio.NewWriter(conn),
		closed: make(chan struct{}),
	}
	return w, nil
}

// acceptKey computes the Sec-WebSocket-Accept value for clientKey,
// per RFC 6455 §4.2.2. Mirrors the server-side helper in
// internal/north/rest/ws/handler.go.
func acceptKey(clientKey string) string {
	const magic = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	h := sha1.New() //nolint:gosec // required by RFC 6455
	_, _ = h.Write([]byte(clientKey))
	_, _ = h.Write([]byte(magic))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Close tears the connection down. Idempotent.
func (w *WSClient) Close() error {
	if w == nil || w.conn == nil {
		return nil
	}
	select {
	case <-w.closed:
		return nil
	default:
		close(w.closed)
	}
	return w.conn.Close()
}

// CallResult is the parsed `{op:"result", id, data, error}` payload.
type CallResult struct {
	ID    string          `json:"id"`
	Op    string          `json:"op"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Call sends one `call` frame with the given command + args and reads
// frames from the server until a `result` envelope with the matching
// id arrives or `timeout` expires. Out-of-band frames (server-pushed
// events, pings) are dropped — the walker is not interested in them.
func (w *WSClient) Call(id, command string, args any, timeout time.Duration) (*CallResult, error) {
	if args == nil {
		args = map[string]any{}
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("encode args: %w", err)
	}
	frameJSON, err := json.Marshal(struct {
		Op      string          `json:"op"`
		ID      string          `json:"id"`
		Command string          `json:"command"`
		Args    json.RawMessage `json:"args"`
	}{Op: "call", ID: id, Command: command, Args: argsJSON})
	if err != nil {
		return nil, fmt.Errorf("encode frame: %w", err)
	}

	w.mu.Lock()
	if err := writeMaskedTextFrame(w.bw, frameJSON); err != nil {
		w.mu.Unlock()
		return nil, fmt.Errorf("write call: %w", err)
	}
	w.mu.Unlock()

	deadline := time.Now().Add(timeout)
	for {
		if err := w.conn.SetReadDeadline(deadline); err != nil {
			return nil, fmt.Errorf("set deadline: %w", err)
		}
		op, payload, err := readServerFrame(w.br)
		if err != nil {
			return nil, fmt.Errorf("read frame: %w", err)
		}
		switch op {
		case 0x9: // ping — server doesn't send, but be safe
			_ = w.writePong(payload)
			continue
		case 0xA: // pong
			continue
		case 0x8: // close
			return nil, errors.New("server closed connection")
		case 0x1: // text
			// Filter to result envelopes for our id.
			var head struct {
				Op string `json:"op"`
				ID string `json:"id"`
			}
			if err := json.Unmarshal(payload, &head); err != nil {
				continue
			}
			if head.Op != "result" || head.ID != id {
				continue
			}
			var res CallResult
			if err := json.Unmarshal(payload, &res); err != nil {
				return nil, fmt.Errorf("decode result: %w", err)
			}
			return &res, nil
		}
	}
}

// writePong responds to a server ping frame with the same payload.
func (w *WSClient) writePong(payload []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return writeMaskedFrame(w.bw, 0xA, payload)
}

// writeMaskedTextFrame is a convenience wrapper for opcode 0x1.
func writeMaskedTextFrame(bw *bufio.Writer, payload []byte) error {
	return writeMaskedFrame(bw, 0x1, payload)
}

// writeMaskedFrame writes one masked client→server frame per RFC 6455.
// Clients MUST mask their frames; servers MUST NOT.
func writeMaskedFrame(bw *bufio.Writer, opcode byte, payload []byte) error {
	const finBit byte = 0x80
	header := []byte{finBit | (opcode & 0x0F)}
	switch {
	case len(payload) < 126:
		header = append(header, 0x80|byte(len(payload)))
	case len(payload) <= 0xFFFF:
		header = append(header, 0x80|126, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(len(payload)))
	default:
		header = append(header, 0x80|127, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(len(payload)))
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	header = append(header, mask[:]...)
	if _, err := bw.Write(header); err != nil {
		return err
	}
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	if _, err := bw.Write(masked); err != nil {
		return err
	}
	return bw.Flush()
}

// readServerFrame reads one unmasked server→client frame.
func readServerFrame(br *bufio.Reader) (opcode byte, payload []byte, err error) {
	const (
		finBit  byte = 0x80
		rsvBit  byte = 0x70
		opMask  byte = 0x0F
		maskBit byte = 0x80
		lenMask byte = 0x7F
	)
	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil {
		return 0, nil, err
	}
	if header[0]&rsvBit != 0 {
		return 0, nil, errors.New("ws: reserved bits must be zero")
	}
	_ = finBit
	opcode = header[0] & opMask
	masked := header[1]&maskBit != 0
	length := int64(header[1] & lenMask)
	switch length {
	case 126:
		var ext uint16
		if err := binary.Read(br, binary.BigEndian, &ext); err != nil {
			return 0, nil, err
		}
		length = int64(ext)
	case 127:
		var ext uint64
		if err := binary.Read(br, binary.BigEndian, &ext); err != nil {
			return 0, nil, err
		}
		length = int64(ext) //nolint:gosec // bounded by maxPayload below
	}
	const maxPayload = 1 << 20
	if length < 0 || length > maxPayload {
		return 0, nil, errors.New("ws: frame too large")
	}
	if masked {
		// RFC: server frames must NOT be masked, but tolerate.
		var mask [4]byte
		if _, err := io.ReadFull(br, mask[:]); err != nil {
			return 0, nil, err
		}
		payload = make([]byte, length)
		if _, err := io.ReadFull(br, payload); err != nil {
			return 0, nil, err
		}
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
		return opcode, payload, nil
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(br, payload); err != nil {
		return 0, nil, err
	}
	return opcode, payload, nil
}
