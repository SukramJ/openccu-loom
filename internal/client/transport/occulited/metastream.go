// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// The metadata change stream, GET /api/meta/v1/events/sse: ": connected",
// then frames that carry only "data: <json>" (no id, no event name), a
// ": ping" comment every 30 s. ?since=<revision> replays the retained
// events after that revision, or answers {"kind":"resync"} when it
// cannot. One mutation may emit several events with the same revision,
// and a slow subscriber loses events silently; detecting that gap from
// the revisions is the consumer's job, this reader only delivers.

// DefaultMetaHeartbeatTimeout is the silence after which the metadata
// stream counts as dead: three of the box's 30 s heartbeats.
const DefaultMetaHeartbeatTimeout = 90 * time.Second

// MetaKind classifies a [MetaMessage].
type MetaKind string

// Metadata stream message kinds.
const (
	MetaOpen    MetaKind = "open"
	MetaComment MetaKind = "comment"
	// MetaChange is a change event (object.*, enum.*, node.*, import).
	MetaChange MetaKind = "change"
	// MetaResync means the replay the reader asked for is not available;
	// the consumer re-reads the snapshot.
	MetaResync MetaKind = "resync"
	MetaClosed MetaKind = "closed"
)

// MetaEvent is one change event. Kind is object.updated, object.deleted,
// enum.created|updated|deleted, node.created|updated|deleted|moved,
// import, or resync.
type MetaEvent struct {
	Revision int             `json:"revision"`
	Kind     string          `json:"kind"`
	Ref      string          `json:"ref,omitempty"`
	Enum     string          `json:"enum,omitempty"`
	Path     string          `json:"path,omitempty"`
	From     string          `json:"from,omitempty"`
	To       string          `json:"to,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Objects  json.RawMessage `json:"objects,omitempty"`
	Enums    json.RawMessage `json:"enums,omitempty"`
}

// MetaMessage is one item the metadata stream reader delivers.
type MetaMessage struct {
	Kind MetaKind
	// Event is set for MetaChange and MetaResync.
	Event *MetaEvent
	// Data is the frame's raw JSON.
	Data    json.RawMessage
	Comment string
	// Err and RetryIn describe a MetaClosed.
	Err     error
	RetryIn time.Duration
}

// MetaStreamOptions configures [Client.MetaEvents].
type MetaStreamOptions struct {
	// Since is the revision to replay after; nil starts at the current
	// revision without a replay.
	Since *int
	// HeartbeatTimeout overrides [DefaultMetaHeartbeatTimeout].
	HeartbeatTimeout time.Duration
	// Backoff is the reconnect policy; zero fields take the defaults.
	Backoff Backoff
	// Buffer is the capacity of the message channel (default 64).
	Buffer int
}

// MetaStream is a running metadata stream reader.
//
// Lifecycle: [Client.MetaEvents] starts one goroutine that connects,
// delivers messages on [MetaStream.Messages] and reconnects after every
// ended connection with ?since= set to the newest revision delivered. It
// stops when the context passed to MetaEvents ends; it then closes the
// message channel and [MetaStream.Done].
type MetaStream struct {
	c    *Client
	opts MetaStreamOptions
	msgs chan MetaMessage
	done chan struct{}

	mu       sync.Mutex
	revision int
	hasRev   bool
}

// MetaEvents starts reading the metadata change stream (scope
// meta:read). See [MetaStream] for the lifecycle.
func (c *Client) MetaEvents(ctx context.Context, opts MetaStreamOptions) *MetaStream {
	if opts.HeartbeatTimeout <= 0 {
		opts.HeartbeatTimeout = DefaultMetaHeartbeatTimeout
	}
	if opts.Buffer <= 0 {
		opts.Buffer = 64
	}
	s := &MetaStream{c: c, opts: opts, msgs: make(chan MetaMessage, opts.Buffer), done: make(chan struct{})}
	if opts.Since != nil {
		s.revision, s.hasRev = *opts.Since, true
	}
	go func() {
		defer close(s.done)
		supervise(ctx, c.logger, "meta", opts.Backoff, s.msgs, s.connect,
			func(err error, retryIn time.Duration) MetaMessage {
				return MetaMessage{Kind: MetaClosed, Err: err, RetryIn: retryIn}
			})
	}()
	return s
}

// Messages returns the message channel; it is closed when the reader
// stops.
func (s *MetaStream) Messages() <-chan MetaMessage { return s.msgs }

// Done is closed when the reader has stopped.
func (s *MetaStream) Done() <-chan struct{} { return s.done }

// Revision returns the newest revision delivered (or the starting one);
// ok is false before any.
func (s *MetaStream) Revision() (rev int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revision, s.hasRev
}

func (s *MetaStream) advance(rev int, resync bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if resync || !s.hasRev || rev > s.revision {
		s.revision, s.hasRev = rev, true
	}
}

// connect runs one connection (see connectFunc). A connection counts as
// live once the box answered 200.
func (s *MetaStream) connect(ctx context.Context, send func(MetaMessage) bool) (bool, error) {
	connCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	q := url.Values{}
	if rev, ok := s.Revision(); ok {
		q.Set("since", strconv.Itoa(rev))
	}
	resp, err := s.c.openStream(connCtx, "/api/meta/v1/events/sse", q, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = resp.Body.Close() }()
	if !send(MetaMessage{Kind: MetaOpen}) {
		return true, ctx.Err()
	}
	wd := newWatchdog(s.opts.HeartbeatTimeout, func() { cancel(ErrHeartbeatTimeout) })
	defer wd.pause()
	fr := NewFrameReader(stampReader{r: resp.Body, wd: wd})
	for {
		f, err := fr.Next()
		if err != nil {
			return true, endError(connCtx, ctx, err)
		}
		msgs, err := decodeMetaFrame(f)
		if err != nil {
			return true, err
		}
		for _, m := range msgs {
			// Advance before delivering: a consumer that reads the event and
			// then asks for Revision must already see it.
			if m.Event != nil {
				s.advance(m.Event.Revision, m.Kind == MetaResync)
			}
			wd.pause()
			ok := send(m)
			wd.stamp()
			if !ok {
				return true, ctx.Err()
			}
		}
	}
}

// decodeMetaFrame turns a frame into messages.
func decodeMetaFrame(f Frame) ([]MetaMessage, error) {
	out := make([]MetaMessage, 0, len(f.Comments)+1)
	for _, c := range f.Comments {
		out = append(out, MetaMessage{Kind: MetaComment, Comment: c})
	}
	if !f.HasData {
		return out, nil
	}
	var ev MetaEvent
	if err := json.Unmarshal([]byte(f.Data), &ev); err != nil {
		return nil, fmt.Errorf("%w: meta event: %w", ErrProtocol, err)
	}
	kind := MetaChange
	if ev.Kind == "resync" {
		kind = MetaResync
	}
	return append(out, MetaMessage{Kind: kind, Event: &ev, Data: json.RawMessage(f.Data)}), nil
}
