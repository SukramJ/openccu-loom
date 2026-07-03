// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp

import (
	"sync"
	"time"
)

// Secure Channel protocol constants per Matter Core Spec §4.10
// Table 16 (Secure Channel Protocol Opcodes) + §4.12.7 (Standalone
// ACK timing). These opcodes and the protocol id ride under
// ProtocolHeader.ProtocolID == [SecureChannelProtocolID]; the bridge's
// receive pipeline uses them to fan out to PASE / CASE / MRP-ack
// handlers.
const (
	// SecureChannelProtocolID is the Matter "Secure Channel" protocol
	// identifier under which standalone ACKs, PASE, CASE and status
	// reports travel (Spec §4.4.1.4 Table 4 — Protocol IDs).
	SecureChannelProtocolID uint16 = 0x0000

	// SCOpcodeMsgCounterSyncReq / Rsp — counter-sync (group-only;
	// not used for v1.1 unicast).
	SCOpcodeMsgCounterSyncReq uint8 = 0x00
	SCOpcodeMsgCounterSyncRsp uint8 = 0x01

	// StandaloneAckOpcode is the Secure Channel opcode that carries
	// nothing but an acknowledgement.
	StandaloneAckOpcode uint8 = 0x10

	// PBKDF param + PASE Spake2+ exchange opcodes (Spec §4.13).
	SCOpcodePBKDFParamRequest  uint8 = 0x20
	SCOpcodePBKDFParamResponse uint8 = 0x21
	SCOpcodePake1              uint8 = 0x22
	SCOpcodePake2              uint8 = 0x23
	SCOpcodePake3              uint8 = 0x24
	SCOpcodePakeFinished       uint8 = 0x25

	// CASE Sigma exchange opcodes (Spec §4.14).
	SCOpcodeSigma1       uint8 = 0x30
	SCOpcodeSigma2       uint8 = 0x31
	SCOpcodeSigma3       uint8 = 0x32
	SCOpcodeSigma2Resume uint8 = 0x33

	// StatusReport carries protocol-level errors / warnings outside
	// the IM status surface (Spec §4.10.4).
	SCOpcodeStatusReport uint8 = 0x40

	// DefaultStandaloneAckDelay is the typical piggyback grace window
	// before a sender must emit a standalone ACK. Matter §4.12.7 leaves
	// the exact delay implementation-defined but recommends ~200 ms so
	// short interactive command bursts can ride a single packet. Drop
	// to 0 in tests for deterministic timing.
	DefaultStandaloneAckDelay = 200 * time.Millisecond
)

// AckObligation captures the bookkeeping for one outstanding ACK that
// the local side owes a peer. The caller (typically the message
// dispatcher above MRP) drains due obligations via [AckTracker.Due]
// and builds a Secure-Channel StandaloneAck per Matter §4.12.7:
//
//   - ProtocolHeader.ProtocolID = [SecureChannelProtocolID]
//   - ProtocolHeader.Opcode     = [StandaloneAckOpcode]
//   - ProtocolHeader.HasAck     = true
//   - ProtocolHeader.AckCounter = AckCounter
//   - ProtocolHeader.ExchangeID = ExchangeID
//   - ProtocolHeader.Initiator  = !Initiator (peer's flag inverted)
//   - ProtocolHeader.NeedsAck   = false (ACKs are never themselves Reliable)
//
// MRP itself stays protocol-shape-free; building the message bytes is
// the caller's job.
type AckObligation struct {
	// AckCounter is the message counter we must acknowledge.
	AckCounter uint32
	// SessionID is the session the original Reliable message arrived
	// on. Exchange IDs are only unique per session (peers pick them
	// independently), so the obligation carries both halves of the key.
	SessionID uint16
	// ExchangeID is the exchange the original Reliable message
	// belonged to. The standalone ACK rides the same exchange so the
	// peer's MRP layer correlates it with the in-flight message.
	ExchangeID uint16
	// Initiator is true when the local side opened the exchange. The
	// emitted standalone ACK uses the negation: a responder ACKs an
	// initiator-flagged peer message and vice versa.
	Initiator bool
	// DueAt is the wall-clock time after which the obligation MUST be
	// emitted as a standalone ACK if it hasn't been piggybacked yet.
	DueAt time.Time
}

// ExchangeKey identifies an exchange scoped to its session. Exchange
// IDs are picked independently by every peer, so two concurrent
// controllers (or an old and a new CASE session of the same
// controller) can carry the same 16-bit exchange ID — matter.js
// treats an exchange whose session no longer matches as a different
// exchange (ExchangeManager.ts:287 invalidates the lookup when
// `exchange.session.id !== session.id`).
type ExchangeKey struct {
	SessionID  uint16
	ExchangeID uint16
}

// AckTracker bookkeeps outstanding ACK obligations across exchanges.
// It is concurrency-safe and I/O-free; the message dispatcher pumps it
// via [Owe] / [Discharge] / [Due]. One obligation per (session,
// exchange) is retained at any time — Matter §4.12.4.2 lets the
// implementation collapse cumulative ACKs (a single ACK for the
// latest counter implicitly covers earlier counters in the same
// exchange).
type AckTracker struct {
	mu      sync.Mutex
	pending map[ExchangeKey]AckObligation
	delay   time.Duration
}

