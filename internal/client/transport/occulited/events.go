// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The lite-rpc event stream, GET /api/rpc/v1/events: ": connected", then
// a hello frame with an id, then (on a resume) the replay or a resync,
// then live frames "id: <boot>-<seq>" / "event: <type>" / "data: <json>"
// and a ": ping" comment every 15 s. A resync frame has no id; neither
// have the ?devices=1 snapshots.

// DefaultHeartbeatTimeout is the silence after which the lite-rpc
// stream counts as dead: three of the box's 15 s heartbeats.
const DefaultHeartbeatTimeout = 45 * time.Second

// Kind classifies a [Message].
type Kind string

// Message kinds. The stream's own message types keep their wire names.
const (
	// KindOpen: the box accepted the stream (HTTP 200).
	KindOpen Kind = "open"
	// KindComment: a comment line (": connected", ": ping"). Every
	// comment is a sign of life.
	KindComment       Kind = "comment"
	KindHello         Kind = "hello"
	KindEvent         Kind = "event"
	KindState         Kind = "state"
	KindInterface     Kind = "interface"
	KindNewDevices    Kind = "newDevices"
	KindDeleteDevices Kind = "deleteDevices"
	KindUpdateDevice  Kind = "updateDevice"
	KindReplaceDevice Kind = "replaceDevice"
	KindReaddedDevice Kind = "readdedDevice"
	KindResync        Kind = "resync"
	// KindUnknown: a message type this client does not know; Type and
	// Data carry it for forward compatibility.
	KindUnknown Kind = "unknown"
	// KindClosed: the connection ended; Err says why, RetryIn when the
	// next attempt starts.
	KindClosed Kind = "closed"
)

// Message is one item the stream reader delivers. Exactly the payload
// field matching Kind is set.
type Message struct {
	Kind Kind
	// Type is the wire event name; ID the frame id ("" when none).
	Type string
	ID   string
	// Data is the frame's raw JSON.
	Data json.RawMessage
	// Comment is the text of a KindComment ("connected", "ping").
	Comment string

	Hello     *Hello
	Event     *Event
	State     *State
	Interface *InterfaceChange
	Devices   *DeviceChange
	Resync    *Resync

	// Err and RetryIn describe a KindClosed.
	Err     error
	RetryIn time.Duration
}

// Hello is the first message of every connection.
type Hello struct {
	BootID     string           `json:"boot_id"`
	Seq        uint64           `json:"seq"`
	Interfaces []HelloInterface `json:"interfaces"`
	Buffer     HelloBuffer      `json:"buffer"`
}

// HelloInterface is one interface row of the hello. State is up, down or
// silent (the box counts silent as running). Members beyond these are
// in the message's Data.
type HelloInterface struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	State      string `json:"state"`
	Registered bool   `json:"registered"`
}

// HelloBuffer is the replay ring's size.
// loom:reachable:reason="the type of Hello.Buffer, decoded with every hello frame the lite event stream consumes; a method-less struct reached through a field, which the analyzer's type heuristic cannot see used"
type HelloBuffer struct {
	Seconds int `json:"seconds"`
	Events  int `json:"events"`
}

// Event is a datapoint value an interface process reported. Value is the
// JSON value as the stream sent it: the XML-RPC type is lost on the way
// (a double 1.0 arrives as 1, a dateTime or base64 as its raw string),
// so the consumer re-types it from the paramset description.
type Event struct {
	Interface string          `json:"interface"`
	Address   string          `json:"address"`
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	TS        time.Time       `json:"ts"`
	// Batch is the seq of the first event of the interface process's
	// multicall this event came in.
	Batch     uint64 `json:"batch"`
	Confirmed bool   `json:"confirmed"`
}

// State is one of occulited's own sweep readings (source "sweep").
type State struct {
	Interface string          `json:"interface"`
	Address   string          `json:"address"`
	Datapoint string          `json:"datapoint"`
	Value     json.RawMessage `json:"value"`
	TS        time.Time       `json:"ts"`
	Source    string          `json:"source"`
}

// InterfaceChange is an interface process state change: up, down,
// restarted, added or removed.
type InterfaceChange struct {
	Interface string    `json:"interface"`
	State     string    `json:"state"`
	TS        time.Time `json:"ts"`
}

