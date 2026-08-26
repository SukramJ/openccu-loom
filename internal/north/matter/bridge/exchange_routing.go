// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"sync"
	"sync/atomic"
	"time"
)

// timedSweepInterval bounds how often the expiry sweep walks
// [exchangeRouting.timedDeadlines]. A TimedRequest timeout is a
// uint16 millisecond value (Matter §8.7), so no deadline can sit more
// than ~66 s in the future and an interval of one minute keeps the
// table proportional to the interactions actually in flight.
const timedSweepInterval = time.Minute

// timedKey is the composite key for [exchangeRouting.timedDeadlines].
// Using a struct{sessionID, exchangeID uint16} instead of a bare
// exchangeID means a different session cannot consume a deadline
// that was registered by another session. Without the session
// dimension a rogue or replaying peer could craft a Write/Invoke
// with a matching exchangeID from a different session and pass the
// timed gate. Mirrors chip CASESession.cpp / chip WriteHandler.cpp
// which always validate against the session context before checking
// the timed gate.
type timedKey struct {
	sessionID  uint16
	exchangeID uint16
}

// exchangeRouting bundles the four routing tables the receive / ack /
// subscribe pipelines use to resolve in-flight exchange state back to
// its owning peer, timing context, or subscription. Each table keys
// on a different shape (exchange, session+exchange, subscription id)
// and is populated / consumed by a different pipeline stage, but all
// four share the same lifecycle: populated when an exchange or
// subscription opens, consumed (and usually removed) when the
// matching event fires or the exchange/subscription closes.
//
// Concurrency: each table is its own sync.Map — callers never need
// cross-table atomicity, so a single shared mutex would only add
// contention no call site benefits from. Embedded as a single
// [Bridge.routing] field so the four tables read as one cohesive unit
// instead of as four same-shaped anonymous fields scattered across
// the Bridge struct.
type exchangeRouting struct {
	// exchangeSrcs maps an inbound exchange (session, exchange) to the
	// *net.UDPAddr (+ peer identity) that opened it. The ack pump uses
	// this to route synthesised StandaloneAck datagrams back to the
	// right peer when the piggyback grace window expires. Populated by
	// [Bridge.owedInboundAck]; consumed by [Bridge.emitStandaloneAck];
	// cleared by [Bridge.dischargeOwedAck].
	//
	// map[mrp.ExchangeKey]exchangeReplyTarget
	exchangeSrcs sync.Map

	// timedDeadlines maps a (sessionID, exchangeID) pair to the
	// wall-clock deadline a TimedRequest established. The follow-up
	// Write / Invoke (req.TimedRequest=true) must arrive before the
	// deadline expires; otherwise the IM dispatcher rejects with
	// TIMEOUT (0x94) per Matter §8.7. Map is concurrency-safe.
	//
	// Reclamation has three paths, because an interaction that is
	// simply abandoned reaches none of the first one: consumption in
	// [Bridge.checkTimedGate], the expiry sweep
	// ([exchangeRouting.maybeSweepExpiredTimedDeadlines], driven from
	// the registration site and from the bridge's ack pump), and the
	// session-teardown drop
	// ([exchangeRouting.dropSessionTimedDeadlines]).
	//
	// map[timedKey]time.Time
	timedDeadlines sync.Map

	// lastTimedSweep is the wall-clock nanosecond stamp of the last
	// expiry sweep over timedDeadlines, so the sweep amortises to at
	// most one full Range per [timedSweepInterval] no matter how many
	// TimedRequests or pump ticks arrive in between. Zero means "never
	// swept", which lets the first caller sweep immediately.
	lastTimedSweep atomic.Int64

	// subTargets maps an active subscription ID to the routing
	// metadata the ongoing-report pump needs to ship a fresh
	// ReportData back to the commissioner: the original UDP src, the
	// peer's SourceNodeID (echoed as DestNodeID per §4.4.1.2), the
	// exchange ID, and the operational session ID. Populated by
	// handleSubscribeRequest after a successful Subscribe; consumed by
	// [Bridge.reportSubscription] from the manager's reporter
	// callback.
	//
	// map[uint32]subTarget
	subTargets sync.Map

	// statusResponseWaits stages a per-exchange "next
	// IM:StatusResponse" rendezvous channel for the Subscribe-Initial
	// chunk-streaming loop. matter.js's
	// `InteractionMessenger.ts:sendDataReportMessage(_, waitForAck=true)`
	// blocks on the peer's IM:StatusResponse between every chunk;
	// Apple's ReadClient (`connectedhomeip/src/app/ReadClient.cpp:541`
	// → `OnMessageReceived`) emits one per inbound ReportData and
	// expects the next chunk only after the round-trip closes. Without
	// this synchronisation openccu-loom burst-fires every chunk and
	// Apple's state machine never advances past
	// `ProcessSubscribeResponse`.
	//
	// Each entry is created in [Bridge.armStatusResponseWait] just
	// before [Bridge.sendReplyReliable], closed exactly once in
	// [Bridge.signalStatusResponseRX] (the IM-dispatcher's
	// StatusResponse branch), and torn down by the caller's
	// [Bridge.disarmStatusResponseWait] on timeout / completion.
	//
	// map[mrp.ExchangeKey]chan struct{}
	statusResponseWaits sync.Map
}

// maybeSweepExpiredTimedDeadlines runs the expiry sweep at most once
// per [timedSweepInterval] and reports how many entries it reclaimed.
// Callers pass the time they already hold, so the sweep costs a single
// atomic load on the common path.
func (r *exchangeRouting) maybeSweepExpiredTimedDeadlines(now time.Time) int {
	last := r.lastTimedSweep.Load()
	nowNanos := now.UnixNano()
	if nowNanos-last < int64(timedSweepInterval) {
		return 0
	}
	if !r.lastTimedSweep.CompareAndSwap(last, nowNanos) {
		// A concurrent caller claimed this sweep slot; one Range is
		// enough.
		return 0
	}
	return r.sweepExpiredTimedDeadlines(now)
}

// sweepExpiredTimedDeadlines deletes every timed deadline that elapsed
// before now and reports how many entries it reclaimed. An elapsed
// deadline is unambiguously garbage: [Bridge.checkTimedGate] rejects
// the follow-up Write / Invoke with TIMEOUT once it passes, so nothing
// downstream can ever consume the entry again. matter.js arms one
// per-exchange timer instead (MessageExchange.ts:1009); a table plus a
// periodic sweep costs one Range a minute rather than one goroutine
// per in-flight timed interaction.
func (r *exchangeRouting) sweepExpiredTimedDeadlines(now time.Time) int {
	reclaimed := 0
	r.timedDeadlines.Range(func(k, v any) bool {
		deadline, ok := v.(time.Time)
		if !ok || now.After(deadline) {
			r.timedDeadlines.Delete(k)
			reclaimed++
		}
		return true
	})
	return reclaimed
}

// dropSessionTimedDeadlines deletes every timed deadline registered by
// sessionID. An exchange cannot outlive its session — matter.js lets
// the exchange's timer die
// with the exchange (MessageExchange.ts:1143 close) and the exchange
// die with the session — so a peer that rotates sessions must not be
// able to leave one entry behind per abandoned interaction.
func (r *exchangeRouting) dropSessionTimedDeadlines(sessionID uint16) {
	r.timedDeadlines.Range(func(k, _ any) bool {
		if key, ok := k.(timedKey); ok && key.sessionID == sessionID {
			r.timedDeadlines.Delete(k)
		}
		return true
	})
}
