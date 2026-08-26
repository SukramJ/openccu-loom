// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // handshake parity with RFC 6455
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// --- framing unit tests ---

func TestMatchTopic(t *testing.T) {
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		{"*", "foo", true},
		{"device.*", "device.0001", true},
		{"device.*", "device.0001.channel.1", true},
		{"device.*", "device", true},
		{"device.*", "hub.info", false},
		{"device.0001", "device.0001", true},
		{"device.0001", "device.0002", false},
	}
	for _, c := range cases {
		if got := matchTopic(c.pattern, c.topic); got != c.want {
			t.Fatalf("match(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}

func TestAcceptKey(t *testing.T) {
	// Sample from RFC 6455 §1.3.
	got := acceptKey("dGhlIHNhbXBsZSBub25jZQ==")
	if got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatalf("accept=%q", got)
	}
}

// --- end-to-end handshake + publish test ---

func TestHandlerEndToEnd(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(Handler(hub, nil, nil))
	defer server.Close()

	wsURL, _ := url.Parse(server.URL)
	conn, err := net.Dial("tcp", wsURL.Host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	key := genWSKey(t)
	req := "GET /events HTTP/1.1\r\n" +
		"Host: " + wsURL.Host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n\r\n"
	if _, err := conn.Write([]byte(req)); err != nil {
		t.Fatalf("write: %v", err)
	}

	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatalf("read handshake: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	expectedAccept := acceptKey(key)
	if resp.Header.Get("Sec-WebSocket-Accept") != expectedAccept {
		t.Fatalf("accept=%s want %s", resp.Header.Get("Sec-WebSocket-Accept"), expectedAccept)
	}

	// Subscribe to "device.*".
	writeClientText(t, conn, `{"op":"subscribe","topics":["device.*"]}`)

	// Wait for the subscribe message to be processed on the server.
	waitForMatch(t, hub, "device.0001")

	// Publish event on matching topic.
	hub.Publish(Event{
		Topic:   "device.0001",
		Type:    "DataPointValueChanged",
		When:    time.Now(),
		Payload: map[string]any{"value": true},
	})
	// Publish event on non-matching topic (should not arrive).
	hub.Publish(Event{Topic: "hub.info", Type: "Info", When: time.Now()})

	// First frame is the subscribe ACK; the matching event follows.
	got := readEvent(t, br)
	if got.Topic != "device.0001" || got.Type != "DataPointValueChanged" {
		t.Fatalf("event=%+v", got)
	}
}

// readEvent reads server text frames, discarding any control envelopes
// (subscribe ACKs, replay markers) until an actual outboundEvent
// arrives. Tests use this when they care about the next broadcast.
func readEvent(t *testing.T, r *bufio.Reader) outboundEvent {
	t.Helper()
	for {
		pay := readServerText(t, r)
		var probe struct {
			Op string `json:"op"`
		}
		if json.Unmarshal(pay, &probe) == nil {
			switch probe.Op {
			// "error" is a protocol-level notification (see the matching
			// comment in live_test.go's recv) uncorrelated to any
			// broadcast event, so it is skipped here too.
			case "subscribed", "unsubscribed", "replay_done", "replay_lost", "ping", "error":
				continue
			}
		}
		var ev outboundEvent
		if err := json.Unmarshal(pay, &ev); err != nil {
			t.Fatalf("decode: %v, raw=%s", err, string(pay))
		}
		if ev.Type != "" || ev.Topic != "" {
			return ev
		}
	}
}

// genWSKey mimics the client-side Sec-WebSocket-Key generator.
func genWSKey(t *testing.T) string {
	t.Helper()
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// writeClientText frames payload as a masked text frame.
func writeClientText(t *testing.T, w io.Writer, s string) {
	t.Helper()
	payload := []byte(s)
	header := []byte{0x81} // FIN + text opcode
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	switch {
	case len(payload) < 126:
		header = append(header, byte(len(payload))|0x80) //nolint:gosec // G115: len(payload) < 126 per branch guard, fits uint8
	case len(payload) <= 0xFFFF:
		header = append(header, 126|0x80, 0, 0)
		binary.BigEndian.PutUint16(header[len(header)-2:], uint16(len(payload))) //nolint:gosec // bounded
	default:
		header = append(header, 127|0x80, 0, 0, 0, 0, 0, 0, 0, 0)
		binary.BigEndian.PutUint64(header[len(header)-8:], uint64(len(payload))) //nolint:gosec // bounded
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

func readServerText(t *testing.T, r *bufio.Reader) []byte {
	t.Helper()
	for {
		var header [2]byte
		if _, err := io.ReadFull(r, header[:]); err != nil {
			t.Fatalf("header: %v", err)
		}
		opcode := header[0] & 0x0F
		length := int(header[1] & 0x7F)
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
			length = int(ext) //nolint:gosec // length bounded by protocol
		}
		buf := make([]byte, length)
		if _, err := io.ReadFull(r, buf); err != nil {
			t.Fatalf("payload: %v", err)
		}
		if opcode == 0x1 && !strings.Contains(string(buf), `"op":"ping"`) {
			return buf
		}
	}
}

func waitForMatch(t *testing.T, hub *Hub, topic string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.MatchCount(topic) >= 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no subscriber for topic %q", topic)
}

func TestAcceptKeyParity(t *testing.T) {
	h := sha1.New() //nolint:gosec // RFC 6455 handshake uses SHA-1 by design
	h.Write([]byte("test" + handshakeMagic))
	want := base64.StdEncoding.EncodeToString(h.Sum(nil))
	if got := acceptKey("test"); got != want {
		t.Fatalf("got=%s want=%s", got, want)
	}
}