// DeviceChange is a newDevices, deleteDevices, updateDevice,
// replaceDevice or readdedDevice message. The live form carries
// Addresses only (updateDevice one address and no hint, replaceDevice
// [old, new]); the ?devices=1 snapshot form of newDevices carries
// Devices (the interface's listDevices answer, JSON-typed) or Error.
type DeviceChange struct {
	Interface string          `json:"interface"`
	Addresses []string        `json:"addresses"`
	Devices   json.RawMessage `json:"devices"`
	Error     string          `json:"error"`
	TS        time.Time       `json:"ts"`
}

// IsSnapshot reports the ?devices=1 form.
func (d *DeviceChange) IsSnapshot() bool { return d.Devices != nil || d.Error != "" }

// Resync tells the consumer that it missed messages: reason boot (the
// box restarted or the resume id is foreign), gap (the ring no longer
// covers the resume position) or overflow (the stream's queue
// overflowed; the stream ends right after).
type Resync struct {
	Reason string `json:"reason"`
}

// Resync reasons.
const (
	ResyncBoot     = "boot"
	ResyncGap      = "gap"
	ResyncOverflow = "overflow"
)

// EventsOptions configures [Client.Events].
type EventsOptions struct {
	// Interfaces, Types, Addresses and Keys are the stream filters (AND
	// across kinds, OR within one). hello and resync pass every filter.
	Interfaces []string
	Types      []string
	Addresses  []string
	Keys       []string
	// Devices asks for the ?devices=1 snapshots after the hello.
	Devices bool
	// LastEventID is the resume position to start from ("" for none).
	LastEventID string
	// HeartbeatTimeout overrides [DefaultHeartbeatTimeout].
	HeartbeatTimeout time.Duration
	// Backoff is the reconnect policy; zero fields take the defaults.
	Backoff Backoff
	// Buffer is the capacity of the message channel (default 64).
	Buffer int
}

// position is a resume position "<boot>-<seq>".
type position struct {
	raw  string
	boot string
	seq  uint64
	ok   bool
}

func parsePosition(id string) position {
	i := strings.LastIndexByte(id, '-')
	if i <= 0 {
		return position{raw: id}
	}
	seq, err := strconv.ParseUint(id[i+1:], 10, 64)
	if err != nil {
		return position{raw: id}
	}
	return position{raw: id, boot: id[:i], seq: seq, ok: true}
}

// Stream is a running lite-rpc stream reader.
//
// Lifecycle: [Client.Events] starts one goroutine that connects,
// delivers messages on [Stream.Messages] (blocking while the consumer
// does not read) and reconnects with the held resume position after
// every ended connection. It stops when the context passed to Events
// ends; it then closes the message channel and [Stream.Done].
type Stream struct {
	c    *Client
	opts EventsOptions
	msgs chan Message
	done chan struct{}

	mu   sync.Mutex
	pos  position
	dups atomic.Uint64
}

// Events starts reading the lite-rpc event stream. See [Stream] for the
// lifecycle.
func (c *Client) Events(ctx context.Context, opts EventsOptions) *Stream {
	if opts.HeartbeatTimeout <= 0 {
		opts.HeartbeatTimeout = DefaultHeartbeatTimeout
	}
	if opts.Buffer <= 0 {
		opts.Buffer = 64
	}
	s := &Stream{
		c:    c,
		opts: opts,
		msgs: make(chan Message, opts.Buffer),
		done: make(chan struct{}),
		pos:  parsePosition(opts.LastEventID),
	}
	go func() {
		defer close(s.done)
		supervise(ctx, c.logger, "lite-rpc", opts.Backoff, s.msgs, s.connect,
			func(err error, retryIn time.Duration) Message {
				return Message{Kind: KindClosed, Err: err, RetryIn: retryIn}
			})
	}()
	return s
}

// Messages returns the message channel; it is closed when the reader
// stops.
func (s *Stream) Messages() <-chan Message { return s.msgs }

// Done is closed when the reader has stopped.
func (s *Stream) Done() <-chan struct{} { return s.done }

// Position returns the resume position: the id of the last delivered
// message, or "" before any.
func (s *Stream) Position() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pos.raw
}

// Duplicates counts messages dropped because their id was at or before
// the held position of the same boot (a replay overlapping what was
// already delivered).
func (s *Stream) Duplicates() uint64 { return s.dups.Load() }

func (s *Stream) position() position {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pos
}

func (s *Stream) setPosition(p position) {
	s.mu.Lock()
	s.pos = p
	s.mu.Unlock()
}

