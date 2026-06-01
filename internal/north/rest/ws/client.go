// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// clientBufferSize is the max number of queued events per client.
// Slow clients overflowing this buffer are closed.
const clientBufferSize = 1000

// pingInterval is the server-side heartbeat cadence (§16.3: 30s).
const pingInterval = 30 * time.Second

// readTimeout is the deadline for each frame read — the client must
// respond to server pings within this window.
const readTimeout = 60 * time.Second

// client owns one WebSocket connection: its subscriptions, an
// outbound queue, and the goroutines reading/writing frames.
type client struct {
	conn   net.Conn
	br     *bufio.Reader
	bw     *bufio.Writer
	hub    *Hub
	logger *slog.Logger

	mu       sync.RWMutex
	topics   []string
	identity auth.Identity
	// classify, when set via a subscribe frame's `classify:true`, keeps
	// the quasi-static category / data_point_type fields on value-changed
	// payloads this client receives. Default off so the high-frequency
	// stream stays lean for clients that cache classification from the
	// snapshot catalogue instead.
	classify bool

	writeMu sync.Mutex

	out    chan Event
	closed chan struct{}
	once   sync.Once
}

func newClient(conn net.Conn, br *bufio.Reader, bw *bufio.Writer, hub *Hub, logger *slog.Logger) *client {
	return &client{
		conn:   conn,
		br:     br,
		bw:     bw,
		hub:    hub,
		logger: logger,
		out:    make(chan Event, clientBufferSize),
		closed: make(chan struct{}),
	}
}

// setClassify records the client's opt-in for inline DP classification
// on value-changed payloads. Guarded by the same mutex as the topic set
// since the write pump reads it on every dispatch.
func (c *client) setClassify(v bool) {
	c.mu.Lock()
	c.classify = v
	c.mu.Unlock()
}

// classifyEnabled reports whether this client opted into inline
// category / data_point_type on value-changed payloads.
func (c *client) classifyEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.classify
}

// subscribe adds topics to the subscription set (deduped).
func (c *client) subscribe(topics []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := make(map[string]struct{}, len(c.topics)+len(topics))
	for _, t := range c.topics {
		seen[t] = struct{}{}
	}
	for _, t := range topics {
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		c.topics = append(c.topics, t)
	}
}

func (c *client) unsubscribe(topics []string) {
	drop := make(map[string]struct{}, len(topics))
	for _, t := range topics {
		drop[t] = struct{}{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.topics[:0]
	for _, t := range c.topics {
		if _, rm := drop[t]; !rm {
			next = append(next, t)
		}
	}
	c.topics = next
}

func (c *client) matches(topic string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, p := range c.topics {
		if matchTopic(p, topic) {
			return true
		}
	}
	return false
}

func (c *client) enqueue(ev Event) {
	select {
	case c.out <- ev:
	default:
		// Buffer full → close the client.
		c.logger.Warn("ws.backpressure", slog.String("topic", ev.Topic))
		c.close()
	}
}

// replayFrom delivers buffered events with Seq > since that match
// the client's current subscriptions, then sends a control-frame
// acknowledgement: `{op: "replay_done", seq: lastSeq}` when the
// resume succeeded, or `{op: "replay_lost", oldest_seq: M}` when
// `since` precedes the oldest buffered event (client must take a
// fresh /snapshot).
func (c *client) replayFrom(since uint64) {
	res := c.hub.Replay(since, c.matches)
	if res.Lost {
		c.sendOp(outboundOp{Op: "replay_lost", OldestSeq: res.OldestSeq})
		return
	}
	var last uint64
	for _, ev := range res.Events {
		c.enqueue(ev)
		last = ev.Seq
	}
	if last == 0 {
		last = since
	}
	c.sendOp(outboundOp{Op: "replay_done", Seq: last})
}

// sendOp marshals a control-frame envelope and writes it directly
// to the wire. Failures are silent — the read/write pump will close
// the client on the next read deadline if the connection is dead.
func (c *client) sendOp(op outboundOp) {
	buf, err := json.Marshal(op)
	if err != nil {
		return
	}
	_ = c.writeFrame(opText, buf)
}

// reauth handles the in-band {op:"reauth", token:"..."} frame.
// Re-resolves token via the hub's TokenStore; on success swaps the
// connection's identity (subject + role) without forcing a reconnect.
// On failure (no store wired, empty token, unknown token) emits
// {op:"reauth_failed"} and closes the connection — clients can then
// reconnect with fresh credentials.
func (c *client) reauth(token string) {
	store := c.hub.TokenStore()
	if store == nil || token == "" {
		c.sendOp(outboundOp{Op: "reauth_failed"})
		c.close()
		return
	}
	id, err := store.AuthenticateToken(context.Background(), token)
	if err != nil {
		c.sendOp(outboundOp{Op: "reauth_failed"})
		c.close()
		return
	}
	c.mu.Lock()
	c.identity = id
	c.mu.Unlock()
	c.sendOp(outboundOp{Op: "reauth_ok"})
}

// SetIdentity records the connection's authenticated identity. The
// HTTP upgrade handler calls this once on accept; reauth() updates
// it in-place thereafter.
func (c *client) SetIdentity(id auth.Identity) {
	c.mu.Lock()
	c.identity = id
	c.mu.Unlock()
}

// Identity returns the current connection identity (zero-value when
// the upgrade handshake did not resolve one).
func (c *client) Identity() auth.Identity {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.identity
}

func (c *client) close() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
	})
}

