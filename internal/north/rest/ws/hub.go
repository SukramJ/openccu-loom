// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SukramJ/openccu-loom/internal/auth"
)

// Event is one item the domain layer hands to [*Hub.Publish]. Topic
// is expected to follow the hierarchical naming convention described
// in the package doc.
//
// Seq is assigned by the hub at Publish time — callers may leave it
// zero. Kind is one of the wire constants ("initial", "change",
// "refresh"); an empty Kind defaults to "change" on the wire so the
// new envelope field is opt-in for emit-site callers that have not
// been updated.
type Event struct {
	Seq     uint64
	Kind    string
	Topic   string
	Type    string
	When    time.Time
	Payload any
}

// Kind values surfaced on the WebSocket envelope. Producers set
// these explicitly when the distinction matters; the default
// (empty -> "change") matches the dominant case (value-change
// broadcast). See ADR 0022.
const (
	KindInitial = "initial"
	KindChange  = "change"
	KindRefresh = "refresh"
)

// defaultReplayCapacity is the in-memory replay buffer size when no
// explicit cap is set via [Hub.SetReplayCapacity]. Sized to roughly
// cover a 30-second burst at 30 events/second — beyond that, clients
// fall through to the snapshot resync path.
const defaultReplayCapacity = 1024

// Hub dispatches domain events to every subscribed client. It is
// the one piece the REST router wires into the domain layer; the
// per-connection plumbing lives in [Handler].
//
// The hub also owns a [*Router] that routes RPC-style `call` frames
// from clients to registered command handlers. Wire-level structure:
//
//	→ {"op":"call", "id":"req-1", "command":"system.health", "args":{}}
//	← {"op":"result", "id":"req-1", "data":{...}}
//	← {"op":"result", "id":"req-1", "error":{"code":"...","message":"..."}}
//
// Mirrors Home Assistant's `async_register_command` pattern that
// Consumes — each command is registered once at
// daemon boot and dispatched per inbound frame.
//
// Sequence + replay (ADR 0022): the hub assigns a monotonic Seq to
// every published Event and retains the most recent
// [defaultReplayCapacity] events in a ring buffer. Clients can
// re-subscribe with a `since` cursor to replay events they missed
// across a reconnect. If `since` precedes the oldest buffered Seq,
// the hub signals `replay_lost` so the client knows it must take a
// fresh [/snapshot] instead of relying on the stream.
type Hub struct {
	mu      sync.RWMutex
	clients map[*client]struct{}
	router  *Router

	seqMu    sync.Mutex
	seqNext  uint64
	replay   []Event
	replayMx int

	// tokens, when non-nil, enables the {op:"reauth"} in-band
	// token-rotation flow on every connected client. See the
	// `Auth lifecycle on long-lived connections` section in
	// docs/external-clients/topic-hierarchy.md.
	tokens auth.TokenStore

	// resyncSignals counts [Hub.SignalResync] calls so a wiring test
	// can assert that a producer actually reaches this seam.
	resyncSignals atomic.Uint64
}

