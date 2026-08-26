// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// ackPumpInterval is how often the pump goroutine polls the
// AckTracker for due obligations. Roughly one quarter of
// [mrp.DefaultStandaloneAckDelay] so a max-delayed obligation fires
// no more than ~50 ms past its deadline. Lower bound on the deadline
// is the receive-loop's handler latency (sub-millisecond), so this
// is the operationally-relevant precision.
const ackPumpInterval = 50 * time.Millisecond

// pumpStopGrace is the bounded budget [Bridge.Stop] hands to the ACK
// pump goroutine to unwind once its context is cancelled. The pump
// blocks at most one [ackPumpInterval] inside its select; we add a
// generous safety margin for race-detector overhead. Independent of
// the caller-supplied ctx so a slow serve-loop teardown doesn't
// starve the pump's exit path.
const pumpStopGrace = 500 * time.Millisecond

// AttachAckTracker wires the [mrp.AckTracker] in two roles at once:
//
//  1. The Discharge half (peer ACKs of our outbound messages) goes
//     through [AckHandler] just like before — the adapter is
//     constructed and registered transparently.
//  2. The Owe + Pump half (synthesising StandaloneAck datagrams for
//     un-piggybacked obligations) gets direct access to the tracker.
//     A pump goroutine spawns from [Bridge.Start] when this method
//     has been called; without it the bridge falls back to noop ACK
//     handling (useful for tests that do not need ACK emission).
//
// Calling AttachAckTracker AFTER [Bridge.Start] is allowed, but the
// pump goroutine starts only at the next Start (or never if the
// bridge is already started — the daemon is expected to wire the
// tracker before Start). For tests that wire post-Start, schedule
// pump exercises via direct [Bridge.RunAckPumpOnce] calls instead.
func (b *Bridge) AttachAckTracker(t *mrp.AckTracker) {
	b.AttachAckHandler(NewMRPAckAdapter(t))
	b.mu.Lock()
	b.ackTracker = t
	if b.outboundReliable == nil {
		b.outboundReliable = newOutboundReliableTracker(b.outboundBaseInterval)
	}
	b.mu.Unlock()
}

// outboundBaseInterval resolves the peer-appropriate MRP base
// interval for an outbound retransmission on sessionID. Delegates to
// the session lookup's optional
// [SessionRetransmitIntervalResolver] capability; a lookup without
// the capability, an unknown session, or session 0 (unsecured PASE)
// returns 0 and the tracker falls back to the spec idle default.
func (b *Bridge) outboundBaseInterval(sessionID uint16, now time.Time) time.Duration {
	b.mu.RLock()
	sessions := b.sessions
	b.mu.RUnlock()
	if r, ok := sessions.(SessionRetransmitIntervalResolver); ok {
		if d, ok := r.RetransmitBaseInterval(sessionID, now); ok && d > 0 {
			return d
		}
	}
	return 0
}

// exchangeReplyTarget captures the per-exchange routing state the
// ack pump needs to ship a StandaloneAck back to a peer. Beyond the
// UDP address, we remember the peer's SourceNodeID so the synthesised
// reply can echo it as DestNodeID — chip-tool's commissioner sets
// HasSourceNodeID on inbound PASE traffic and rejects unsecured
// replies that don't echo the value (Matter §4.4.1.2). Same constraint
// as [Bridge.sendReply].
type exchangeReplyTarget struct {
	src                 *net.UDPAddr
	hasPeerSourceNodeID bool
	peerSourceNodeID    uint64
	// sessionID is the SessionID of the inbound message that owed the
	// ACK. 0 means "unsecured" (PASE pre-fabric); non-zero means the
	// synthesised StandaloneAck must encrypt under that session.
	sessionID uint16
}