// inboundMessage is the subset of client→server JSON we care about.
//
// Four operations are supported today:
//
//   - subscribe / unsubscribe — manage the client's topic membership.
//     subscribe accepts an optional `since` cursor that triggers a
//     replay of buffered events with Seq > since (see ADR 0022)
//   - pong — heartbeat acknowledgement
//   - call — RPC-style command dispatch (see [Router]); requires
//     `id` for response correlation and `command` for routing
type inboundMessage struct {
	Op      string          `json:"op"`
	Topics  []string        `json:"topics,omitempty"`
	Since   *uint64         `json:"since,omitempty"`
	Token   string          `json:"token,omitempty"`
	ID      string          `json:"id,omitempty"`
	Command string          `json:"command,omitempty"`
	Args    json.RawMessage `json:"args,omitempty"`
	// Classify, when present on a subscribe frame, toggles inline
	// category / data_point_type on value-changed payloads for this
	// client. Pointer so an absent field leaves the current preference
	// untouched across re-subscribes.
	Classify *bool `json:"classify,omitempty"`
}

// outboundEvent is the envelope every server→client event uses.
// Seq is the monotonic cursor a reconnecting client passes back as
// `since` on its next subscribe; Kind tags the event family
// ("initial" first observation, "change" delta, "refresh" periodic
// re-emit). See ADR 0022.
type outboundEvent struct {
	Seq     uint64 `json:"seq"`
	Kind    string `json:"kind"`
	Topic   string `json:"topic"`
	Type    string `json:"type"`
	TS      string `json:"ts"`
	Payload any    `json:"payload"`
}

// outboundOp is the simple envelope for ping, pong, subscribe ACKs
// and replay control frames. Op-specific fields are optional so a
// single struct covers every wire shape the client sees.
type outboundOp struct {
	Op        string   `json:"op"`
	Seq       uint64   `json:"seq,omitempty"`
	OldestSeq uint64   `json:"oldest_seq,omitempty"`
	Topics    []string `json:"topics,omitempty"`
}

// readPump reads inbound frames and updates subscriptions. Runs
// until the connection closes or the client misbehaves.
func (c *client) readPump() {
	defer c.close()
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(readTimeout))
		f, err := readFrame(c.br)
		if err != nil {
			return
		}
		switch f.opcode {
		case opText:
			var msg inboundMessage
			if err := json.Unmarshal(f.payload, &msg); err != nil {
				c.logger.Warn("ws.malformed", slog.String("err", err.Error()))
				continue
			}
			switch msg.Op {
			case "subscribe":
				if msg.Classify != nil {
					c.setClassify(*msg.Classify)
				}
				c.subscribe(msg.Topics)
				c.sendAck("subscribed", msg.Topics)
				if msg.Since != nil {
					c.replayFrom(*msg.Since)
				}
			case "unsubscribe":
				c.unsubscribe(msg.Topics)
				c.sendAck("unsubscribed", msg.Topics)
			case "pong":
				// heartbeat ack — nothing to do
			case "reauth":
				c.reauth(msg.Token)
			case "call":
				c.handleCommand(msg)
			}
		case opPing:
			_ = c.writeFrame(opPong, f.payload)
		case opClose:
			return
		}
	}
}

