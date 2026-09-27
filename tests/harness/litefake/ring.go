// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Message types on the lite-rpc event stream.
const (
	typeHello         = "hello"
	typeEvent         = "event"
	typeState         = "state"
	typeInterface     = "interface"
	typeNewDevices    = "newDevices"
	typeDeleteDevices = "deleteDevices"
	typeUpdateDevice  = "updateDevice"
	typeReplaceDevice = "replaceDevice"
	typeReaddedDevice = "readdedDevice"
	typeResync        = "resync"
)

// Resync reasons. A resync frame carries no id and passes every filter.
const (
	resyncBoot     = "boot"
	resyncGap      = "gap"
	resyncOverflow = "overflow"
)

// message is one frame of the event stream. seq 0 marks a frame without
// an id (resync and the ?devices=1 snapshots); every ring message has a
// seq above 0.
type message struct {
	seq     uint64
	typ     string
	data    []byte
	at      time.Time
	iface   string
	address string
	key     string
}

// reader is one open event stream. The ring fans every published message
// into queue without blocking; a full queue drops the message and counts
// it, and the stream reports the loss as resync{overflow} at its next
// heartbeat tick.
type reader struct {
	subject  string
	queue    chan message
	dropped  atomic.Uint64
	kill     chan struct{}
	killOnce sync.Once
}

// stop ends the stream the reader belongs to. Safe to call repeatedly.
func (r *reader) stop() {
	r.killOnce.Do(func() { close(r.kill) })
}

// limitError explains why a stream was refused; the text is the message
// of the 429 too-many-streams answer.
type limitError struct{ text string }

func (e *limitError) Error() string { return e.text }

// attachResult is what a new stream needs to write its opening frames:
// the hello position, and either a replay or a resync reason.
type attachResult struct {
	bootID string
	seq    uint64
	replay []message
	resync string
}

// ring is the bounded message history with its boot id, plus the set of
// open readers. One lock covers both, so a stream that attaches sees
// every message exactly once: those published before it attached in its
// replay, the rest in its queue.
type ring struct {
	mu        sync.Mutex
	bootID    string
	last      uint64
	msgs      []message
	maxEvents int
	maxAge    time.Duration
	queueSize int
	perSubj   int
	total     int
	readers   map[*reader]struct{}
}

func newRing(maxEvents int, maxAge time.Duration, queueSize, perSubject, total int) *ring {
	return &ring{
		bootID:    newBootID(),
		maxEvents: maxEvents,
		maxAge:    maxAge,
		queueSize: queueSize,
		perSubj:   perSubject,
		total:     total,
		readers:   make(map[*reader]struct{}),
	}
}

// newBootID returns 16 lowercase hex characters, random per process
// start as on a real box.
func newBootID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail on supported platforms; a fixed id
		// still keeps the fake usable if it ever does.
		return "0000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// publish assigns the next seq, lets build render the data with that seq
// in hand (an event's batch field refers to seqs), stores the message
// and fans it out.
func (g *ring) publish(typ, iface, address, key string, build func(seq uint64) []byte) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.last++
	now := time.Now()
	m := message{
		seq:     g.last,
		typ:     typ,
		data:    build(g.last),
		at:      now,
		iface:   iface,
		address: address,
		key:     key,
	}
	g.msgs = append(g.msgs, m)
	g.pruneLocked(now)
	for rd := range g.readers {
		select {
		case rd.queue <- m:
		default:
			rd.dropped.Add(1)
		}
	}
}

// pruneLocked applies both ring bounds: at most maxEvents messages, none
// older than maxAge.
func (g *ring) pruneLocked(now time.Time) {
	drop := 0
	if over := len(g.msgs) - g.maxEvents; over > 0 {
		drop = over
	}
	cutoff := now.Add(-g.maxAge)
	for drop < len(g.msgs) && g.msgs[drop].at.Before(cutoff) {
		drop++
	}
	if drop > 0 {
		g.msgs = append([]message(nil), g.msgs[drop:]...)
	}
}

