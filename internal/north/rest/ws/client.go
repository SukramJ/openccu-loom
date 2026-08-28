// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// clientBufferSize is the max number of queued events per client.
// A client that overflows it loses the oldest events, not its
// connection — see [client.enqueue].
const clientBufferSize = 1000

// overflowDropFraction is the share of the outbound buffer discarded on
// overflow. Dropping a block rather than a single event keeps the
// overflow path off the hot path for the rest of the burst: with a
// single-event drop, every subsequent event in the same flood re-enters
// it.
const overflowDropFraction = 2

// maxTopicsPerClient caps the retained subscription set per connection. Each
// subscribe frame appends deduped topics with no natural bound, so an
// authenticated client could otherwise grow the set without limit — exhausting
// memory and driving the per-event O(n) match cost up for every dispatch. A
// real client subscribes to a modest set of topics/patterns; 1024 is far above
// that. Additions past the cap are dropped.
const maxTopicsPerClient = 1024

// pingInterval is the server-side heartbeat cadence (§16.3: 30s).
const pingInterval = 30 * time.Second

// readTimeout is the deadline for each frame read — the client must
// respond to server pings within this window.
const readTimeout = 60 * time.Second

// closeFlushTimeout bounds how long [client.failConnection] waits for the
// writer goroutine to emit the close frame. Normal completion is immediate;
// the bound only matters when the peer has stopped reading.
const closeFlushTimeout = 5 * time.Second

// kindReplayDoneMarker tags an [Event] queued on c.out as the
// replay-completion ack rather than a real broadcast — see
// [client.replayFrom] for why it travels through the domain queue
// instead of c.ctrl. writePump intercepts it before the normal
// outboundEvent path, so it never reaches a real subscriber's wire
// payload and never needs to be a value a domain producer could emit by
// accident (it does not appear in the [KindInitial] / [KindChange] /
// [KindRefresh] wire vocabulary).
const kindReplayDoneMarker = "$replay_done"

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
	// releasedOnly, when set via a subscribe frame's `released_only:true`,
	// drops every frame about a device the onboarding wizard has not
	// released. Default off, because this plane's other consumer — the
	// Config UI — has to see exactly those devices to configure them.
	releasedOnly bool

	// out carries domain events (topic broadcasts) to the writer
	// goroutine; ctrl carries every other outbound frame (subscribe/
	// unsubscribe ACKs, pong replies, reauth results, replay markers,
	// `call` command results). writePump is the single reader of both
	// channels and therefore the connection's only writer — see
	// writePump's doc comment for why that removes the need for a
	// write mutex.
	out    chan Event
	ctrl   chan wireMsg
	closed chan struct{}
	once   sync.Once

	// gapSignalled marks an in-flight overflow episode: the client has
	// been told its stream has a gap and does not need telling again
	// until the writer has drained the queue. Without it a single flood
	// produces one warning and one resync frame per dropped event.
	gapSignalled atomic.Bool

	// born anchors the monotonic reading the heartbeat echo carries. Using
	// an elapsed duration rather than a wall-clock stamp keeps the round-trip
	// immune to an NTP step landing inside the 30s window.
	born time.Time

	// elapsed reads the connection age. Production leaves it nil and takes
	// time.Since(born); a test overrides it to pin both ends of a round-trip
	// to chosen readings. It exists because the sub-tick case — pong and ping
	// landing in the same monotonic tick, which is what the one-nanosecond
	// floor in noteHeartbeat is for — cannot be scheduled from outside, and a
	// guard that cannot reach the case it names proves nothing.
	elapsed func() time.Duration

	// pingSeq issues the heartbeat echo tokens. It starts at 0 and is
	// pre-incremented, so the first live token is 1 and 0 stays available as
	// the "no ping outstanding" sentinel.
	pingSeq atomic.Uint64

	// pendingMu guards the outstanding ping's token and send time as a pair.
	// writePump arms them together; readPump clears the token and reads the
	// time under the same lock, so a pong can never be timed against the send
	// of a different ping. pendingEcho is 0 when nothing is outstanding, which
	// is why the counter above never issues that value.
	pendingMu     sync.Mutex
	pendingEcho   uint64
	pendingSentAt time.Duration

	// lastRTT is the most recently measured heartbeat round-trip, in
	// nanoseconds. Zero until the first pong carrying an echo arrives —
	// a client that answers the heartbeat without echoing stays unmeasured
	// rather than reporting a wrong number.
	lastRTT atomic.Int64
}