// query renders the filters.
func (s *Stream) query() url.Values {
	q := url.Values{}
	add := func(key string, vals []string) {
		if len(vals) > 0 {
			q.Set(key, strings.Join(vals, ","))
		}
	}
	add("interface", s.opts.Interfaces)
	add("type", s.opts.Types)
	add("address", s.opts.Addresses)
	add("key", s.opts.Keys)
	if s.opts.Devices {
		q.Set("devices", "1")
	}
	return q
}

// connState is what one connection has seen so far.
type connState struct {
	helloSeen bool
	helloPos  position
}

// pending is a decoded message plus the position to hold once it has
// been delivered.
type pending struct {
	msg   Message
	pos   position
	setPo bool
}

// connect runs one connection (see connectFunc).
func (s *Stream) connect(ctx context.Context, send func(Message) bool) (bool, error) {
	connCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	header := http.Header{}
	if p := s.Position(); p != "" {
		header.Set("Last-Event-ID", p)
	}
	resp, err := s.c.openStream(connCtx, "/api/rpc/v1/events", s.query(), header)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if !send(Message{Kind: KindOpen}) {
		return false, ctx.Err()
	}
	wd := newWatchdog(s.opts.HeartbeatTimeout, func() { cancel(ErrHeartbeatTimeout) })
	defer wd.pause()
	fr := NewFrameReader(stampReader{r: resp.Body, wd: wd})
	var st connState
	for {
		f, err := fr.Next()
		if err != nil {
			return st.helloSeen, endError(connCtx, ctx, err)
		}
		items, err := s.decode(&st, f)
		if err != nil {
			return st.helloSeen, err
		}
		for i := range items {
			it := &items[i]
			// The position moves before the message is handed over, so a
			// reader that asks for it after receiving the message sees
			// that message's id. A send that fails only does so because
			// the stream is ending, and nothing resumes from its position.
			if it.setPo {
				s.setPosition(it.pos)
			}
			wd.pause()
			ok := send(it.msg)
			wd.stamp()
			if !ok {
				return st.helloSeen, ctx.Err()
			}
		}
	}
}

// decode turns a frame into the messages to deliver, enforcing the
// stream rules the resume logic depends on: hello first and with an id,
// no id on a resync, every other id "<boot>-<seq>".
func (s *Stream) decode(st *connState, f Frame) ([]pending, error) {
	out := make([]pending, 0, len(f.Comments)+1)
	for _, c := range f.Comments {
		out = append(out, pending{msg: Message{Kind: KindComment, Comment: c}})
	}
	if f.Event == "" && !f.HasData {
		return out, nil
	}
	typ := f.Event
	if typ == "" {
		typ = "message"
	}
	if !st.helloSeen && typ != string(KindHello) {
		return nil, fmt.Errorf("%w: first message is %q, want hello", ErrProtocol, typ)
	}
	m := Message{Type: typ, ID: f.ID, Data: json.RawMessage(f.Data)}
	switch typ {
	case string(KindHello):
		return s.decodeHello(st, f, m, out)
	case string(KindResync):
		if f.HasID {
			return nil, fmt.Errorf("%w: resync carries id %q", ErrProtocol, f.ID)
		}
		m.Kind = KindResync
		if err := decodeInto(f.Data, &m.Resync, typ); err != nil {
			return nil, err
		}
		p := pending{msg: m}
		if m.Resync.Reason != ResyncOverflow {
			// After boot or gap nothing before this connection's hello
			// can be replayed any more; continue from there. After an
			// overflow the held position stays: the ring may still hold
			// what the queue dropped, and a resume replays it.
			p.pos, p.setPo = st.helloPos, true
		}
		return append(out, p), nil
	}
	if err := decodePayload(typ, f.Data, &m); err != nil {
		return nil, err
	}
	p := pending{msg: m}
	if f.HasID {
		id := parsePosition(f.ID)
		if !id.ok {
			return nil, fmt.Errorf("%w: frame id %q is not <boot>-<seq>", ErrProtocol, f.ID)
		}
		if held := s.position(); held.ok && held.boot == id.boot && id.seq <= held.seq {
			s.dups.Add(1)
			return out, nil
		}
		p.pos, p.setPo = id, true
	}
	return append(out, p), nil
}

