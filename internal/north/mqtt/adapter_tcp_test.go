// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/mqtt/protocol"
)

// mockBroker is a minimal in-process MQTT broker for tests. It
// accepts exactly one connection, answers CONNECT with CONNACK,
// PUBLISH QoS 1 with PUBACK, PINGREQ with PINGRESP, and records
// every received publish for assertions.
type mockBroker struct {
	t        *testing.T
	listener net.Listener
	addr     string

	mu        sync.Mutex
	published []*protocol.InboundPublish
	subs      []string
}

func newMockBroker(t *testing.T) *mockBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	b := &mockBroker{t: t, listener: ln, addr: ln.Addr().String()}
	go b.accept()
	t.Cleanup(func() { _ = ln.Close() })
	return b
}

func (b *mockBroker) URL() string { return "tcp://" + b.addr }

func (b *mockBroker) accept() {
	conn, err := b.listener.Accept()
	if err != nil {
		return
	}
	go b.serve(conn)
}

func (b *mockBroker) serve(conn net.Conn) {
	defer conn.Close()
	bw := bufio.NewWriter(conn)
	br := bufio.NewReader(conn)
	for {
		frame, err := protocol.ReadFrame(br)
		if err != nil {
			return
		}
		switch frame.PacketType() { //nolint:exhaustive // only inbound frames the mock sends a reply for
		case protocol.PacketConnect:
			_ = writePacket(bw, byte(protocol.PacketConnack)<<4, []byte{0, 0})
			_ = bw.Flush()
		case protocol.PacketPublish:
			ib, err := protocol.DecodePublish(frame.Header, frame.Body)
			if err != nil {
				return
			}
			b.mu.Lock()
			b.published = append(b.published, ib)
			b.mu.Unlock()
			if ib.QoS == 1 {
				_ = protocol.EncodePuback(bw, ib.PacketID)
				_ = bw.Flush()
			}
		case protocol.PacketSubscribe:
			topic, _, _ := readString(frame.Body[2:])
			b.mu.Lock()
			b.subs = append(b.subs, topic)
			b.mu.Unlock()
			// SUBACK (packet id + one status byte)
			body := []byte{frame.Body[0], frame.Body[1], 0x01}
			_ = writePacket(bw, byte(protocol.PacketSuback)<<4, body)
			_ = bw.Flush()
		case protocol.PacketPingreq:
			_ = writePacket(bw, byte(protocol.PacketPingresp)<<4, nil)
			_ = bw.Flush()
		case protocol.PacketDisconnect:
			return
		}
	}
}

func (b *mockBroker) snapshot() []*protocol.InboundPublish {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]*protocol.InboundPublish, len(b.published))
	copy(out, b.published)
	return out
}

// --- helpers (local copies — the broker is testing-only) ---

func writePacket(w *bufio.Writer, header byte, body []byte) error {
	if _, err := w.Write([]byte{header}); err != nil {
		return err
	}
	if _, err := w.Write(encodeLength(len(body))); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	return w.Flush()
}

func encodeLength(n int) []byte {
	var out []byte
	for {
		digit := byte(n & 0x7F)
		n >>= 7
		if n > 0 {
			digit |= 0x80
		}
		out = append(out, digit)
		if n == 0 {
			break
		}
	}
	return out
}

func readString(b []byte) (value string, bytesRead int, err error) {
	if len(b) < 2 {
		return "", 0, nil
	}
	n := int(b[0])<<8 | int(b[1])
	return string(b[2 : 2+n]), 2 + n, nil
}

// --- tests ---

func TestTCPClientConnectAndPublishQoS0(t *testing.T) {
	b := newMockBroker(t)
	c := NewTCPClient(TCPConfig{BrokerURL: b.URL(), ClientID: "gotest", KeepAlive: 30 * time.Second})
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Disconnect(ctx) //nolint:errcheck // teardown

	if err := c.Publish(ctx, "openccu-loom/test", []byte("hello"), QoS0, true); err != nil {
		t.Fatalf("publish: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if snap := b.snapshot(); len(snap) == 1 && snap[0].Topic == "openccu-loom/test" && string(snap[0].Payload) == "hello" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("broker never saw publish: %+v", b.snapshot())
}

func TestTCPClientPublishQoS1WaitsForPuback(t *testing.T) {
	b := newMockBroker(t)
	c := NewTCPClient(TCPConfig{BrokerURL: b.URL(), ClientID: "gotest", KeepAlive: 30 * time.Second})
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Disconnect(ctx) //nolint:errcheck // teardown

	if err := c.Publish(ctx, "openccu-loom/ack", []byte("x"), QoS1, false); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func TestTCPClientSubscribeSendsFrame(t *testing.T) {
	b := newMockBroker(t)
	c := NewTCPClient(TCPConfig{BrokerURL: b.URL(), ClientID: "gotest", KeepAlive: 30 * time.Second})
	ctx := context.Background()
	if err := c.Connect(ctx); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer c.Disconnect(ctx) //nolint:errcheck // teardown

	var received []string
	if err := c.Subscribe(ctx, "cmd/#", QoS1, func(topic string, _ []byte, _ bool) {
		received = append(received, topic)
	}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		b.mu.Lock()
		has := strings.Contains(strings.Join(b.subs, "|"), "cmd/#")
		b.mu.Unlock()
		if has {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("broker never saw subscribe")
}

func TestTopicMatches(t *testing.T) {
	cases := []struct {
		filter, topic string
		want          bool
	}{
		{"a/b", "a/b", true},
		{"a/+", "a/b", true},
		{"a/+", "a/b/c", false},
		{"a/#", "a/b/c", true},
		{"#", "anything/goes", true},
		{"a/+/c", "a/b/c", true},
	}
	for _, c := range cases {
		if got := topicMatches(c.filter, c.topic); got != c.want {
			t.Fatalf("match(%q,%q)=%v want %v", c.filter, c.topic, got, c.want)
		}
	}
}

func TestTCPClientConnectRejectsBadURL(t *testing.T) {
	c := NewTCPClient(TCPConfig{BrokerURL: "::::not a url"})
	if err := c.Connect(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}