// wireMsg is one pre-serialised control-plane frame queued for the
// writer goroutine via [client.ctrl].
type wireMsg struct {
	op      byte
	payload []byte
}

func newClient(conn net.Conn, br *bufio.Reader, bw *bufio.Writer, hub *Hub, logger *slog.Logger) *client {
	return &client{
		conn:   conn,
		br:     br,
		bw:     bw,
		hub:    hub,
		logger: logger,
		born:   time.Now(),
		out:    make(chan Event, clientBufferSize),
		ctrl:   make(chan wireMsg, clientBufferSize),
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

// setReleasedOnly records the client's opt-in to the onboarding filter.
func (c *client) setReleasedOnly(v bool) {
	c.mu.Lock()
	c.releasedOnly = v
	c.mu.Unlock()
}

// releasedOnlyEnabled reports whether this client asked to be spared
// devices that are still being onboarded.
func (c *client) releasedOnlyEnabled() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.releasedOnly
}

// shapeForThisClient decides what a frame becomes for this connection: it
// is dropped, it is rewritten, or it passes untouched.
//
// Three outcomes, because there are three kinds of payload:
//
//   - one ABOUT a withheld device is dropped — that is the filter;
//   - a hub entity that merely NAMES a withheld device keeps its own
//     existence but loses the association, because a subscriber that
//     cannot see the device can neither attach the entity to it nor
//     should lose the entity over it;
//   - everything else passes.
//
// The device.released frame passes on its own without a special case: the
// state flips before the event is published, so by the time it reaches
// here the address reads released. That is the frame that lifts the
// filter, and dropping it would strand a filtering client forever.
func (c *client) shapeForThisClient(payload any) (any, bool) {
	if !c.releasedOnlyEnabled() {
		return payload, true
	}
	if p, ok := payload.(DeviceScopedPayload); ok {
		addr := p.DeviceAddr()
		if addr != "" && !c.hub.deviceReleased(addr) {
			return nil, false
		}
		return payload, true
	}
	if p, ok := payload.(DeviceAssociatedPayload); ok {
		addr := p.AssociatedDeviceAddr()
		if addr != "" && !c.hub.deviceReleased(addr) {
			return p.WithoutDeviceAssociation(), true
		}
	}
	return payload, true
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
		if len(c.topics) >= maxTopicsPerClient {
			// Subscription set is full; drop further additions so a client
			// cannot grow it without bound. Existing subscriptions keep working.
			break
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

// enqueue queues a domain event for the writer goroutine.
//
// On overflow the client loses events, not its connection: the oldest
// block in the queue is discarded, the new event takes its place, and
// the client is told once that its stream has a gap so it can resync
// from a snapshot — the same contract a `since` cursor older than the
// replay ring already gets (ADR 0022).
//
// Closing instead, as this did originally, made every slow or
// briefly-blocked consumer lose its session. The boot snapshot fans one
// event out per data point, so on a large installation a daemon restart
// cut every open SPA session and filled the log while doing it: the
// closed client stayed registered on the hub, so the publisher kept
// selecting it and the overflow branch logged again for each attempt.
func (c *client) enqueue(ev Event) {
	select {
	case <-c.closed:
		return
	case c.out <- ev:
		return
	default:
	}

	// At least one, so a small buffer still makes room instead of
	// discarding the event that just arrived and keeping stale ones.
	dropped := c.dropOldest(max(1, cap(c.out)/overflowDropFraction))
	select {
	case c.out <- ev:
	default:
		// The writer is not draining at all; this event goes too. The
		// gap signal below covers it.
		dropped++
	}
	c.signalGap(ev.Topic, dropped)
}

// dropOldest discards up to n queued events, oldest first, and reports
// how many it removed. Non-blocking: it stops as soon as the queue runs
// dry, so a concurrent writer draining in parallel cannot stall it.
func (c *client) dropOldest(n int) int {
	dropped := 0
	for range n {
		select {
		case <-c.out:
			dropped++
		default:
			return dropped
		}
	}
	return dropped
}

// signalGap warns and tells the client to resync, once per overflow
// episode. [client.noteDrained] ends the episode.
func (c *client) signalGap(topic string, dropped int) {
	if !c.gapSignalled.CompareAndSwap(false, true) {
		return
	}
	c.logger.Warn("ws.backpressure",
		slog.String("topic", topic),
		slog.Int("dropped", dropped),
		slog.String("action", "client asked to resync from a snapshot"))
	// OldestSeq anchors the resume: everything up to the hub's current
	// top either arrived or was dropped and the client cannot tell
	// which, so it resyncs from a snapshot and resumes above this mark.
	var anchor uint64
	if c.hub != nil {
		anchor = c.hub.CurrentSeq()
	}
	c.sendOp(outboundOp{Op: "replay_lost", OldestSeq: anchor})
}

// noteDrained ends an overflow episode once the writer has emptied the
// queue, so a later overflow warns again rather than passing silently.
func (c *client) noteDrained() {
	if len(c.out) == 0 {
		c.gapSignalled.Store(false)
	}
}

// enqueueCtrl queues a pre-serialised control-plane frame for the writer
// goroutine. Never blocks: a full queue means the client is not draining
// fast enough, and — mirroring [client.enqueue]'s policy for domain
// events — the connection is closed rather than left to stall the
// caller (typically readPump) for up to the write deadline.
func (c *client) enqueueCtrl(op byte, payload []byte) {
	select {
	case c.ctrl <- wireMsg{op: op, payload: payload}:
	default:
		// Control frames carry ACKs, auth results and command replies —
		// dropping one desynchronises the client silently, so this plane
		// keeps the strict policy. Warn once; close() is idempotent but
		// the log line is not.
		if c.gapSignalled.CompareAndSwap(false, true) {
			c.logger.Warn("ws.backpressure",
				slog.String("kind", "control"),
				slog.String("action", "connection closed; control frames cannot be dropped"))
		}
		c.close()
	}
}

// replayFrom delivers buffered events with Seq > since that match
// the client's current subscriptions, then sends a control-frame
// acknowledgement: `{op: "replay_done", seq: lastSeq}` when the
// resume succeeded, or `{op: "replay_lost", oldest_seq: M}` when
// `since` precedes the oldest buffered event (client must take a
// fresh /snapshot).
//
// The success ack is queued on c.out — the same channel every replayed
// event above just went through — via [client.enqueue], not on c.ctrl.
// writePump's select has no ordering guarantee between the two channels
// (Go picks uniformly among ready cases), so a `replay_done` sent on
// c.ctrl can reach the wire before the tail of a large replay batch
// still sitting in c.out, telling the client "everything after this is
// live" while hundreds of the replayed events it just labelled as
// current are still ahead of it. Routing it through c.out instead makes
// FIFO order within one channel do the ordering, matching the documented
// wire contract. `replay_lost` (both branches) needs no such treatment:
// the res.Lost case returns before anything is queued, and the overflow
// case in [client.signalGap] already tells the client its buffered
// events are unreliable regardless of interleaving.
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
	c.enqueue(Event{Kind: kindReplayDoneMarker, Seq: last})
}

// sendOp marshals a control-frame envelope and queues it for the
// writer goroutine. Marshal failures are silent (nothing to send);
// a dead connection surfaces via the writer goroutine's own close on
// the next physical write failure.
func (c *client) sendOp(op outboundOp) {
	buf, err := json.Marshal(op)
	if err != nil {
		return
	}
	c.enqueueCtrl(opText, buf)
}

// reauth handles the in-band {op:"reauth", token:"..."} frame.
// Re-resolves token via the hub's TokenStore; on success swaps the
// connection's identity (subject + role) without forcing a reconnect.
// The {op:"reauth_ok"} ack carries the new credential's expires_at when
// it has one, so the client learns the deadline [watchCredentialExpiry]
// will enforce without a REST round trip.
//
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
	ack := outboundOp{Op: "reauth_ok"}
	if !id.ExpiresAt.IsZero() {
		exp := id.ExpiresAt.UTC()
		ack.ExpiresAt = &exp
	}
	c.sendOp(ack)
}

// SetIdentity records the connection's authenticated identity. The
// HTTP upgrade handler calls this once on accept; reauth() updates
// it in-place thereafter.
func (c *client) SetIdentity(id auth.Identity) {
	c.mu.Lock()
	c.identity = id
	c.mu.Unlock()
}

// watchCredentialExpiry closes the connection once the credential behind
// its captured identity stops being valid.
//
// A connection resolves its credential once, at the upgrade, and the command
// router gates every later write on that snapshot. Both credential classes
// carry a server-side expiry — a session's absolute TTL, a bearer token's
// expires_at — that is enforced where the credential is resolved, i.e. on an
// HTTP request. Nothing re-resolves an established socket, so without this
// watch an expired session or token keeps operator/admin command authority
// (paramset writes, alarm disarm) for as long as the client answers pings,
// while every REST call from the same principal already answers 401.
//
// Lifecycle: one goroutine per connection, started by the upgrade handler and
// released when the connection closes. An identity with no deadline is
// re-checked every [pingInterval] rather than watched, so a later in-band
// reauth to an expiring token is picked up too.
func (c *client) watchCredentialExpiry() {
	timer := time.NewTimer(pingInterval)
	defer timer.Stop()
	for {
		wait := pingInterval
		if id := c.Identity(); !id.ExpiresAt.IsZero() {
			if id.Expired(time.Now()) {
				c.logger.Info("ws.credential.expired",
					slog.String("subject", id.Subject),
					slog.String("scheme", string(id.Scheme)))
				c.close()
				return
			}
			wait = time.Until(id.ExpiresAt)
		}
		timer.Reset(wait)
		select {
		case <-c.closed:
			return
		case <-timer.C:
		}
	}
}

// Identity returns the current connection identity (zero-value when
// the upgrade handshake did not resolve one).
func (c *client) Identity() auth.Identity {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.identity
}

// close tears the connection down and takes the client out of the hub's
// fan-out set.
//
// The deregistration is the important half. The handler defers one too,
// but that only runs once readPump returns — and the publisher keeps
// selecting the client as a target until then, re-entering the
// backpressure path for every event in flight. One overflowing session
// produced 413 warnings in two seconds that way.
func (c *client) close() {
	c.once.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
		if c.hub != nil {
			c.hub.deregister(c)
		}
	})
}