// NewAckTracker returns a tracker with the given piggyback grace
// delay. Pass 0 in tests for deterministic "every Owe is immediately
// due" semantics; pass [DefaultStandaloneAckDelay] in production.
func NewAckTracker(delay time.Duration) *AckTracker {
	if delay < 0 {
		delay = 0
	}
	return &AckTracker{
		pending: make(map[ExchangeKey]AckObligation),
		delay:   delay,
	}
}

// Owe records an obligation to ACK ackCounter on the (session,
// exchange) pair. If a pending obligation already exists for the
// pair, it is replaced with the higher counter — only the latest ACK
// needs to ride the wire (Matter §4.12.4.2 cumulative-ACK semantics).
func (t *AckTracker) Owe(ackCounter uint32, sessionID, exchangeID uint16, initiator bool, now time.Time) {
	key := ExchangeKey{SessionID: sessionID, ExchangeID: exchangeID}
	t.mu.Lock()
	defer t.mu.Unlock()
	prev, ok := t.pending[key]
	if ok && counterGE(prev.AckCounter, ackCounter) {
		// Earlier obligation already covers ackCounter; keep the older
		// DueAt so we don't continually slide the deadline forward.
		return
	}
	t.pending[key] = AckObligation{
		AckCounter: ackCounter,
		SessionID:  sessionID,
		ExchangeID: exchangeID,
		Initiator:  initiator,
		DueAt:      now.Add(t.delay),
	}
}

// Discharge removes the pending obligation for the (session,
// exchange) pair. Returns true when an obligation existed (so the
// caller can decide whether to skip a redundant standalone-ACK
// emission). Called by the dispatcher when an outbound message
// piggybacks the ACK or when the exchange is torn down.
func (t *AckTracker) Discharge(sessionID, exchangeID uint16) bool {
	key := ExchangeKey{SessionID: sessionID, ExchangeID: exchangeID}
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.pending[key]
	if !ok {
		return false
	}
	delete(t.pending, key)
	return true
}

// LookupAndDischarge returns the latest pending ack-counter for the
// (session, exchange) pair and clears the obligation in one atomic
// step. Returns (counter, true) when an obligation existed;
// (0, false) otherwise.
//
// Used by reply-construction paths that want to piggyback the most
// recent inbound counter — not just the trigger message's. The
// Subscribe-Initial flow registers an obligation on the
// SubscribeRequest counter, but chip-tool's commissioner then sends
// an IM:StatusResponse acking the initial-report mid-flow; the
// follow-up SubscribeResponse SHOULD piggyback the StatusResponse
// counter rather than the stale SubscribeRequest counter, otherwise
// chip-tool's ReliableMessaging drops the reply with
// `Dropping message without piggyback ack when we are waiting for
// an ack` and the subscription times out.
func (t *AckTracker) LookupAndDischarge(sessionID, exchangeID uint16) (uint32, bool) {
	key := ExchangeKey{SessionID: sessionID, ExchangeID: exchangeID}
	t.mu.Lock()
	defer t.mu.Unlock()
	obl, ok := t.pending[key]
	if !ok {
		return 0, false
	}
	delete(t.pending, key)
	return obl.AckCounter, true
}

// ExpediteDue makes the pending obligation for the (session, exchange)
// pair immediately due, so the next pump pass emits its StandaloneAck
// without waiting out the piggyback grace window. Used on authentic
// duplicates: the peer is retransmitting precisely because it never
// saw an ack, so delaying the fresh ack by the grace window invites
// further retransmits. Mirrors matter.js MessageExchange.ts:428-433
// (duplicate + requiresAck → sendStandaloneAckForMessage immediately).
// Returns whether an obligation existed.
func (t *AckTracker) ExpediteDue(sessionID, exchangeID uint16) bool {
	key := ExchangeKey{SessionID: sessionID, ExchangeID: exchangeID}
	t.mu.Lock()
	defer t.mu.Unlock()
	obl, ok := t.pending[key]
	if !ok {
		return false
	}
	obl.DueAt = time.Time{}
	t.pending[key] = obl
	return true
}

// Pending reports the count of in-flight obligations.
func (t *AckTracker) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

// Due returns every obligation whose DueAt has elapsed and removes
// them from the tracker. The caller is expected to emit a standalone
// ACK for each (see [AckObligation] doc for the message shape). The
// order of the returned slice is unspecified — callers that need
// stability sort by ExchangeID themselves.
func (t *AckTracker) Due(now time.Time) []AckObligation {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.pending) == 0 {
		return nil
	}
	var out []AckObligation
	for id, obl := range t.pending {
		if now.Before(obl.DueAt) {
			continue
		}
		out = append(out, obl)
		delete(t.pending, id)
	}
	return out
}

// counterGE reports whether a is greater-or-equal to b under Matter's
// 32-bit message counter monotonicity. This is the same wrap-aware
// comparison the duplicate-detection [Window] uses; counters wrap at
// 2^32 but the half-range distance keeps the comparison total.
func counterGE(a, b uint32) bool {
	return a == b || (a-b) < (1<<31)
}
