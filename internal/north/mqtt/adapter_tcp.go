// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/mqtt/protocol"
)

// TCPConfig wires a [TCPClient] against a real broker.
type TCPConfig struct {
	BrokerURL    string // tcp://host:1883 or tls://host:8883
	ClientID     string
	Username     string
	Password     string
	KeepAlive    time.Duration // floor: 30s per SPEC §18.1
	DialTimeout  time.Duration // default 10s
	AckTimeout   time.Duration // PUBACK wait, default 20s
	TLSConfig    *tls.Config
	WillTopic    string
	WillPayload  []byte
	WillRetain   bool
	CleanSession bool
	Logger       *slog.Logger
}

// subscriberEntry stores the handler and the QoS level a filter was
// originally subscribed at. The QoS is replayed on reconnect so every
// filter is restored at the same delivery guarantee the caller requested.
type subscriberEntry struct {
	handler MessageHandler
	qos     QoS
}

// TCPClient is a pure-Go MQTT 3.1.1 client used by the Bridge's
// Lifecycle.
//
// It implements both [Client] (Publish + Subscribe/Unsubscribe) and
// [Connector] (Connect + Disconnect) so the bridge composes one
// object for the full lifecycle.
type TCPClient struct {
	cfg    TCPConfig
	logger *slog.Logger

	mu     sync.Mutex
	conn   net.Conn
	writer *bufio.Writer
	reader *bufio.Reader

	nextID atomic.Uint32

	ackMu sync.Mutex
	acks  map[uint16]chan struct{}

	subMu       sync.RWMutex
	subscribers map[string]subscriberEntry

	sendMu sync.Mutex // serialises frame writes

	// pingInterval is how often keepAliveLoop sends a PINGREQ; it also
	// bounds the PINGRESP watchdog window. Defaults to KeepAlive/2 in
	// NewTCPClient; tests override it directly to avoid the 30 s floor.
	pingInterval time.Duration
	// pingOutstanding is true between sending a PINGREQ and receiving
	// its PINGRESP. If it is still set when the next ticker fires, the
	// broker has gone silent on a half-open socket and the connection
	// is declared lost.
	pingOutstanding atomic.Bool

	stop    chan struct{}
	stopped atomic.Bool
	wg      sync.WaitGroup

	// connectedAt holds the wall-clock instant of the most recent
	// successful TCP+CONNECT round-trip. Cleared on Disconnect.
	// Read by the diagnostics health probe; never the hot path.
	connectedAt atomic.Pointer[time.Time]
}

// IsConnected reports whether the client currently holds an active
// MQTT session. Used by the diagnostics health probe to derive the
// `mqtt` health component.
func (c *TCPClient) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && !c.stopped.Load()
}

// LastConnectedAt returns the timestamp of the most recent successful
// connect, or the zero time when no connect has happened yet.
func (c *TCPClient) LastConnectedAt() time.Time {
	p := c.connectedAt.Load()
	if p == nil {
		return time.Time{}
	}
	return *p
}

// NewTCPClient constructs a new client.
func NewTCPClient(cfg TCPConfig) *TCPClient {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.KeepAlive < 30*time.Second {
		cfg.KeepAlive = 30 * time.Second
	}
	if cfg.DialTimeout == 0 {
		cfg.DialTimeout = 10 * time.Second
	}
	if cfg.AckTimeout == 0 {
		cfg.AckTimeout = 20 * time.Second
	}
	return &TCPClient{
		cfg:          cfg,
		logger:       cfg.Logger,
		acks:         make(map[uint16]chan struct{}),
		subscribers:  make(map[string]subscriberEntry),
		stop:         make(chan struct{}),
		pingInterval: cfg.KeepAlive / 2,
	}
}