// owedInboundAck registers an obligation for an inbound Reliable
// message. Caller is the receive pipeline; src plus the peer's
// SourceNodeID (when set) are captured per exchange-id so the pump
// can route the synthesised StandaloneAck back to the right peer with
// a header chip-tool's commissioner accepts.
//
// No-op when the bridge has no tracker wired — the receive pipeline
// then degrades to "no MRP". Peers retransmit until their max-retry
// cap, which is fine for unit tests but burns bandwidth in
// production; daemons should always wire a tracker.
func (b *Bridge) owedInboundAck(src *net.UDPAddr, hdr *message.Header, proto message.ProtocolHeader) {
	b.mu.RLock()
	tracker := b.ackTracker
	b.mu.RUnlock()
	if tracker == nil {
		return
	}
	if src != nil {
		// Keyed on (session, exchange, our role): exchange IDs are only
		// unique per session AND per side, so two controllers sharing an
		// exchange ID — or a peer-opened exchange colliding with a
		// bridge-opened one — must not overwrite each other's reply
		// route. Mirrors matter.js ExchangeManager.ts:287 (session-scoped
		// resolution) and :138 (role folded into the exchange key).
		b.routing.exchangeSrcs.Store(mrp.ExchangeKey{SessionID: hdr.SessionID, ExchangeID: proto.ExchangeID, Initiator: !proto.Initiator}, exchangeReplyTarget{
			src:                 src,
			hasPeerSourceNodeID: hdr.HasSourceNodeID,
			peerSourceNodeID:    hdr.SourceNodeID,
			sessionID:           hdr.SessionID,
		})
	}
	// [mrp.AckObligation.Initiator] records OUR role on the exchange,
	// which is the inverse of the inbound message's flag: a peer-opened
	// exchange (proto.Initiator=true) makes us the responder, and a
	// message arriving with Initiator=false is the peer responding on an
	// exchange we opened — the ongoing-subscription case, where the
	// controller's IM StatusResponse answers a report we initiated. The
	// pump stamps the flag verbatim, and a StandaloneAck that does not
	// invert the peer's flag is discarded as unsolicited (chip
	// src/messaging/ExchangeContext.cpp:384, matter.js
	// packages/protocol/src/protocol/ExchangeManager.ts:319), leaving
	// the peer to retransmit until its cap fires.
	tracker.Owe(hdr.MessageCounter, hdr.SessionID, proto.ExchangeID, !proto.Initiator, time.Now())
}

// refreshAckCounter rewrites requestHdr.MessageCounter to the latest
// pending inbound ack-counter for exchangeID, so a subsequent
// sendReplyReliable's `AckCounter: requestHdr.MessageCounter`
// piggybacks the most-recent peer-sent counter rather than the
// stale original-request counter. Atomically discharges the
// obligation so the ack pump does not also emit a StandaloneAck for
// the same counter — the chunk we are about to send carries it.
//
// Needed on every mid-stream chunk in a multi-chunk ReportData burst
// (Subscribe-Initial AND chunked Read-Response): after the peer
// IM:StatusResponse-acks chunk N, chunk N+1 must piggyback that
// StatusResponse counter, otherwise python-matter-server's
// ReliableMessaging.cpp drops the chunk with "Dropping message
// without piggyback ack when we are waiting for an ack" and the
// commissioning interview times out after 5 retries.
//
// Mirrors the SubscribeResponse fix in subscribe.go's send-response
// branch but generalises it for every chunk in the burst. No-op when
// the tracker is not wired (test paths) or when no obligation exists
// (e.g. before the peer has acked any chunk yet).
func (b *Bridge) refreshAckCounter(requestHdr *message.Header, exchangeID uint16, initiator bool) {
	b.mu.RLock()
	tracker := b.ackTracker
	b.mu.RUnlock()
	if tracker == nil {
		return
	}
	if counter, ok := tracker.LookupAndDischarge(requestHdr.SessionID, exchangeID, initiator); ok {
		requestHdr.MessageCounter = counter
	}
}