// writePump dispatches enqueued events plus heartbeats.
func (c *client) writePump() {
	defer c.close()
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.closed:
			return
		case ev := <-c.out:
			kind := ev.Kind
			if kind == "" {
				kind = KindChange
			}
			payload := ev.Payload
			// Strip the inline classification fields unless this client
			// opted into them. The payload is a value type, so the copy
			// here never mutates the buffered event other clients read.
			if dp, ok := payload.(DataPointValueChangedPayload); ok && !c.classifyEnabled() {
				dp.Category = ""
				dp.DataPointType = ""
				payload = dp
			}
			frame := outboundEvent{
				Seq:     ev.Seq,
				Kind:    kind,
				Topic:   ev.Topic,
				Type:    ev.Type,
				TS:      ev.When.UTC().Format("2006-01-02T15:04:05.000Z"),
				Payload: payload,
			}
			buf, err := json.Marshal(frame)
			if err != nil {
				continue
			}
			if err := c.writeFrame(opText, buf); err != nil {
				return
			}
		case <-ticker.C:
			buf, _ := json.Marshal(outboundOp{Op: "ping"})
			if err := c.writeFrame(opText, buf); err != nil {
				return
			}
		}
	}
}

// writeFrame is the single serialization point for the connection's
// write half. readPump (pong, ACKs) and writePump (events, heartbeats)
// share the same bufio.Writer; without the mutex their bytes can
// interleave and produce malformed frames.
func (c *client) writeFrame(op byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return writeFrame(c.bw, op, payload)
}

// sendAck confirms a subscribe / unsubscribe operation. The topics
// slice mirrors what the client requested so a reconnect can be
// reasoned about without inspecting daemon-side state.
func (c *client) sendAck(op string, topics []string) {
	if len(topics) == 0 {
		return
	}
	buf, err := json.Marshal(outboundOp{Op: op, Topics: topics})
	if err != nil {
		return
	}
	_ = c.writeFrame(opText, buf)
}

// handleCommand dispatches an inbound `call` frame through the hub's
// router and writes the result back to the client. A frame without
// an `id` is rejected with a `bad_request` error so callers know to
// supply a correlation id; a frame without a `command` field gets the
// same treatment.
//
// The dispatch uses a short context derived from the connection — the
// caller cannot wait forever for a slow handler, and writing the
// response respects the regular write deadline. Errors during the
// response write tear the connection down; the readPump's defer
// `c.close` handles that.
func (c *client) handleCommand(msg inboundMessage) {
	resp := outboundResult{Op: "result", ID: msg.ID}
	switch {
	case msg.ID == "":
		resp.Error = NewCommandError(CommandErrorBadRequest, "missing id")
	case msg.Command == "":
		resp.Error = NewCommandError(CommandErrorBadRequest, "missing command")
	default:
		ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
		defer cancel()
		result := c.hub.router.Dispatch(ctx, msg.Command, msg.Args)
		if result.Error != nil {
			resp.Error = result.Error
		} else {
			resp.Data = result.Data
		}
	}
	buf, err := json.Marshal(resp)
	if err != nil {
		c.logger.Warn("ws.command.marshal", slog.String("err", err.Error()))
		return
	}
	if err := c.writeFrame(opText, buf); err != nil {
		c.logger.Warn("ws.command.write", slog.String("err", err.Error()))
	}
}

// commandTimeout caps how long a single command may run. Mirrors the
// rough ceiling Home Assistant applies (10 s) — most CCU operations
// finish in <1 s; anything slower likely warrants async progress
// reporting via a separate event topic instead of blocking the
// connection.
const commandTimeout = 10 * time.Second