// Connect implements [Connector]. It dials, sends CONNECT, waits for
// CONNACK, and starts the read pump + keep-alive loop.
func (c *TCPClient) Connect(ctx context.Context) error { //nolint:funlen // single-purpose TCP connect with many protocol/error branches
	c.mu.Lock()
	if c.conn != nil {
		c.mu.Unlock()
		return errors.New("mqtt/tcp: already connected")
	}
	c.mu.Unlock()

	u, err := url.Parse(c.cfg.BrokerURL)
	if err != nil {
		return fmt.Errorf("mqtt/tcp: bad broker url: %w", err)
	}

	dialCtx, cancel := context.WithTimeout(ctx, c.cfg.DialTimeout)
	defer cancel()
	conn, err := c.dial(dialCtx, u)
	if err != nil {
		return fmt.Errorf("mqtt/tcp: dial: %w", err)
	}

	pkt := &protocol.ConnectPacket{
		ClientID:     c.cfg.ClientID,
		KeepAlive:    uint16(c.cfg.KeepAlive.Seconds()), //nolint:gosec // clamped above; see #20
		Username:     c.cfg.Username,
		Password:     c.cfg.Password,
		CleanSession: c.cfg.CleanSession,
		WillTopic:    c.cfg.WillTopic,
		WillPayload:  c.cfg.WillPayload,
		WillRetain:   c.cfg.WillRetain,
	}
	bw := bufio.NewWriter(conn)
	if err := pkt.Encode(bw); err != nil {
		_ = conn.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		_ = conn.Close()
		return err
	}

	_ = conn.SetReadDeadline(time.Now().Add(c.cfg.DialTimeout))
	br := bufio.NewReader(conn)
	frame, err := protocol.ReadFrame(br)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mqtt/tcp: read connack: %w", err)
	}
	if frame.PacketType() != protocol.PacketConnack {
		_ = conn.Close()
		return fmt.Errorf("mqtt/tcp: unexpected packet %d instead of CONNACK", frame.PacketType())
	}
	ack, err := protocol.DecodeConnack(frame.Body)
	if err != nil {
		_ = conn.Close()
		return err
	}
	if ack.ReturnCode != 0 {
		_ = conn.Close()
		return fmt.Errorf("mqtt/tcp: CONNACK return code %d", ack.ReturnCode)
	}
	_ = conn.SetReadDeadline(time.Time{})

	c.mu.Lock()
	c.conn = conn
	c.writer = bw
	c.reader = br
	c.stop = make(chan struct{})
	stopCh := c.stop
	c.stopped.Store(false)
	c.pingOutstanding.Store(false)
	c.mu.Unlock()
	now := time.Now()
	c.connectedAt.Store(&now)

	c.wg.Add(2)
	go c.readLoop(stopCh)
	go c.keepAliveLoop(stopCh)

	// Replay any prior subscriptions on reconnect. Without this, a
	// CleanSession=true client (the typical daemon configuration)
	// loses every SUBSCRIBE on the previous socket and the new
	// session starts with an empty filter set — HA's
	// `set_temperature` / `set_mode` / `set_profile` commands
	// arrive at the broker but are never delivered to the daemon.
	c.subMu.RLock()
	type filterQoS struct {
		filter string
		qos    QoS
	}
	subs := make([]filterQoS, 0, len(c.subscribers))
	for f, entry := range c.subscribers {
		subs = append(subs, filterQoS{filter: f, qos: entry.qos})
	}
	c.subMu.RUnlock()
	for _, s := range subs {
		pkt := &protocol.SubscribePacket{PacketID: c.nextPacketID(), TopicFilter: s.filter, QoS: byte(s.qos)}
		if err := c.writeFrame(pkt); err != nil {
			c.logger.Warn("mqtt.tcp.resubscribe",
				slog.String("filter", s.filter),
				slog.String("err", err.Error()))
		}
	}

	c.logger.Info("mqtt.tcp.connected", slog.String("broker", c.cfg.BrokerURL))
	return nil
}

// Disconnect implements [Connector]. It sends DISCONNECT, closes the
// socket, and waits for the goroutines to exit.
func (c *TCPClient) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	conn := c.conn
	if conn == nil {
		c.mu.Unlock()
		return nil
	}
	if c.stopped.CompareAndSwap(false, true) {
		close(c.stop)
	}
	c.conn = nil
	c.mu.Unlock()

	// Best effort: a graceful DISCONNECT.
	c.sendMu.Lock()
	_ = protocol.EncodeDisconnect(c.writer)
	_ = c.writer.Flush()
	c.sendMu.Unlock()
	_ = conn.Close()

	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	c.logger.Info("mqtt.tcp.disconnected")
	return nil
}

// Publish implements [Publisher]. QoS 0 is fire-and-forget; QoS 1
// waits for PUBACK up to cfg.AckTimeout.
func (c *TCPClient) Publish(ctx context.Context, topic string, payload []byte, qos QoS, retain bool) error {
	if qos > QoS1 {
		return fmt.Errorf("mqtt/tcp: unsupported QoS %d", qos)
	}
	pkt := &protocol.PublishPacket{Topic: topic, Payload: payload, QoS: byte(qos), Retain: retain}
	if qos == 0 {
		return c.writeFrame(pkt)
	}

	pkt.PacketID = c.nextPacketID()
	// Register the ack channel BEFORE the PUBLISH hits the wire —
	// otherwise the broker can answer faster than the registration
	// runs (loopback paths do this routinely) and the PUBACK
	// arrives at the read loop while c.acks is still empty.
	ch := make(chan struct{})
	c.ackMu.Lock()
	c.acks[pkt.PacketID] = ch
	c.ackMu.Unlock()
	defer c.removeAck(pkt.PacketID)

	if err := c.writeFrame(pkt); err != nil {
		return err
	}

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(c.cfg.AckTimeout):
		return fmt.Errorf("mqtt/tcp: PUBACK timeout (id=%d)", pkt.PacketID)
	}
}