// isClosed reports whether the connection has been torn down.
func (c *client) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
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
	// ReleasedOnly, when present on a subscribe frame, drops frames about
	// devices that have not finished onboarding. Pointer for the same
	// reason as Classify: an absent field leaves the preference untouched
	// across re-subscribes.
	ReleasedOnly *bool `json:"released_only,omitempty"`
	// Echo carries back the opaque token the server put on its `ping`
	// frame. Optional: a client that answers the heartbeat without it
	// still keeps the connection alive, it just cannot be timed.
	Echo string `json:"echo,omitempty"`
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

// outboundOp is the simple envelope for ping, pong, subscribe ACKs,
// replay control frames and protocol-level errors. Op-specific fields
// are optional so a single struct covers every wire shape the client sees.
type outboundOp struct {
	Op        string        `json:"op"`
	Seq       uint64        `json:"seq,omitempty"`
	OldestSeq uint64        `json:"oldest_seq,omitempty"`
	Topics    []string      `json:"topics,omitempty"`
	Error     *CommandError `json:"error,omitempty"`
	// Echo is the opaque heartbeat token on a `ping` frame. Clients echo it
	// back on their `pong` so the server can time the round-trip against its
	// own clock alone — no clock agreement between the two ends is required.
	Echo string `json:"echo,omitempty"`
	// RTTMs reports the previous heartbeat's round-trip in milliseconds, so a
	// client can display its own latency to the daemon without measuring
	// anything itself. Absent on the first ping and whenever the last pong
	// carried no echo.
	RTTMs *float64 `json:"rtt_ms,omitempty"`
	// ExpiresAt is the deadline of the credential a `reauth_ok` frame just
	// installed, in UTC. Absent when the new credential has no server-side
	// expiry. It closes the loop the reauth op opens: the connection is
	// closed by [client.watchCredentialExpiry] the moment the deadline
	// passes, so a client that has just refilled its credential needs to
	// know when to do it again — and reading that back over REST would
	// mean a round trip on a surface the client is on precisely to avoid
	// them.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// readPump reads inbound frames, assembles fragmented messages and updates
// subscriptions. Runs until the connection closes or the client misbehaves.
//
// Message assembly is the frame loop's job, not readFrame's: RFC 6455 §5.4
// lets a client split one logical message across a non-final data frame plus
// any number of continuations, and a client library that fragments above some
// size threshold is entitled to do so without negotiating anything. Handling
// only complete text frames meant such a client's `call` failed
// json.Unmarshal on the first half while the continuation matched no case at
// all — the command never ran and the caller waited forever for a `result`
// that could not come.
func (c *client) readPump() {
	defer c.close()
	// fragOpcode is the data opcode of the message currently being
	// assembled, or 0 when none is open. opContinuation is 0x0 and is never
	// stored here, so zero is unambiguous.
	var (
		fragOpcode byte
		fragBuf    []byte
	)
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(readTimeout))
		f, err := readFrame(c.br)
		if err != nil {
			return
		}
		switch f.opcode {
		case opText, opBinary:
			if fragOpcode != 0 {
				// RFC 6455 §5.4: only control frames may be interleaved
				// into a fragmented message.
				c.failConnection(closeProtocolError, "data frame interleaved into a fragmented message")
				return
			}
			if !f.fin {
				fragOpcode = f.opcode
				fragBuf = f.payload
				continue
			}
			if !c.dispatchMessage(f.opcode, f.payload) {
				return
			}
		case opContinuation:
			if fragOpcode == 0 {
				c.failConnection(closeProtocolError, "continuation frame without an open message")
				return
			}
			if len(fragBuf)+len(f.payload) > maxPayload {
				c.failConnection(closeMessageTooBig, "assembled message too large")
				return
			}
			fragBuf = append(fragBuf, f.payload...)
			if !f.fin {
				continue
			}
			opcode, payload := fragOpcode, fragBuf
			fragOpcode, fragBuf = 0, nil
			if !c.dispatchMessage(opcode, payload) {
				return
			}
		case opPing:
			c.enqueueCtrl(opPong, f.payload)
		case opPong:
			// Control-frame heartbeat ack. The documented client heartbeat is
			// the JSON {"op":"pong"} text frame; a peer that answers our ping
			// at the protocol level instead needs nothing done, and saying so
			// keeps it out of the unsupported-opcode branch below.
		case opClose:
			return
		default:
			c.failConnection(closeUnsupportedData, "unsupported opcode")
			return
		}
	}
}