// dischargeOwedAck cancels an obligation because the bridge just
// piggybacked an ACK on an outbound reply for the same (session,
// exchange). Caller invokes this from the IM / SC handler paths
// immediately after [Bridge.sendReply] succeeds.
//
// Safe to call when no obligation exists for the pair — the tracker
// silently no-ops.
func (b *Bridge) dischargeOwedAck(sessionID, exchangeID uint16, initiator bool) {
	b.mu.RLock()
	tracker := b.ackTracker
	b.mu.RUnlock()
	if tracker == nil {
		return
	}
	tracker.Discharge(sessionID, exchangeID, initiator)
	b.routing.exchangeSrcs.Delete(mrp.ExchangeKey{SessionID: sessionID, ExchangeID: exchangeID, Initiator: initiator})
}

// expediteDuplicateAck rewrites the just-registered obligation for a
// duplicate to due-now so the caller's immediate pump pass emits the
// StandaloneAck without waiting out the piggyback grace window. No-op
// when no tracker is wired or no obligation exists.
func (b *Bridge) expediteDuplicateAck(sessionID, exchangeID uint16, initiator bool) {
	b.mu.RLock()
	tracker := b.ackTracker
	b.mu.RUnlock()
	if tracker == nil {
		return
	}
	tracker.ExpediteDue(sessionID, exchangeID, initiator)
}

// armStatusResponseWait registers a per-exchange rendezvous channel
// the caller can <-receive on to block until the peer's next
// IM:StatusResponse arrives on this exchange (Matter §8.6.2). Used by
// the Subscribe-Initial chunk-streaming loop in [bridge/subscribe.go]
// to synchronise with Apple's ReadClient between chunks — see
// [exchangeRouting.statusResponseWaits] for the full rationale. The caller
// MUST [Bridge.disarmStatusResponseWait] the same exchange on the
// exit path so a delayed StatusResponse cannot panic on a closed
// channel.
//
// Returns a freshly-allocated channel that is closed exactly once
// by [Bridge.signalStatusResponseRX] when the StatusResponse lands.
// Keyed on (session, exchange) so a StatusResponse from another
// controller sharing the exchange ID cannot release this waiter.
func (b *Bridge) armStatusResponseWait(sessionID, exchangeID uint16, initiator bool) <-chan struct{} {
	ch := make(chan struct{})
	key := mrp.ExchangeKey{SessionID: sessionID, ExchangeID: exchangeID, Initiator: initiator}
	if prev, loaded := b.routing.statusResponseWaits.Swap(key, ch); loaded {
		// Pathological: caller armed a second waiter on the same
		// exchange without disarming the first. Close the orphan
		// channel so a goroutine blocked on it unblocks (interpreting
		// the close as "moved on"); the new wait wins.
		if prevCh, ok := prev.(chan struct{}); ok {
			safeClose(prevCh)
		}
	}
	return ch
}

// disarmStatusResponseWait removes any pending rendezvous channel
// for the (session, exchange) pair. Safe to call when no wait is
// registered — no-op. Always paired with
// [Bridge.armStatusResponseWait] on the caller's exit path (success
// or timeout) so a late StatusResponse arrival cannot panic on a
// closed channel that a goroutine has already abandoned.
func (b *Bridge) disarmStatusResponseWait(sessionID, exchangeID uint16, initiator bool) {
	key := mrp.ExchangeKey{SessionID: sessionID, ExchangeID: exchangeID, Initiator: initiator}
	if v, loaded := b.routing.statusResponseWaits.LoadAndDelete(key); loaded {
		if ch, ok := v.(chan struct{}); ok {
			safeClose(ch)
		}
	}
}

// signalStatusResponseRX is the IM-dispatcher hook into the wait
// machinery: when handleIMOpcode sees an inbound IM:StatusResponse
// for the (session, exchange) pair, it calls this so any goroutine
// inside the chunk-streaming loop unblocks. Safe to call when no
// wait is registered — no-op.
func (b *Bridge) signalStatusResponseRX(sessionID, exchangeID uint16, initiator bool) {
	key := mrp.ExchangeKey{SessionID: sessionID, ExchangeID: exchangeID, Initiator: initiator}
	if v, loaded := b.routing.statusResponseWaits.LoadAndDelete(key); loaded {
		if ch, ok := v.(chan struct{}); ok {
			safeClose(ch)
		}
	}
}

