// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import "sync"

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
	// TIMEOUT (0x94) per Matter §8.7. Map is concurrency-safe and
	// pruned on consumption / expiry.
	//
	// map[timedKey]time.Time
	timedDeadlines sync.Map

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