// dispatchMessage handles one fully assembled client message. Reports whether
// the read loop may continue: a message the wire contract does not admit
// fails the connection and returns false.
//
// The wire contract is text JSON (assets/wsapi.json). A binary message is
// rejected with 1003 rather than silently discarded, so a client that sends
// one learns why instead of waiting on a reply that never comes. A text
// message that is not valid JSON, or whose `op` is not one this switch
// knows, gets the same courtesy at the application level: an `{op:"error"}`
// frame rather than silence — a client waiting on the documented ack for
// that frame (e.g. `subscribed`) would otherwise hang indefinitely with no
// signal that its request was never processed.
func (c *client) dispatchMessage(opcode byte, payload []byte) bool {
	if opcode != opText {
		c.failConnection(closeUnsupportedData, "binary messages are not supported")
		return false
	}
	var msg inboundMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		c.logger.Warn("ws.malformed", slog.String("err", err.Error()))
		c.sendOp(outboundOp{Op: "error", Error: NewCommandError(CommandErrorBadRequest, "malformed frame: "+err.Error())})
		return true
	}
	switch msg.Op {
	case "subscribe":
		if msg.Classify != nil {
			c.setClassify(*msg.Classify)
		}
		if msg.ReleasedOnly != nil {
			c.setReleasedOnly(*msg.ReleasedOnly)
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
		c.noteHeartbeat(msg.Echo)
	case "reauth":
		c.reauth(msg.Token)
	case "call":
		c.handleCommand(msg)
	default:
		c.logger.Warn("ws.unknown_op", slog.String("op", msg.Op))
		c.sendOp(outboundOp{Op: "error", Error: NewCommandError(CommandErrorBadRequest, "unknown op: "+msg.Op)})
	}
	return true
}