// safeClose closes a channel and recovers from "close of closed
// channel" / "close of nil channel" panics. Belt-and-suspenders for
// the rendezvous channel lifecycle when arm/disarm/signal races.
func safeClose(ch chan struct{}) {
	defer func() { _ = recover() }()
	close(ch)
}

// runAckPump is the goroutine that fires StandaloneAck datagrams for
// obligations that have not been piggybacked within the grace
// window AND retransmits reliable outbound messages whose
// MessageCounter has not been ACKed by the peer. Stops when ctx is
// cancelled.
func (b *Bridge) runAckPump(ctx context.Context) {
	b.mu.RLock()
	tracker := b.ackTracker
	outbound := b.outboundReliable
	b.mu.RUnlock()
	if tracker == nil {
		return
	}
	ticker := time.NewTicker(ackPumpInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, obl := range tracker.Due(now) {
				b.emitStandaloneAck(obl)
			}
			if outbound != nil {
				b.tickOutboundReliable(outbound, now)
			}
			// A peer that abandons its timed interaction and then goes
			// quiet never reaches the registration-site sweep, so the
			// pump owns the idle case. Amortised to one Range per
			// [timedSweepInterval], not one per tick.
			if n := b.routing.maybeSweepExpiredTimedDeadlines(now); n > 0 {
				b.logger.Debug("matter.im.timed.deadlines_reclaimed",
					slog.Int("entries", n))
			}
		}
	}
}

// tickOutboundReliable drives one Tick of the outbound-reliable
// retransmit tracker, sending due datagrams via the bridge's
// listener and logging abandoned entries. Listener races with Stop
// fall through silently — the next tick (if any) re-checks.
//
// When a counter hits [mrp.MaxRetransmissions] the tracker reports
// `mrp.ErrMaxRetransmissionsReached`; the bridge then closes the
// owning subscription so the peer-vanished route stops burning
// engine ticks.
func (b *Bridge) tickOutboundReliable(outbound *outboundReliableTracker, now time.Time) {
	b.mu.RLock()
	listener := b.listener
	b.mu.RUnlock()
	if listener == nil {
		return
	}
	results := outbound.Tick(now, func(dest *net.UDPAddr, datagram []byte) error {
		return listener.Send(dest, datagram)
	})
	for _, r := range results {
		if r.Err == nil {
			continue
		}
		if errors.Is(r.Err, mrp.ErrMaxRetransmissionsReached) {
			b.closeSubscriptionByCounter(r.SessionID, r.Counter)
			continue
		}
		b.logger.Debug("matter.tx.reliable.retransmit",
			slog.Int("counter", int(r.Counter)),
			slog.String("err", r.Err.Error()))
	}
}

// RunAckPumpOnce is the test-side entry point — drains the tracker's
// Due(now) once and emits a StandaloneAck for each, returning the
// number emitted. Production code uses [Bridge.runAckPump] instead.
func (b *Bridge) RunAckPumpOnce(now time.Time) int {
	b.mu.RLock()
	tracker := b.ackTracker
	b.mu.RUnlock()
	if tracker == nil {
		return 0
	}
	due := tracker.Due(now)
	for _, obl := range due {
		b.emitStandaloneAck(obl)
	}
	return len(due)
}