// Subscribe implements [Subscriber]. Only one handler per topic
// filter — re-subscribing replaces the previous handler.
func (c *TCPClient) Subscribe(ctx context.Context, filter string, qos QoS, handler MessageHandler) error {
	pkt := &protocol.SubscribePacket{PacketID: c.nextPacketID(), TopicFilter: filter, QoS: byte(qos)}
	if err := c.writeFrame(pkt); err != nil {
		return err
	}
	c.subMu.Lock()
	c.subscribers[filter] = subscriberEntry{handler: handler, qos: qos}
	c.subMu.Unlock()
	_ = ctx
	return nil
}

// Unsubscribe implements [Subscriber].
func (c *TCPClient) Unsubscribe(ctx context.Context, filter string) error {
	pkt := &protocol.UnsubscribePacket{PacketID: c.nextPacketID(), TopicFilter: filter}
	if err := c.writeFrame(pkt); err != nil {
		return err
	}
	c.subMu.Lock()
	delete(c.subscribers, filter)
	c.subMu.Unlock()
	_ = ctx
	return nil
}

// --- internals ---

func (c *TCPClient) dial(ctx context.Context, u *url.URL) (net.Conn, error) {
	host := u.Host
	if u.Port() == "" {
		switch u.Scheme {
		case "tls", "ssl", "mqtts":
			host = net.JoinHostPort(u.Hostname(), "8883")
		default:
			host = net.JoinHostPort(u.Hostname(), "1883")
		}
	}
	dialer := &net.Dialer{}
	switch u.Scheme {
	case "tcp", "mqtt", "":
		return dialer.DialContext(ctx, "tcp", host)
	case "tls", "ssl", "mqtts":
		tcpConn, err := dialer.DialContext(ctx, "tcp", host)
		if err != nil {
			return nil, err
		}
		tlsConn := tls.Client(tcpConn, c.cfg.TLSConfig)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = tcpConn.Close()
			return nil, err
		}
		return tlsConn, nil
	}
	return nil, fmt.Errorf("mqtt/tcp: unsupported scheme %q", u.Scheme)
}

type frameEncoder interface {
	Encode(w io.Writer) error
}

func (c *TCPClient) writeFrame(pkt frameEncoder) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.mu.Lock()
	writer := c.writer
	c.mu.Unlock()
	if writer == nil {
		return errors.New("mqtt/tcp: not connected")
	}
	if err := pkt.Encode(writer); err != nil {
		return err
	}
	return writer.Flush()
}

func (c *TCPClient) nextPacketID() uint16 {
	for {
		v := c.nextID.Add(1)
		id := uint16(v & 0xFFFF)
		if id == 0 {
			continue
		}
		return id
	}
}

func (c *TCPClient) removeAck(id uint16) {
	c.ackMu.Lock()
	delete(c.acks, id)
	c.ackMu.Unlock()
}

func (c *TCPClient) readLoop(stop <-chan struct{}) {
	defer c.wg.Done()
	for {
		select {
		case <-stop:
			return
		default:
		}
		c.mu.Lock()
		reader := c.reader
		c.mu.Unlock()
		if reader == nil {
			return
		}
		frame, err := protocol.ReadFrame(reader)
		if err != nil {
			if !c.stopped.Load() {
				c.logger.Warn("mqtt.tcp.read", slog.String("err", err.Error()))
				// Tear down the broken socket so the lifecycle's
				// reconnect loop can establish a fresh connection.
				// Without this, `c.conn` stays non-nil after the
				// remote side closes the TCP socket, the next
				// [Connect] returns `mqtt/tcp: already connected`,
				// and the daemon's subscriptions silently die —
				// HA's `set_temperature` / `set_mode` /
				// `set_profile` MQTT commands stop reaching the
				// service-method handler.
				c.handleConnectionLost()
			}
			return
		}
		switch frame.PacketType() { //nolint:exhaustive // outbound-only packet types never reach the read path
		case protocol.PacketPublish:
			ib, err := protocol.DecodePublish(frame.Header, frame.Body)
			if err != nil {
				c.logger.Warn("mqtt.tcp.malformed_publish", slog.String("err", err.Error()))
				continue
			}
			c.dispatch(ib)
			if ib.QoS == 1 {
				c.sendMu.Lock()
				_ = protocol.EncodePuback(c.writer, ib.PacketID)
				_ = c.writer.Flush()
				c.sendMu.Unlock()
			}
		case protocol.PacketPuback:
			if p, err := protocol.DecodePuback(frame.Body); err == nil {
				c.ackMu.Lock()
				if ch, ok := c.acks[p.PacketID]; ok {
					close(ch)
					delete(c.acks, p.PacketID)
				}
				c.ackMu.Unlock()
			}
		case protocol.PacketPingresp:
			// Heartbeat ack: the broker is alive, so clear the
			// watchdog flag set when keepAliveLoop sent the PINGREQ.
			c.pingOutstanding.Store(false)
		case protocol.PacketSuback, protocol.PacketUnsuback:
			// non-blocking in our MVP; the subscribe/unsubscribe
			// calls return as soon as the frame is on the wire.
		}
	}
}