// decodeHello handles the hello frame and its effect on the position:
// with no position held, a position of another boot, or one ahead of the
// box, the hello's id becomes the position; otherwise the replay that
// follows advances it.
func (s *Stream) decodeHello(st *connState, f Frame, m Message, out []pending) ([]pending, error) {
	if st.helloSeen {
		return nil, fmt.Errorf("%w: second hello on one connection", ErrProtocol)
	}
	if !f.HasID {
		return nil, fmt.Errorf("%w: hello without id", ErrProtocol)
	}
	id := parsePosition(f.ID)
	if !id.ok {
		return nil, fmt.Errorf("%w: hello id %q is not <boot>-<seq>", ErrProtocol, f.ID)
	}
	m.Kind = KindHello
	if err := decodeInto(f.Data, &m.Hello, string(KindHello)); err != nil {
		return nil, err
	}
	st.helloSeen, st.helloPos = true, id
	p := pending{msg: m}
	if held := s.position(); !held.ok || held.boot != id.boot || held.seq > id.seq {
		p.pos, p.setPo = id, true
	}
	return append(out, p), nil
}

// decodePayload fills the typed payload of a non-hello, non-resync
// message.
func decodePayload(typ, data string, m *Message) error {
	switch Kind(typ) {
	case KindEvent:
		m.Kind = KindEvent
		return decodeInto(data, &m.Event, typ)
	case KindState:
		m.Kind = KindState
		return decodeInto(data, &m.State, typ)
	case KindInterface:
		m.Kind = KindInterface
		return decodeInto(data, &m.Interface, typ)
	case KindNewDevices, KindDeleteDevices, KindUpdateDevice, KindReplaceDevice, KindReaddedDevice:
		m.Kind = Kind(typ)
		return decodeInto(data, &m.Devices, typ)
	default:
		m.Kind = KindUnknown
		return nil
	}
}

// decodeInto decodes a frame's data into a fresh *T. T is the payload
// type of the message kind.
func decodeInto[T any](data string, into **T, typ string) error {
	v := new(T)
	if err := json.Unmarshal([]byte(data), v); err != nil {
		return fmt.Errorf("%w: %s data: %w", ErrProtocol, typ, err)
	}
	*into = v
	return nil
}

// ----------------------------------------------------------------------
// GET /api/rpc/v1/state
// ----------------------------------------------------------------------

// StateQuery filters and pages GET /api/rpc/v1/state.
type StateQuery struct {
	Interface string
	Address   string
	Datapoint string
	// Limit is 1-5000; zero leaves the box's default (1000).
	Limit int
	// After is the next cursor of the previous page.
	After string
}

// StateEntry is one value the box keeps for its chosen datapoint set.
// Entries with Confirmed false or Source "restored" come from disk after
// a restart and must not be acted on.
type StateEntry struct {
	Interface string          `json:"interface"`
	Address   string          `json:"address"`
	Datapoint string          `json:"datapoint"`
	Value     json.RawMessage `json:"value"`
	TS        time.Time       `json:"ts"`
	Confirmed bool            `json:"confirmed"`
	Source    string          `json:"source"`
}

// StatePage is one page of GET /api/rpc/v1/state. EventID was taken
// before the read: resuming the stream from it misses nothing.
type StatePage struct {
	Entries     []StateEntry `json:"entries"`
	Total       int          `json:"total"`
	Unconfirmed int          `json:"unconfirmed"`
	Next        string       `json:"next"`
	EventID     string       `json:"event_id"`
}

// State reads one page of GET /api/rpc/v1/state (scope rpc:read).
func (c *Client) State(ctx context.Context, sq StateQuery) (StatePage, error) {
	q := url.Values{}
	for k, v := range map[string]string{"interface": sq.Interface, "address": sq.Address, "datapoint": sq.Datapoint, "after": sq.After} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if sq.Limit > 0 {
		q.Set("limit", strconv.Itoa(sq.Limit))
	}
	return get[StatePage](ctx, c, "/api/rpc/v1/state", q)
}

// RPCInterface is one row of GET /api/rpc/v1/interfaces.
type RPCInterface struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	URLPath  string `json:"url_path"`
	// Running is false only for an interface process that is down (a
	// silent one counts as running).
	Running bool `json:"running"`
}

// Interfaces reads GET /api/rpc/v1/interfaces (scope rpc:read).
func (c *Client) Interfaces(ctx context.Context) ([]RPCInterface, error) {
	return get[[]RPCInterface](ctx, c, "/api/rpc/v1/interfaces", nil)
}