// emitStandaloneAck synthesises a StandaloneAck datagram for obl and
// ships it via the bridge's listener. The src is looked up from the
// per-exchange map (populated by [Bridge.owedInboundAck]); a missing
// src indicates the exchange's src was never recorded, which means
// either the obligation was added out-of-band (tests) or the receive
// pipeline didn't run owedInboundAck — either way we can't route the
// reply, so log and drop.
func (b *Bridge) emitStandaloneAck(obl mrp.AckObligation) {
	raw, ok := b.routing.exchangeSrcs.LoadAndDelete(mrp.ExchangeKey{SessionID: obl.SessionID, ExchangeID: obl.ExchangeID, Initiator: obl.Initiator})
	if !ok {
		b.logger.Debug("matter.tx.ack_pump.no_src",
			slog.Int("exchange_id", int(obl.ExchangeID)),
			slog.Int("session_id", int(obl.SessionID)))
		return
	}
	target, ok := raw.(exchangeReplyTarget)
	if !ok || target.src == nil {
		return
	}

	b.mu.RLock()
	listener := b.listener
	sessions := b.sessions
	b.mu.RUnlock()
	if listener == nil {
		return
	}

	proto := message.ProtocolHeader{
		Initiator:  obl.Initiator,
		HasAck:     true,
		AckCounter: obl.AckCounter,
		Opcode:     mrp.StandaloneAckOpcode,
		ExchangeID: obl.ExchangeID,
		ProtocolID: mrp.SecureChannelProtocolID,
	}
	hdr := message.Header{}
	// Echo the peer's SourceNodeID as our DestNodeID so chip-tool's
	// commissioner accepts the unsecured StandaloneAck (same rule as
	// [Bridge.sendReply], Matter §4.4.1.2). For peers that don't set
	// SourceNodeID the header stays bare. Skip on encrypted sessions —
	// chip-tool's secure-receive validator does not expect it there.
	if target.sessionID == 0 && target.hasPeerSourceNodeID {
		hdr.DestSize = message.DestNodeID
		hdr.DestNodeID = target.peerSourceNodeID
	}

	var datagram []byte
	if target.sessionID == 0 {
		// PASE pre-fabric — counter from the bridge's unsecured pool,
		// no encryption.
		hdr.SessionID = 0
		hdr.MessageCounter = b.nextUnsecuredCounter()
		datagram = append(hdr.Marshal(), proto.Marshal()...)
	} else {
		if sessions == nil {
			b.logger.Debug("matter.tx.ack_pump.no_sessions",
				slog.Int("exchange_id", int(obl.ExchangeID)),
				slog.Int("session_id", int(target.sessionID)))
			return
		}
		sess, ok := sessions.Lookup(target.sessionID)
		if !ok {
			// Session torn down between OWE and emit (rare; usually
			// only on fabric removal). Drop the synthesised ACK — the
			// peer either reaches its retransmit cap or notices the
			// session vanish via its own keepalive.
			b.logger.Debug("matter.tx.ack_pump.session_gone",
				slog.Int("exchange_id", int(obl.ExchangeID)),
				slog.Int("session_id", int(target.sessionID)))
			return
		}
		// Stamp the peer's view of the SessionID so their inbound
		// dispatcher resolves the right session — same rule as
		// [Bridge.sendReply] / [Bridge.sendUnsolicitedIM].
		if peerID := sess.PeerSessionID(); peerID != 0 {
			hdr.SessionID = peerID
		} else {
			hdr.SessionID = target.sessionID
		}
		body := proto.Marshal()
		enc, err := b.encryptSecureOutbound(sess, target.sessionID, &hdr, body)
		if err != nil {
			b.logger.Warn("matter.tx.ack_pump.encrypt",
				slog.Int("exchange_id", int(obl.ExchangeID)),
				slog.Int("session_id", int(target.sessionID)),
				slog.String("err", err.Error()))
			return
		}
		datagram = append(hdr.Marshal(), enc.Ciphertext...)
	}

	if err := listener.Send(target.src, datagram); err != nil {
		b.logger.Warn("matter.tx.ack_pump.send",
			slog.Int("exchange_id", int(obl.ExchangeID)),
			slog.String("src", target.src.String()),
			slog.String("err", err.Error()))
		return
	}
	b.logger.Debug("matter.tx.ack_pump.emitted",
		slog.Int("exchange_id", int(obl.ExchangeID)),
		slog.Int("ack_counter", int(obl.AckCounter)),
		slog.Int("session_id", int(target.sessionID)))
}