// attach registers a reader for subject, enforcing the stream limits,
// and computes the opening frames. lastEventID is the resume position
// the client sent; hasResume is false when it sent none.
func (g *ring) attach(subject, lastEventID string, hasResume bool) (*reader, attachResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.readers) >= g.total {
		return nil, attachResult{}, &limitError{text: "too many streams: " + strconv.Itoa(g.total) + " in total"}
	}
	same := 0
	for rd := range g.readers {
		if rd.subject == subject {
			same++
		}
	}
	if same >= g.perSubj {
		return nil, attachResult{}, &limitError{
			text: "too many streams: " + strconv.Itoa(g.perSubj) + " per token or session",
		}
	}
	rd := &reader{
		subject: subject,
		queue:   make(chan message, g.queueSize),
		kill:    make(chan struct{}),
	}
	g.readers[rd] = struct{}{}
	g.pruneLocked(time.Now())
	res := attachResult{bootID: g.bootID, seq: g.last}
	if hasResume {
		res.replay, res.resync = g.resumeLocked(lastEventID)
	}
	return rd, res, nil
}

// resumeLocked implements the resume rule: an id from another boot, or
// one that does not parse, resyncs with reason boot; a position whose
// successors the ring no longer holds in full resyncs with reason gap;
// otherwise every held message after the position is replayed.
//
// A client already at (or claiming to be past) the newest position gets
// neither a replay nor a resync. The condensed contract phrases the gap
// case against a position called "next"; read as the newest assigned
// seq, its literal rule is exactly this one: no message is ever skipped
// silently, and a client that has seen everything is never resynced.
func (g *ring) resumeLocked(lastEventID string) (replay []message, resync string) {
	boot, seqText, ok := strings.Cut(lastEventID, "-")
	if !ok || boot != g.bootID {
		return nil, resyncBoot
	}
	since, err := strconv.ParseUint(seqText, 10, 64)
	if err != nil {
		return nil, resyncBoot
	}
	if since >= g.last {
		return nil, ""
	}
	if len(g.msgs) == 0 || g.msgs[0].seq > since+1 {
		return nil, resyncGap
	}
	for _, m := range g.msgs {
		if m.seq > since {
			replay = append(replay, m)
		}
	}
	return replay, ""
}

// lookup returns the held message with seq, if the ring still holds it.
func (g *ring) lookup(seq uint64) (message, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, m := range g.msgs {
		if m.seq == seq {
			return m, true
		}
	}
	return message{}, false
}

// detach removes a reader once its stream has ended.
func (g *ring) detach(rd *reader) {
	g.mu.Lock()
	delete(g.readers, rd)
	g.mu.Unlock()
	rd.stop()
}

// position returns the boot id and the newest assigned seq, the value a
// client can pass as Last-Event-ID for a gap-free continuation.
func (g *ring) position() (bootID string, seq uint64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.bootID, g.last
}

// dropStreams ends every open stream.
func (g *ring) dropStreams() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for rd := range g.readers {
		rd.stop()
	}
}

// forceOverflow marks every open stream as having lost a message, so
// each reports resync{overflow} at its next heartbeat tick and ends.
func (g *ring) forceOverflow() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for rd := range g.readers {
		rd.dropped.Add(1)
	}
}

// forceGap discards every held message without touching the seq
// counter, so any resume from a position before the newest one finds
// its successors gone.
func (g *ring) forceGap() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.msgs = nil
}

// restart models a process restart: a new boot id, an empty ring, the
// seq counter back at zero and every stream closed.
func (g *ring) restart() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.bootID = newBootID()
	g.last = 0
	g.msgs = nil
	for rd := range g.readers {
		rd.stop()
	}
}

// openStreams reports how many streams are attached.
func (g *ring) openStreams() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.readers)
}