func (c *TCPClient) keepAliveLoop(stop <-chan struct{}) {
	defer c.wg.Done()
	ticker := time.NewTicker(c.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// Watchdog: a PINGREQ from the previous tick that never
			// drew a PINGRESP means the socket is half-open — the
			// broker or network vanished without a TCP FIN/RST, so
			// readLoop stays blocked in ReadFrame forever and never
			// trips handleConnectionLost. Declare the connection lost
			// so the lifecycle reconnects. Without this, publishes
			// silently time out on a dead socket until a manual
			// restart.
			if c.pingOutstanding.Load() {
				c.logger.Warn("mqtt.tcp.ping_timeout")
				c.handleConnectionLost()
				return
			}
			c.sendMu.Lock()
			c.mu.Lock()
			writer := c.writer
			c.mu.Unlock()
			if writer == nil {
				c.sendMu.Unlock()
				return
			}
			if err := protocol.EncodePingReq(writer); err != nil {
				c.sendMu.Unlock()
				c.logger.Warn("mqtt.tcp.ping", slog.String("err", err.Error()))
				c.handleConnectionLost()
				return
			}
			if err := writer.Flush(); err != nil {
				c.sendMu.Unlock()
				c.logger.Warn("mqtt.tcp.ping", slog.String("err", err.Error()))
				c.handleConnectionLost()
				return
			}
			// Arm the watchdog only after the PINGREQ is on the wire.
			c.pingOutstanding.Store(true)
			c.sendMu.Unlock()
		}
	}
}

// handleConnectionLost tears down the in-flight TCP socket after the
// read or keep-alive loop has detected a remote-side close. Resets
// `c.conn` / `c.reader` / `c.writer` to nil so the next
// [TCPClient.Connect] call dials a fresh socket instead of returning
// `mqtt/tcp: already connected`. Idempotent — concurrent callers
// converge on the same nil-state.
//
// Without this, a connection lost mid-flight (broker restart, NAT
// timeout, transient network glitch) leaves the lifecycle's
// reconnect loop spinning on `already connected` errors forever:
// the daemon's MQTT subscriptions silently expire and HA's
// `set_temperature` / `set_mode` / `set_profile` commands stop
// being delivered.
func (c *TCPClient) handleConnectionLost() {
	c.mu.Lock()
	conn := c.conn
	c.conn = nil
	c.reader = nil
	c.writer = nil
	if c.stopped.CompareAndSwap(false, true) {
		close(c.stop)
	}
	c.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

func (c *TCPClient) dispatch(ib *protocol.InboundPublish) {
	c.subMu.RLock()
	var handler MessageHandler
	for filter, entry := range c.subscribers {
		if topicMatches(filter, ib.Topic) {
			handler = entry.handler
			break
		}
	}
	c.subMu.RUnlock()
	if handler != nil {
		handler(ib.Topic, ib.Payload, ib.Retain)
	}
}

// topicMatches implements the minimal MQTT wildcard rules the
// bridge relies on: `+` matches one level, `#` matches multiple.
func topicMatches(filter, topic string) bool {
	if filter == topic {
		return true
	}
	fp, tp := 0, 0
	for fp < len(filter) && tp < len(topic) {
		fc, tc := filter[fp], topic[tp]
		switch fc {
		case '#':
			return true
		case '+':
			for tp < len(topic) && topic[tp] != '/' {
				tp++
			}
			fp++
		default:
			if fc != tc {
				return false
			}
			fp++
			tp++
		}
	}
	return fp == len(filter) && tp == len(topic)
}

// Confirm TCPClient satisfies both bridge contracts at compile time.
var (
	_ Client    = (*TCPClient)(nil)
	_ Connector = (*TCPClient)(nil)
)
