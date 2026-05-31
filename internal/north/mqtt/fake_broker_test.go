// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/mqtt/protocol"
)

// fakeBroker is a minimal MQTT 3.1.1 broker that runs in-process on a
// loopback socket. It exists to drive [TCPClient] through its full
// lifecycle (CONNECT/CONNACK, SUBSCRIBE/SUBACK, PUBLISH/PUBACK,
// PINGREQ/PINGRESP, UNSUBSCRIBE/UNSUBACK, DISCONNECT) without
// requiring a real broker or docker.
//
// Wire shape mirrors the subset the client emits. Anything outside
// that subset is rejected with a test fatal so a regression in the
// client surfaces as an explicit "I sent X, broker doesn't know X".
//
// One broker accepts one connection. After Disconnect the broker
// drops the socket and refuses further dials; tests that want to
// re-test reconnect spin up a second broker.
type fakeBroker struct {
	t      *testing.T
	ln     net.Listener
	closed atomic.Bool

	connackReturnCode byte // set before Start to force a non-zero CONNACK

	// Captured frames (post-flush) the test can assert on.
	mu           sync.Mutex
	connect      *protocol.ConnectPacket
	subscribes   []string
	unsubscribes []string
	pingreqs     int
	publishes    []capturedPublish
	disconnect   bool

	conn net.Conn
	wg   sync.WaitGroup

	// publishCh fans inbound CLIENT publishes to the test so it can
	// e.g. wait for the client to PUBLISH something.
	publishCh chan capturedPublish

	// pingCh fans PINGREQ arrivals to the test.
	pingCh chan struct{}
}

type capturedPublish struct {
	Topic    string
	Payload  []byte
	QoS      byte
	Retain   bool
	PacketID uint16
}

func newFakeBroker(t *testing.T) *fakeBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	b := &fakeBroker{
		t:         t,
		ln:        ln,
		publishCh: make(chan capturedPublish, 8),
		pingCh:    make(chan struct{}, 8),
	}
	t.Cleanup(b.Close)
	return b
}

// URL returns the tcp:// broker URL pointing at the loopback listener.
func (b *fakeBroker) URL() string {
	return "tcp://" + b.ln.Addr().String()
}

// Start begins accepting one connection and serving the MQTT exchange.
// Returns once the listener is accepting; the goroutine continues in
// the background. The test should call helpers like WaitPublish /
// WaitPing to synchronise.
func (b *fakeBroker) Start() {
	b.wg.Add(1)
	go b.acceptLoop()
}

// Close terminates the listener + any active connection. Safe to call
// multiple times; bound to t.Cleanup so tests don't have to.
func (b *fakeBroker) Close() {
	if !b.closed.CompareAndSwap(false, true) {
		return
	}
	_ = b.ln.Close()
	b.mu.Lock()
	if b.conn != nil {
		_ = b.conn.Close()
	}
	b.mu.Unlock()
	b.wg.Wait()
}

// PublishToClient simulates an upstream message arriving for a topic
// the client has subscribed to. Encodes a QoS 0 PUBLISH and writes it
// onto the active connection.
func (b *fakeBroker) PublishToClient(topic string, payload []byte) error {
	b.mu.Lock()
	conn := b.conn
	b.mu.Unlock()
	if conn == nil {
		return errors.New("fakebroker: no active connection")
	}
	pkt := &protocol.PublishPacket{Topic: topic, Payload: payload, QoS: 0}
	return pkt.Encode(conn)
}

// Connect returns a copy of the captured CONNECT packet (nil before
// the client connects).
func (b *fakeBroker) Connect() *protocol.ConnectPacket {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.connect == nil {
		return nil
	}
	cp := *b.connect
	return &cp
}

// Subscribes returns the topic filters the client has subscribed to.
func (b *fakeBroker) Subscribes() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.subscribes...)
}

// Publishes returns every PUBLISH the client emitted.
func (b *fakeBroker) Publishes() []capturedPublish {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]capturedPublish(nil), b.publishes...)
}