// failConnection queues a close frame carrying code and reason, then waits
// for the writer goroutine to put it on the wire before the connection is
// dropped (RFC 6455 §7.1.1: the server may close the underlying connection
// once it has sent a Close frame).
//
// The wait is what makes the status code observable at all: writePump
// returns after writing a close frame and its own deferred close tears the
// socket down, which is what closes c.closed. Bounding it keeps a stalled
// peer from pinning the read goroutine.
func (c *client) failConnection(code uint16, reason string) {
	c.logger.Debug("ws.protocol_error",
		slog.Int("code", int(code)),
		slog.String("reason", reason))
	payload := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(payload, code)
	payload = append(payload, reason...)
	c.enqueueCtrl(opClose, payload)
	timer := time.NewTimer(closeFlushTimeout)
	defer timer.Stop()
	select {
	case <-c.closed:
	case <-timer.C:
	}
}

// writePump is the connection's single writer goroutine. It is the only
// code path that touches c.bw / c.conn for writes — every other goroutine
// (readPump, handleCommand, reauth, the hub's broadcast fan-out) hands
// its outbound frame to one of two channels instead of writing directly:
//
//   - c.out — domain events (topic broadcasts), kept as its own channel
//     so [client.enqueue]'s backpressure policy (close on a full 1000-
//     event buffer) stays scoped to the high-frequency broadcast path.
//   - c.ctrl — everything else (subscribe/unsubscribe ACKs, pong
//     replies, reauth results, replay markers, `call` command results),
//     queued via [client.enqueueCtrl] with the same backpressure policy.
//
// Because exactly one goroutine ever calls rawWrite, frames can never
// interleave on the wire and no write mutex is needed. This also means
// readPump never blocks on a slow consumer: previously it wrote
// synchronously (10s deadline) while holding a shared write mutex, so a
// stalled TCP peer could pause the read loop — and therefore ping/pong
// liveness and inbound `call` dispatch — for up to that deadline on every
// frame. Queuing decouples the two: a slow writer now only delays its own
// physical writes, and a truly stuck peer is caught by the backpressure
// close instead of stalling reads.
func (c *client) writePump() {
	defer c.close()
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.closed:
			return
		case ev := <-c.out:
			if ev.Kind == kindReplayDoneMarker {
				// See kindReplayDoneMarker and client.replayFrom: this
				// marker travels through c.out purely for its position in
				// the FIFO order, not as a broadcast — write it as the
				// documented {op:"replay_done"} control frame instead of
				// an outboundEvent.
				buf, err := json.Marshal(outboundOp{Op: "replay_done", Seq: ev.Seq})
				if err == nil {
					if err := c.rawWrite(opText, buf); err != nil {
						return
					}
				}
				c.noteDrained()
				continue
			}
			kind := ev.Kind
			if kind == "" {
				kind = KindChange
			}
			payload := ev.Payload
			// Drop — or reshape — frames touching a device this client
			// asked not to see until it has finished onboarding.
			shaped, keep := c.shapeForThisClient(payload)
			if !keep {
				continue
			}
			payload = shaped
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
			if err := c.rawWrite(opText, buf); err != nil {
				return
			}
			// Queue empty again → the overflow episode (if any) is over
			// and the next one is worth reporting.
			c.noteDrained()
		case msg := <-c.ctrl:
			if err := c.rawWrite(msg.op, msg.payload); err != nil {
				return
			}
			if msg.op == opClose {
				// A close frame is the last thing this connection sends.
				// Returning here runs the deferred close, which is also what
				// releases [client.failConnection] — so the status code is
				// flushed before the socket goes away.
				return
			}
		case <-ticker.C:
			if err := c.rawWrite(opText, c.buildPing()); err != nil {
				return
			}
		}
	}
}