// SignalResync tells every connected client that its view of the model
// may be stale and that it should reload from REST rather than wait for
// the stream to catch up. Reports how many clients were told.
//
// It carries the same `replay_lost` frame a too-old `since` cursor gets:
// both mean "the stream cannot get you to the current state, take a
// snapshot". The boot snapshot uses it in place of the per-data-point
// broadcast it used to emit — on a 1000-device installation that walk
// produced tens of thousands of frames, every one of them delivered to
// every "*" subscriber, which is how a daemon restart used to overrun
// each connected session's queue.
func (h *Hub) SignalResync() int {
	h.resyncSignals.Add(1)
	h.mu.RLock()
	targets := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		if !c.isClosed() {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()

	anchor := h.CurrentSeq()
	for _, c := range targets {
		c.sendOp(outboundOp{Op: "replay_lost", OldestSeq: anchor})
	}
	return len(targets)
}

// ResyncSignals reports how many times [Hub.SignalResync] has been
// called. Producers of the signal are wired far from the hub, so this
// is what lets a test assert the wiring instead of the mechanism.
func (h *Hub) ResyncSignals() uint64 {
	return h.resyncSignals.Load()
}

// SetTokenStore wires the bearer-token resolver the in-band reauth
// op consults. Nil disables reauth (clients sending {op:"reauth"}
// receive {op:"reauth_failed"} and stay connected with their
// original identity).
func (h *Hub) SetTokenStore(t auth.TokenStore) { h.tokens = t }

// TokenStore returns the registered token resolver, or nil.
func (h *Hub) TokenStore() auth.TokenStore { return h.tokens }

// NewHub returns an empty hub with a fresh [*Router].
func NewHub() *Hub {
	return &Hub{
		clients:  make(map[*client]struct{}),
		router:   NewRouter(),
		replay:   make([]Event, 0, defaultReplayCapacity),
		replayMx: defaultReplayCapacity,
	}
}

// Router returns the hub's command router. Daemons populate it at
// boot via [Router.Register] before clients connect.
func (h *Hub) Router() *Router { return h.router }

// SetReplayCapacity reconfigures the replay-buffer ceiling. n <= 0
// disables replay entirely (subscribe-with-since immediately yields
// `replay_lost`). Existing buffered events outside the new cap are
// dropped on the next Publish.
func (h *Hub) SetReplayCapacity(n int) {
	h.seqMu.Lock()
	defer h.seqMu.Unlock()
	if n < 0 {
		n = 0
	}
	h.replayMx = n
	if len(h.replay) > n {
		h.replay = append([]Event(nil), h.replay[len(h.replay)-n:]...)
	}
}

// Publish fans the event out to every client whose subscription
// matches. The caller is free to re-use the payload after return.
// Assigns a monotonic Seq and appends the event to the replay
// buffer for future since-cursor resumes.
func (h *Hub) Publish(ev Event) {
	if ev.Kind == "" {
		ev.Kind = KindChange
	}
	ev = h.stampAndBuffer(ev)

	h.mu.RLock()
	targets := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		// A closed client is not a target. It normally leaves the set in
		// close(), but readPump and writePump both close asynchronously,
		// so a publish can still race the removal.
		if !c.isClosed() && c.matches(ev.Topic) {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()
	for _, c := range targets {
		c.enqueue(ev)
	}
}

// stampAndBuffer assigns the next Seq to ev, stores it in the replay
// ring (truncating the oldest entry when the cap is reached), and
// returns the stamped event so Publish can fan out with the assigned
// Seq.
func (h *Hub) stampAndBuffer(ev Event) Event {
	h.seqMu.Lock()
	defer h.seqMu.Unlock()
	h.seqNext++
	ev.Seq = h.seqNext
	if h.replayMx > 0 {
		if len(h.replay) >= h.replayMx {
			// Drop the oldest entry. The slice retains its
			// capacity; append on the next call reuses the
			// underlying array.
			h.replay = h.replay[1:]
		}
		h.replay = append(h.replay, ev)
	}
	return ev
}

// CurrentSeq reports the highest Seq the hub has ever assigned.
// Used by clients that want to anchor a resume cursor at "the
// current top" instead of "everything since boot".
func (h *Hub) CurrentSeq() uint64 {
	h.seqMu.Lock()
	defer h.seqMu.Unlock()
	return h.seqNext
}

// ReplayResult is what [Hub.Replay] returns. Events carries the
// buffered events whose Seq > since and whose Topic matches
// match(); Lost is true when since precedes the oldest buffered
// Seq — the client then knows it must resync via /snapshot.
type ReplayResult struct {
	Events    []Event
	Lost      bool
	OldestSeq uint64
}

// Replay returns the buffered events the caller missed since the
// supplied cursor. match selects which buffered events the caller is
// actually subscribed to — typically a closure over the client's
// active topic list.
//
// Concurrency: Replay reads the buffer under the seq mutex and
// returns a fresh slice so the caller can iterate without holding
// any hub state.
func (h *Hub) Replay(since uint64, match func(topic string) bool) ReplayResult {
	h.seqMu.Lock()
	defer h.seqMu.Unlock()
	if len(h.replay) == 0 {
		return ReplayResult{}
	}
	oldest := h.replay[0].Seq
	if since < oldest-1 {
		// since references an event that has aged out of the
		// buffer. The client must take a fresh snapshot.
		return ReplayResult{Lost: true, OldestSeq: oldest}
	}
	out := make([]Event, 0, len(h.replay))
	for _, e := range h.replay {
		if e.Seq <= since {
			continue
		}
		if match != nil && !match(e.Topic) {
			continue
		}
		out = append(out, e)
	}
	return ReplayResult{Events: out, OldestSeq: oldest}
}

// ClientCount is the current subscribed connection count.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// MatchCount reports how many active clients would receive an event
// on topic. Used by tests to synchronise with the subscribe handshake.
func (h *Hub) MatchCount(topic string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := 0
	for c := range h.clients {
		if c.matches(topic) {
			n++
		}
	}
	return n
}

func (h *Hub) register(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) deregister(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// matchTopic reports whether topic matches pattern. "*" matches any
// topic; a pattern ending in ".*" matches every topic whose prefix
// (up to the final dot) equals the pattern's prefix.
func matchTopic(pattern, topic string) bool {
	if pattern == "*" || pattern == topic {
		return true
	}
	if before, ok := strings.CutSuffix(pattern, ".*"); ok {
		prefix := before
		return topic == prefix || strings.HasPrefix(topic, prefix+".")
	}
	return false
}