// WaitPublish blocks until the client emits a PUBLISH or the deadline
// elapses. Returns the captured frame.
func (b *fakeBroker) WaitPublish(timeout time.Duration) (capturedPublish, bool) {
	select {
	case p := <-b.publishCh:
		return p, true
	case <-time.After(timeout):
		return capturedPublish{}, false
	}
}

// WaitPing blocks until the client sends PINGREQ or the deadline
// elapses. Returns true on success.
func (b *fakeBroker) WaitPing(timeout time.Duration) bool {
	select {
	case <-b.pingCh:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (b *fakeBroker) acceptLoop() {
	defer b.wg.Done()
	conn, err := b.ln.Accept()
	if err != nil {
		return // listener closed
	}
	b.mu.Lock()
	b.conn = conn
	b.mu.Unlock()

	if err := b.handshake(conn); err != nil {
		_ = conn.Close()
		return
	}
	b.serve(conn)
}

func (b *fakeBroker) handshake(conn net.Conn) error {
	br := bufio.NewReader(conn)
	frame, err := protocol.ReadFrame(br)
	if err != nil {
		return fmt.Errorf("fakebroker: read CONNECT: %w", err)
	}
	if frame.PacketType() != protocol.PacketConnect {
		return fmt.Errorf("fakebroker: first packet %d is not CONNECT", frame.PacketType())
	}
	cp, err := decodeConnect(frame.Body)
	if err != nil {
		return fmt.Errorf("fakebroker: decode CONNECT: %w", err)
	}
	b.mu.Lock()
	b.connect = cp
	b.mu.Unlock()

	// Encode CONNACK: 0x20, length 2, session-present 0, return code.
	if _, err := conn.Write([]byte{0x20, 0x02, 0x00, b.connackReturnCode}); err != nil {
		return fmt.Errorf("fakebroker: write CONNACK: %w", err)
	}
	if b.connackReturnCode != 0 {
		return errors.New("fakebroker: rejected client per configured CONNACK return code")
	}
	// Continue serving on the same reader so any frames already
	// buffered by bufio aren't lost (PUBLISH issued right after
	// CONNACK is the typical pattern).
	go b.serveWithReader(conn, br)
	return nil
}

// serve is unused — kept symmetric with handshake for clarity. Real
// serving runs from serveWithReader (the bufio reader is created in
// handshake to consume CONNECT).
func (b *fakeBroker) serve(_ net.Conn) {}

func (b *fakeBroker) serveWithReader(conn net.Conn, br *bufio.Reader) {
	for {
		frame, err := protocol.ReadFrame(br)
		if err != nil {
			return
		}
		switch frame.PacketType() {
		case protocol.PacketPublish:
			pkt, err := protocol.DecodePublish(frame.Header, frame.Body)
			if err != nil {
				b.t.Errorf("fakebroker: decode PUBLISH: %v", err)
				return
			}
			cp := capturedPublish{
				Topic:    pkt.Topic,
				Payload:  pkt.Payload,
				QoS:      pkt.QoS,
				PacketID: pkt.PacketID,
			}
			b.mu.Lock()
			b.publishes = append(b.publishes, cp)
			b.mu.Unlock()
			// Drain non-blocking — buffered channel; drop on overrun
			// rather than wedge the broker.
			select {
			case b.publishCh <- cp:
			default:
			}
			if pkt.QoS == 1 {
				if err := protocol.EncodePuback(conn, pkt.PacketID); err != nil {
					return
				}
			}

		case protocol.PacketSubscribe:
			// Body: packetID (uint16) + repeated [topicFilter, qos]
			if len(frame.Body) < 2 {
				return
			}
			packetID := binary.BigEndian.Uint16(frame.Body[:2])
			body := frame.Body[2:]
			topics := []string{}
			returnCodes := []byte{}
			for len(body) > 0 {
				if len(body) < 2 {
					return
				}
				n := int(binary.BigEndian.Uint16(body[:2]))
				body = body[2:]
				if len(body) < n+1 {
					return
				}
				topics = append(topics, string(body[:n]))
				returnCodes = append(returnCodes, body[n]) // requested QoS = granted QoS
				body = body[n+1:]
			}
			b.mu.Lock()
			b.subscribes = append(b.subscribes, topics...)
			b.mu.Unlock()
			// Encode SUBACK: header 0x90, remaining length = 2 + N return codes.
			var buf bytes.Buffer
			buf.WriteByte(0x90)
			buf.WriteByte(byte(2 + len(returnCodes)))
			_ = binary.Write(&buf, binary.BigEndian, packetID)
			buf.Write(returnCodes)
			if _, err := conn.Write(buf.Bytes()); err != nil {
				return
			}

		case protocol.PacketUnsubscribe:
			if len(frame.Body) < 2 {
				return
			}
			packetID := binary.BigEndian.Uint16(frame.Body[:2])
			body := frame.Body[2:]
			for len(body) > 0 {
				if len(body) < 2 {
					return
				}
				n := int(binary.BigEndian.Uint16(body[:2]))
				body = body[2:]
				if len(body) < n {
					return
				}
				b.mu.Lock()
				b.unsubscribes = append(b.unsubscribes, string(body[:n]))
				b.mu.Unlock()
				body = body[n:]
			}
			// Encode UNSUBACK: 0xB0 + length 2 + packetID.
			var hdr [4]byte
			hdr[0] = 0xB0
			hdr[1] = 0x02
			binary.BigEndian.PutUint16(hdr[2:], packetID)
			if _, err := conn.Write(hdr[:]); err != nil {
				return
			}

		case protocol.PacketPingreq:
			b.mu.Lock()
			b.pingreqs++
			b.mu.Unlock()
			select {
			case b.pingCh <- struct{}{}:
			default:
			}
			// PINGRESP: 0xD0 0x00
			if _, err := conn.Write([]byte{0xD0, 0x00}); err != nil {
				return
			}

		case protocol.PacketDisconnect:
			b.mu.Lock()
			b.disconnect = true
			b.mu.Unlock()
			return

		case protocol.PacketPuback:
			// Client doesn't currently send PUBACK back to us
			// (broker → client publishes are QoS 0). Ignore so
			// future-proofing doesn't trip the default arm.

		default:
			b.t.Errorf("fakebroker: unexpected packet type %d", frame.PacketType())
			return
		}
	}
}

// decodeConnect inverts ConnectPacket.Encode just enough for tests
// to verify ClientID + KeepAlive + auth fields.
func decodeConnect(body []byte) (*protocol.ConnectPacket, error) {
	if len(body) < 10 {
		return nil, errors.New("CONNECT body too short")
	}
	r := bytes.NewReader(body)
	name, err := readMQTTString(r)
	if err != nil {
		return nil, err
	}
	if name != "MQTT" {
		return nil, fmt.Errorf("unexpected protocol name %q", name)
	}
	level, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if level != 4 {
		return nil, fmt.Errorf("unexpected protocol level %d", level)
	}
	flags, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	var ka [2]byte
	if _, err := io.ReadFull(r, ka[:]); err != nil {
		return nil, err
	}
	keepAlive := binary.BigEndian.Uint16(ka[:])
	clientID, err := readMQTTString(r)
	if err != nil {
		return nil, err
	}
	cp := &protocol.ConnectPacket{
		ClientID:     clientID,
		KeepAlive:    keepAlive,
		CleanSession: flags&0x02 != 0,
	}
	if flags&0x04 != 0 {
		willTopic, err := readMQTTString(r)
		if err != nil {
			return nil, err
		}
		var l [2]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return nil, err
		}
		willLen := binary.BigEndian.Uint16(l[:])
		willPayload := make([]byte, willLen)
		if _, err := io.ReadFull(r, willPayload); err != nil {
			return nil, err
		}
		cp.WillTopic = willTopic
		cp.WillPayload = willPayload
		cp.WillRetain = flags&0x20 != 0
		cp.WillQoS = (flags >> 3) & 0x03
	}
	if flags&0x80 != 0 {
		username, err := readMQTTString(r)
		if err != nil {
			return nil, err
		}
		cp.Username = username
	}
	if flags&0x40 != 0 {
		password, err := readMQTTString(r)
		if err != nil {
			return nil, err
		}
		cp.Password = password
	}
	return cp, nil
}

func readMQTTString(r *bytes.Reader) (string, error) {
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return "", err
	}
	n := binary.BigEndian.Uint16(l[:])
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