// rawWrite is the single physical write to the connection. Called only
// from writePump — see its doc comment for why that makes a write mutex
// unnecessary.
func (c *client) rawWrite(op byte, payload []byte) error {
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := writeFrame(c.bw, op, payload); err != nil {
		c.logger.Debug("ws.write.failed", slog.String("err", err.Error()))
		return err
	}
	return nil
}

// sendAck confirms a subscribe / unsubscribe operation. The topics
// slice mirrors what the client requested so a reconnect can be
// reasoned about without inspecting daemon-side state. Always answers,
// even for a missing or empty topics list — the wire contract promises
// one ack per subscribe/unsubscribe frame, and a client waiting on it
// before considering itself connected would otherwise hang forever on a
// request the daemon accepted but never confirmed.
func (c *client) sendAck(op string, topics []string) {
	buf, err := json.Marshal(outboundOp{Op: op, Topics: topics})
	if err != nil {
		return
	}
	c.enqueueCtrl(opText, buf)
}

// handleCommand dispatches an inbound `call` frame through the hub's
// router and queues the result for the writer goroutine. A frame without
// an `id` is rejected with a `bad_request` error so callers know to
// supply a correlation id; a frame without a `command` field gets the
// same treatment.
//
// The dispatch uses a short context derived from the connection — the
// caller cannot wait forever for a slow handler. Queuing the response
// (rather than writing it inline) keeps a slow consumer from stalling
// readPump for the write deadline; a physical write failure surfaces
// later, in writePump, which tears the connection down via its own
// `defer c.close`.
func (c *client) handleCommand(msg inboundMessage) {
	resp := outboundResult{Op: "result", ID: msg.ID}
	switch {
	case msg.ID == "":
		resp.Error = NewCommandError(CommandErrorBadRequest, "missing id")
	case msg.Command == "":
		resp.Error = NewCommandError(CommandErrorBadRequest, "missing command")
	case c.Identity().Expired(time.Now()):
		// The identity snapshot outlives the credential it was resolved
		// from. [client.watchCredentialExpiry] closes the connection at the
		// deadline; this closes the window between the deadline and that
		// close, so no command is ever dispatched on an expired credential.
		resp.Error = NewCommandError(CommandErrorUnauthorized, "credential expired — reconnect to re-authenticate")
	default:
		// Carry the connection's authenticated identity into the dispatch
		// context. The command outlives the inbound frame read, so the base
		// is a detached context.Background() rather than the frame's — but it
		// must still carry the identity so the router can enforce per-command
		// role gating and key the rate limiter / user_permissions by subject
		// instead of collapsing every caller to "anonymous".
		base := context.Background()
		if id := c.Identity(); id.Subject != "" {
			base = auth.ContextWithIdentity(base, id)
		}
		ctx, cancel := context.WithTimeout(base, commandTimeout)
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
	c.enqueueCtrl(opText, buf)
}

// commandTimeout caps how long a single command may run. Mirrors the
// rough ceiling Home Assistant applies (10 s) — most CCU operations
// finish in <1 s; anything slower likely warrants async progress
// reporting via a separate event topic instead of blocking the
// connection.
const commandTimeout = 10 * time.Second
