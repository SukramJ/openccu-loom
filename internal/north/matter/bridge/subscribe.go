// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	endpointpkg "github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Errors surfaced by the unsolicited-IM send path. They are logged
// and dropped — best-effort delivery is the v1.1 mode.
var (
	// ErrUnsolicitedListenerMissing is returned when the bridge has no
	// UDP listener bound (Stop racing with a tick).
	ErrUnsolicitedListenerMissing = errors.New("bridge: unsolicited send: listener missing")
	// ErrUnsolicitedSessionMissing is returned when the operational
	// session backing a subscription is gone (peer disconnected; the
	// next manager tick reaps the subscription).
	ErrUnsolicitedSessionMissing = errors.New("bridge: unsolicited send: session missing")
	// ErrUnsolicitedEncrypt wraps an AES-CCM seal failure.
	ErrUnsolicitedEncrypt = errors.New("bridge: unsolicited send: encrypt")
)

// perChunkStatusRespTimeout caps how long Subscribe-initial AND
// Read-response chunk loops wait for Apple's IM:StatusResponse on
// each chunk before falling through to the next chunk. matter.js's
// InteractionMessenger acks every chunk on the IM layer; without the
// wait openccu-loom burst-fires all chunks and Apple's MTRDevice
// `ProcessReadResponse` / `ProcessSubscribeResponse` state machines
// drop late chunks. Subscribe requires the same per-chunk wait as the
// Read path.
//
// Matches matter.js InteractionMessenger.ts:742 default
// (`Millis(500)`). A 2-second value left the 10-chunk Subscribe-Initial
// taking ~10s — Apple's HMMTRAccessoryServer fires "Rebuilding HAP
// Services from MTRDevice cache" 50ms after CASESessionSanityCheckPassed
// without waiting for the cache to prime, hits "No enumeration/topology
// dictionary", and releases the fabric after ~15s. With 500ms per chunk
// the full Subscribe-Initial completes in ~2.5s and Apple's cache is
// primed before the HAP rebuild even begins.
const perChunkStatusRespTimeout = 500 * time.Millisecond

// AttachSubscriptionManager wires the subscription manager that
// powers IM Subscribe. When set, inbound SubscribeRequest datagrams
// produce:
//
//  1. Initial ReportData (synchronous, with HasSubscription=true and
//     the freshly-allocated SubscriptionID).
//  2. SubscribeResponse carrying SubscriptionID + MaxInterval.
//
// The manager's engine ticks ongoing reports through the
// [Bridge.SubscriptionReporter] callback the daemon plumbs into
// [subscription.NewManager]; that callback re-reads the cached
// values and ships a fresh ReportData over the original commissioner
// session, looked up via [Bridge.subTargets] (populated here on
// successful Subscribe).
//
// Pass nil to revert to noop (Subscribe replies with empty
// ReportData + a synthetic SubscriptionID=0 SubscribeResponse so
// the message frame still parses on the controller side).
func (b *Bridge) AttachSubscriptionManager(m *subscription.Manager) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subManager = m
	b.wireMeasurementListenersLocked()
}

// subTarget captures the per-subscription routing metadata needed to
// ship ongoing ReportData datagrams. Populated by
// handleSubscribeRequest when Subscribe succeeds; consumed by
// [Bridge.reportSubscription] from the manager's tick.
//
// peerSourceNodeID is the value the commissioner stamped into
// SourceNodeID on the original SubscribeRequest header — the bridge
// echoes it as DestNodeID in every outbound report (Matter §4.4.1.2).
// sessionID is the operational session under which the reports
// travel; SessionID==0 means PASE (unencrypted), >0 means CASE
// (AES-CCM-sealed via [channel.Session]).
type subTarget struct {
	src                 *net.UDPAddr
	hasPeerSourceNodeID bool
	peerSourceNodeID    uint64
	exchangeID          uint16
	sessionID           uint16
	peerInitiator       bool // true if peer opened the exchange (always true for Subscribe inbound)

	// Authorization context captured at Subscribe time, so ongoing
	// reports enforce the SAME ACL + fabric projection as the initial
	// read (Matter §8.5 / §9.10; matter.js ServerSubscription re-applies
	// the read-response authority on every update). Without these the
	// engine tick would ship attribute/event data the subject is no
	// longer (or never was) permitted to see. fabricIndex==0 means PASE.
	fabricIndex    uint8
	subjectNodeID  uint64
	subjectCATs    []uint32
	fabricFiltered bool
}

// SubscriptionReporter returns the [subscription.Reporter] closure the
// daemon plugs into [subscription.NewManager]. The closure looks up
// the per-subscription [subTarget], reads the dirty paths through the
// dispatcher, and ships a fresh ReportData over the same session.
//
// Reliability: when the bridge has an outbound MRP tracker wired
// (the standard daemon path), each ReportData is shipped with
// NeedsAck=true and registered with the tracker — the ACK pump
// retransmits until the peer ACKs or the retransmit cap fires.
// On cap exhaustion the [Bridge.releaseReportCounter] callback reaps
// the dead subscription so the manager stops emitting against a
// vanished peer. Replay after a peer reconnect relies on the
// manager's dirty-flag re-emission rather than a side replay buffer
// — every attribute change re-marks the path dirty, so the next
// healthy tick ships a fresh ReportData.
func (b *Bridge) SubscriptionReporter() subscription.Reporter {
	return b.reportSubscription
}

// SubscriptionEventReporter is the symmetrical hook for ongoing
// EVENT reports. The daemon wires this via
// [subscription.Manager.SetEventReporter] so a cluster-server-fired
// event lands on every subscription whose EventPaths cover the
// (endpoint, cluster, event) triple.
//
// The closure encodes the events under the same per-subscription
// [subTarget] used for attribute reports, so a single peer session
// receives both report types interleaved.
func (b *Bridge) SubscriptionEventReporter() subscription.EventReporter {
	return b.reportSubscriptionEvents
}

// eventReadAuthorizer builds the ACL + fabric-sensitive gate for event reads
// and subscriptions from the requesting session's identity, using dispatcher's
// optional [im.ACLChecker]. A PASE session (fabricIndex==0) or a dispatcher
// without an ACLChecker fails open inside [im.AuthorizeEventReports]. Shared by
// the plain event read (receive_dispatch), the Subscribe-Initial priming report
// (subscribe_dispatch), and the ongoing event fan-out (authorizedEventReports)
// so all three enforce identical event authorization.
func (b *Bridge) eventReadAuthorizer(dispatcher im.Dispatcher, fabricIndex uint8, subjectNodeID uint64, subjectCATs []uint32) im.EventReadAuthorizer {
	checker, _ := dispatcher.(im.ACLChecker)
	return im.EventReadAuthorizer{
		Checker:       checker,
		FabricIndex:   fabricIndex,
		SubjectNodeID: subjectNodeID,
		SubjectCATs:   subjectCATs,
	}
}

// authorizedEventReports filters events down to those the subscribing subject
// (captured on target) is permitted to read AND drops fabric-sensitive records
// owned by another fabric. A PASE session (fabricIndex==0) or a dispatcher
// without an [im.ACLChecker] returns the events unchanged. Each event is gated
// at View — the Matter default event read privilege — except AccessControl
// (0x001F) events, which require Administer AND are dropped when the record's
// FabricIndex differs from the subscribing fabric (Matter §8.4.3.2 /
// §9.10.7.1): path-ACL alone does not stop cross-fabric disclosure of a
// fabric-sensitive record, so the fabric-sensitive drop is applied here too.
// Closes the event half of the subscribe-path ACL bypass.
func (b *Bridge) authorizedEventReports(ctx context.Context, target subTarget, events []im.EventReport) []im.EventReport {
	auth := b.eventReadAuthorizer(b.Dispatcher(), target.fabricIndex, target.subjectNodeID, target.subjectCATs)
	return im.AuthorizeEventReports(ctx, auth, events)
}

// reportSubscriptionEvents assembles + ships an event-only ReportData
// for sub. Called by the manager's engine tick.
func (b *Bridge) reportSubscriptionEvents(ctx context.Context, sub *subscription.Subscription, events []im.EventReport) {
	if sub == nil || len(events) == 0 {
		return
	}
	raw, ok := b.subTargets.Load(sub.ID)
	if !ok {
		return
	}
	target, ok := raw.(subTarget)
	if !ok || target.src == nil {
		return
	}

	// Authorize each event against the subscribing subject: a CASE
	// subject only receives events on (endpoint, cluster) it holds at
	// least View privilege for (Matter §9.10 — events carry a read
	// privilege, default View; AccessControlEntryChanged is Administer).
	// Without this an ACE-less subject would stream every fired event.
	// PASE (fabricIndex==0) bypasses; a dispatcher without an ACLChecker
	// keeps the pre-ACL behaviour.
	authorizedEvents := b.authorizedEventReports(ctx, target, events)
	if len(authorizedEvents) == 0 {
		return
	}

	report := im.ReportData{
		HasSubscription: true,
		SubscriptionID:  sub.ID,
		EventReports:    authorizedEvents,
	}

	body, err := EncodeReportData(report)
	if err != nil {
		debugReplyError(b.logger, "encode_event_report", target.src, err)
		return
	}
	counter, err := b.sendUnsolicitedIM(target, im.OpcodeReportData, body)
	if err != nil {
		b.subTargets.Delete(sub.ID)
		b.logger.Debug("matter.tx.subscribe.event_report",
			slog.Int("subscription_id", int(sub.ID)),
			slog.String("err", err.Error()))
		return
	}
	if counter != 0 {
		b.reportCounterOwner.Store(reportCounterKey(target.sessionID, counter), sub.ID)
	}
	b.logger.Debug("matter.tx.subscribe.event_report",
		slog.Int("subscription_id", int(sub.ID)),
		slog.Int("events", len(events)))
}

// EmitEvent is the bridge-side [interfaces.MatterEventEmitter]
// implementation. Cluster servers (or model-side cluster servers
// that implement [interfaces.MatterEventReceiver]) call this when
// the underlying DP fires an event that should surface to
// subscribers. Non-blocking — fan-out into per-subscription queues
// happens in the manager; the engine tick later drains them.
//
// In addition to the live subscription fan-out, EmitEvent appends
// the event to the bridge's persistent [im.EventLog] so controllers
// that send a ReadRequest with EventRequests (e.g. chip-tool's
// `read-event-by-id`, Apple MTRDevice liveness checks) can retrieve
// historical events — Matter §10.6.6 requires Critical events to be
// buffered and answerable out-of-band from live subscriptions.
//
// `endpoint` is the Matter endpoint ID hosting the cluster
// (assigned by the topology assembler). `cluster` is the cluster
// ID (e.g. 0x003B Switch). `event` is the event ID per the cluster
// spec (e.g. 0x00 InitialPress).
func (b *Bridge) EmitEvent(endpoint uint16, cluster, event uint32, data any, priority interfaces.MatterEventPriority) {
	// Append to the persistent log first (always, even with no active
	// subscriptions) so retrospective Read-Event requests see the event.
	// The log allocates its own monotonic number internally; we read it
	// back to use the same number in the live fan-out so the
	// EventNumber is consistent across both delivery paths.
	rec := im.EventRecord{
		Priority: im.EventPriority(priority),
		Endpoint: endpoint,
		Cluster:  cluster,
		EventID:  event,
		Payload:  data,
	}
	num := b.eventLog.Append(rec)

	m := b.subscriptionManagerLocked()
	if m == nil {
		return
	}
	m.OnEventFired(subscription.EventFiring{
		Path: im.ConcreteEventPath{
			HasEndpoint: true, HasCluster: true, HasEvent: true,
			Endpoint: endpoint, Cluster: cluster, Event: event,
		},
		Number:   num,
		Priority: im.EventPriority(priority),
		Data:     im.AttributeValue{Value: data},
	})
}

// MatterEmitEvent is an alias of [Bridge.EmitEvent] satisfying
// [interfaces.MatterEventEmitter] — the cluster-side hook signature
// the bridge passes to model-side receivers via SetMatterEventEmitter.
func (b *Bridge) MatterEmitEvent(endpoint uint16, cluster, event uint32, data any, priority interfaces.MatterEventPriority) {
	b.EmitEvent(endpoint, cluster, event, data, priority)
}

// readAuthorizedResults runs dispatcher.Read for path and returns only
// the results the requesting subject (carried in ctx via
// [im.WithFabricFilter] / [im.WithSubject]) is authorized to read,
// mirroring the per-result ACL gate in [im.HandleReadRequest]. A PASE
// session (fabricIndex==0) or a dispatcher without an [im.ACLChecker]
// returns every result unchanged.
//
// This closes the subscribe-path ACL bypass: unlike HandleReadRequest,
// the subscription read paths (initial + ongoing) call dispatcher.Read
// directly, so without this gate a View-only or ACE-less subject could
// subscribe to fabric-sensitive attributes (AccessControl.ACL,
// OperationalCredentials.NOCs) and have the engine tick stream them.
// matter.js gates every subscription report through the same read
// authorization (ServerSubscription re-applies the read-response
// authority check). Unauthorized attributes are dropped from the report
// — a subscription simply does not cover paths the subject cannot read.
func (b *Bridge) readAuthorizedResults(ctx context.Context, dispatcher im.Dispatcher, path im.ConcreteAttributePath) []im.ReadResult {
	results := dispatcher.Read(ctx, path)
	_, fabricIndex := im.FabricFilterFromContext(ctx)
	if fabricIndex == 0 {
		return results // PASE / commissioning — ACL not yet applicable
	}
	aclChecker, hasACL := dispatcher.(im.ACLChecker)
	if !hasACL {
		return results
	}
	subjectNodeID, subjectCATs := im.SubjectFromContext(ctx)
	privProvider, hasPriv := dispatcher.(im.AttributeReadPrivilegeProvider)
	authorized := make([]im.ReadResult, 0, len(results))
	for _, rr := range results {
		var priv uint8 = 1 // View default
		if hasPriv {
			priv = privProvider.MinReadPrivilege(rr.Path.Endpoint, rr.Path.Cluster, rr.Path.Attribute)
		}
		if status := aclChecker.CheckACL(ctx, fabricIndex, subjectNodeID, subjectCATs, rr.Path.Endpoint, rr.Path.Cluster, priv); !status.IsSuccess() {
			continue // subject not permitted to read this attribute — drop
		}
		authorized = append(authorized, rr)
	}
	return authorized
}

// reportSubscription assembles + ships a ReportData for sub. Called
// by the manager's engine tick.
func (b *Bridge) reportSubscription(ctx context.Context, sub *subscription.Subscription, paths []im.ConcreteAttributePath) {
	dispatcher := b.Dispatcher()
	if dispatcher == nil || sub == nil {
		return
	}
	raw, ok := b.subTargets.Load(sub.ID)
	if !ok {
		return
	}
	target, ok := raw.(subTarget)
	if !ok || target.src == nil {
		return
	}

	// Authorize + fabric-project every ongoing report against the
	// identity captured at Subscribe time, so a CASE subject cannot
	// keep receiving attributes it is not permitted to read (or another
	// fabric's fabric-scoped rows). Mirrors the subCtx stamping the
	// initial read applies in handleSubscribeRequest.
	readCtx := im.WithFabricFilter(ctx, target.fabricFiltered, target.fabricIndex)
	readCtx = im.WithSubject(readCtx, target.subjectNodeID, target.subjectCATs)

	report := im.ReportData{
		HasSubscription: true,
		SubscriptionID:  sub.ID,
	}
	for _, path := range paths {
		for _, rr := range b.readAuthorizedResults(readCtx, dispatcher, path) {
			rep := im.AttributeReport{Path: rr.Path, DataVersion: rr.DataVersion}
			if rr.Status != im.StatusSuccess {
				rep.IsStatus = true
				rep.Status = im.StatusIB{Status: rr.Status}
			} else {
				rep.Value = rr.Value
			}
			report.Reports = append(report.Reports, rep)
		}
	}

	// An empty report is a max-interval keepalive (nothing changed, or every
	// dirty path was dropped by the ACL gate). matter.js ships empty
	// DataReports with SuppressResponse=true (ServerSubscription.ts:782) so
	// the controller does not owe an IM StatusResponse for a no-op heartbeat.
	if len(report.Reports) == 0 {
		report.SuppressResponse = true
	}

	body, err := EncodeReportData(report)
	if err != nil {
		debugReplyError(b.logger, "encode_ongoing_report", target.src, err)
		return
	}
	counter, freshExch, err := b.sendInitiatedIM(target, body)
	if err != nil {
		// matter.js's ServerSubscription.ts retries an ongoing report
		// up to 2 times before cancelling. Mirror that: per-subscription
		// consecutive failure counter; only on the 3rd consecutive
		// failure do we evict. A transient socket-write hiccup or
		// fabric-reload race no longer reaps an otherwise-healthy
		// subscription, while a peer that truly vanished still gets
		// reaped eventually. The MRP tracker path
		// (closeSubscriptionByCounter) handles the ACK-timeout case;
		// this path covers the immediate-send-error case where no MRP
		// tracker is wired.
		const sendErrorRetryLimit = 2
		var failures int
		if raw, ok := b.subSendErrorCount.Load(sub.ID); ok {
			if n, ok := raw.(int); ok {
				failures = n
			}
		}
		failures++
		if failures <= sendErrorRetryLimit {
			b.subSendErrorCount.Store(sub.ID, failures)
			b.logger.Debug("matter.tx.subscribe.report",
				slog.Int("subscription_id", int(sub.ID)),
				slog.Int("send_failures", failures),
				slog.Int("retry_limit", sendErrorRetryLimit),
				slog.String("err", err.Error()))
			return
		}
		// Cap reached — drop the routing target and evict the
		// subscription from the manager so the engine stops ticking
		// a dead subscription.
		b.subTargets.Delete(sub.ID)
		b.subSendErrorCount.Delete(sub.ID)
		if m := b.subscriptionManagerLocked(); m != nil {
			_ = m.Close(sub.ID) // ErrNotFound is fine — racing with peer or ACK-pump Close
		}
		b.logger.Info("matter.subscribe.peer_unreachable",
			slog.Int("subscription_id", int(sub.ID)),
			slog.Int("send_failures", failures),
			slog.String("hint", "consecutive send-error cap reached; subscription reaped"))
		return
	}
	// Successful send — reset the per-subscription error counter so a
	// later transient hiccup starts at 1 again.
	b.subSendErrorCount.Delete(sub.ID)
	// Record the counter→subID mapping so the ACK pump can close
	// the subscription if the peer never ACKs. Best-effort sends
	// (NeedsAck=false / no tracker) return counter=0; skip in that case.
	if counter != 0 {
		b.reportCounterOwner.Store(reportCounterKey(target.sessionID, counter), sub.ID)
	}
	b.logger.Debug("matter.tx.subscribe.report",
		slog.Int("subscription_id", int(sub.ID)),
		slog.Int("paths", len(paths)),
		slog.Int("reports", len(report.Reports)),
		slog.Int("exchange_id", int(freshExch)))
}

// closeSubscriptionByCounter is called by the ACK pump when an
// outbound report's counter hit the retransmit cap. The subscription
// it belonged to is therefore unreachable; close it in the manager
// (so quotas free up and the engine stops ticking it) and drop the
// target.
func (b *Bridge) closeSubscriptionByCounter(sessionID uint16, counter uint32) {
	raw, ok := b.reportCounterOwner.LoadAndDelete(reportCounterKey(sessionID, counter))
	if !ok {
		return
	}
	subID, ok := raw.(uint32)
	if !ok || subID == 0 {
		return
	}
	b.subTargets.Delete(subID)
	b.subSendErrorCount.Delete(subID)
	if m := b.subscriptionManagerLocked(); m != nil {
		_ = m.Close(subID) // ErrNotFound is fine — racing with peer Close
	}
	if b.logger != nil {
		b.logger.Info("matter.subscribe.peer_unreachable",
			slog.Int("subscription_id", int(subID)),
			slog.String("hint", "max retransmissions reached; subscription reaped"))
	}
}

// releaseReportCounter clears the (session, counter)→subID mapping
// after a successful peer ACK. Caller is the inbound HasAck pipeline,
// which supplies the session from the received message header.
func (b *Bridge) releaseReportCounter(sessionID uint16, counter uint32) {
	if counter == 0 {
		return
	}
	b.reportCounterOwner.Delete(reportCounterKey(sessionID, counter))
}

// reportCounterKey composes (sessionID, counter) into the
// [Bridge.reportCounterOwner] map key. Two concurrent sessions can
// legitimately reuse the same 32-bit MRP counter (each is seeded from
// an independent random per Matter §4.5.4), so the subscription-owner
// lookup must be session-scoped — otherwise session A's ACK could
// release / reap the subscription session B owns.
func reportCounterKey(sessionID uint16, counter uint32) uint64 {
	return uint64(sessionID)<<32 | uint64(counter)
}

// captureSubTarget records the routing metadata for sub on the bridge
// so reportSubscription can find it later. Overwrites any prior entry
// with the same subID — recycled IDs are rare in practice but the
// engine guarantees ordering of Subscribe-then-tick on the same ID.
func (b *Bridge) captureSubTarget(subID uint32, src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader, fabricFiltered bool) {
	if subID == 0 || src == nil || requestHdr == nil {
		return
	}
	// Resolve the requesting fabric + subject the same way the initial
	// read did (handleSubscribeRequest), so ongoing reports authorize
	// against the exact identity that opened the subscription.
	fabricIndex := b.resolveSessionFabric(requestHdr.SessionID)
	subjectNodeID, subjectCATs := b.resolveSessionSubject(requestHdr.SessionID)
	b.subTargets.Store(subID, subTarget{
		src:                 src,
		hasPeerSourceNodeID: requestHdr.HasSourceNodeID,
		peerSourceNodeID:    requestHdr.SourceNodeID,
		exchangeID:          proto.ExchangeID,
		sessionID:           requestHdr.SessionID,
		peerInitiator:       proto.Initiator,
		fabricIndex:         fabricIndex,
		subjectNodeID:       subjectNodeID,
		subjectCATs:         subjectCATs,
		fabricFiltered:      fabricFiltered,
	})
}

// nextOutboundExchangeID allocates a fresh 15-bit exchange identifier
// for a server-initiated exchange. Mirrors matter.js's
// `ExchangeManager.#getNextExchangeId` (packages/protocol/src/
// protocol/ExchangeManager.ts). The high bit (0x8000) is reserved by
// Matter §4.13 as the Initiator flag and is set in the protocol
// header on the wire, not embedded in the exchange ID itself.
//
// Counter wraps via modulo 0x7FFF + 1; the bridge re-uses an ID only
// after the entire 15-bit space has been consumed, which keeps the
// risk of an ID collision with a still-live commissioner exchange
// negligible (the longest-running subscription is bounded by Apple's
// MaxIntervalCeiling ~600 s, far below the wrap time at any realistic
// outbound rate).
func (b *Bridge) nextOutboundExchangeID() uint16 {
	for {
		next := b.outboundExchangeID.Add(1)
		exch := uint16((next & 0x7FFF) & 0xFFFF)
		if exch != 0 {
			return exch
		}
	}
}

// sendInitiatedIM ships an IM datagram on a **fresh bridge-initiated
// exchange** instead of reusing the commissioner's Subscribe exchange.
// Use this for ongoing subscribe reports (matter.js's
// ServerSubscription.#sendUpdateMessage pattern,
// packages/node/src/node/server/ServerSubscription.ts:764). Returns the
// allocated exchange ID so the caller can correlate the
// signalStatusResponseRX call back into the per-subscription state
// machine.
//
// Background: ongoing reports on the original Subscribe exchange are
// MRP-ACKed by Apple at the SecureChannel layer but never elicit the
// IM StatusResponse the spec mandates for non-empty ReportData
// (§10.7.5). Empirically Apple's HMOutlet projection trusts ongoing
// state-change reports only after the StatusResponse handshake
// completes, so reusing the Subscribe exchange leaves external state
// changes invisible in the UI. Opening a fresh exchange triggers the
// full IM handshake on Apple's side. matter.js handles the parallel
// "subscription stays alive across exchanges" expectation via the
// negotiated SubscriptionID — which matches the commissioner's
// expectation regardless of which exchange carries the report.
func (b *Bridge) sendInitiatedIM(target subTarget, payload []byte) (counter uint32, exchangeID uint16, err error) {
	freshExchangeID := b.nextOutboundExchangeID()
	freshTarget := target
	freshTarget.exchangeID = freshExchangeID
	// Set peerInitiator=false so sendUnsolicitedIM's
	// `Initiator: !target.peerInitiator` evaluates to true — we are the
	// exchange initiator on this fresh exchange.
	freshTarget.peerInitiator = false
	counter, err = b.sendUnsolicitedIM(freshTarget, im.OpcodeReportData, payload)
	if err != nil {
		return 0, 0, err
	}
	return counter, freshExchangeID, nil
}

func (b *Bridge) sendUnsolicitedIM(target subTarget, opcode uint8, payload []byte) (uint32, error) {
	b.mu.RLock()
	listener := b.listener
	sessions := b.sessions
	tracker := b.outboundReliable
	b.mu.RUnlock()
	if listener == nil {
		return 0, ErrUnsolicitedListenerMissing
	}

	// sendUnsolicitedIM is the low-level primitive that ships an IM
	// datagram on whichever exchange the caller has prepared in `target`.
	// Two callers, two semantics:
	//   - reportSubscriptionEvents stays on the peer-opened Subscribe
	//     exchange (target.peerInitiator=true → Initiator=false).
	//   - sendInitiatedIM forges a fresh bridge-opened exchange
	//     (target.peerInitiator=false → Initiator=true) for ongoing
	//     attribute reports. See sendInitiatedIM's doc for the Apple
	//     HMOutlet projection background.
	respProto := message.ProtocolHeader{
		Initiator:  !target.peerInitiator, // caller picks via target.peerInitiator
		Opcode:     opcode,
		ExchangeID: target.exchangeID,
		ProtocolID: im.InteractionModelProtocolID,
		// NeedsAck only when the bridge can track the obligation;
		// without a tracker the peer would block on a reply that
		// nothing rebroadcasts on loss.
		NeedsAck: tracker != nil,
	}
	body := append(respProto.Marshal(), payload...) //nolint:gocritic // single-allocation join

	respHdr := message.Header{
		SessionID: target.sessionID,
	}
	if target.hasPeerSourceNodeID {
		respHdr.DestSize = message.DestNodeID
		respHdr.DestNodeID = target.peerSourceNodeID
	}

	var datagram []byte
	var counter uint32
	if target.sessionID == 0 {
		counter = b.nextUnsecuredCounter()
		respHdr.MessageCounter = counter
		datagram = append(respHdr.Marshal(), body...) //nolint:gocritic // single-allocation join
	} else {
		if sessions == nil {
			return 0, ErrUnsolicitedSessionMissing
		}
		sess, ok := sessions.Lookup(target.sessionID)
		if !ok {
			return 0, ErrUnsolicitedSessionMissing
		}
		// Stamp the peer's SessionID so their inbound table resolves;
		// see [Bridge.sendReply] for the parallel rationale.
		if peerID := sess.PeerSessionID(); peerID != 0 {
			respHdr.SessionID = peerID
		}
		enc, err := sess.Encrypt(&respHdr, securityFlagsByte(&respHdr), body)
		if err != nil {
			return 0, fmt.Errorf("%w: %w", ErrUnsolicitedEncrypt, err)
		}
		// Encrypt allocates the message counter from the session's
		// Tx counter — read it back from the encoded header so the
		// retransmit tracker stores the canonical value.
		counter = respHdr.MessageCounter
		datagram = append(respHdr.Marshal(), enc.Ciphertext...)
	}

	if err := listener.Send(target.src, datagram); err != nil {
		return 0, err
	}
	if tracker != nil && respProto.NeedsAck {
		tracker.Track(counter, target.sessionID, target.exchangeID, datagram, target.src, time.Now())
		return counter, nil
	}
	return 0, nil
}

// handleSubscribeRequest is the Subscribe-opcode branch of the IM
// dispatcher. Exposed as a method so the receive-pipeline test can
// drive it without spinning up a real UDP listener.
//
// Orchestrates the Subscribe flow in four steps — each extracted into
// its own method in subscribe_dispatch.go (mirrors the ADR 0031 pattern
// for handleIMOpcode → receive_dispatch.go):
//  1. buildInitialReport — read all requested paths + events, sort.
//  2. registerSubscription — manager Subscribe + KeepSubscriptions teardown.
//  3. streamInitialReportChunks — chunked ReportData send with per-chunk ack wait.
//  4. sendSubscribeResponse — SubscribeResponse with piggyback ack + TouchLastReport.
func (b *Bridge) handleSubscribeRequest(
	ctx context.Context,
	src *net.UDPAddr,
	requestHdr *message.Header,
	proto message.ProtocolHeader,
	req im.SubscribeRequest,
) error {
	dispatcher := b.Dispatcher()
	if dispatcher == nil {
		// Topology not assembled — drop with debug log; commissioner
		// retries via MRP.
		b.logger.Debug("matter.rx.im.subscribe.no_dispatcher",
			slog.String("src", srcString(src)))
		return nil
	}

	// Reject illegal paths up front with a top-level InvalidAction
	// StatusResponse (wildcard cluster + concrete non-global attribute, or
	// wildcard cluster + concrete event) before building any report. Mirrors
	// matter.js InteractionServer.ts validateReadPaths (#3926, Matter
	// §8.4.3.2), which gates Read and Subscribe through the same check.
	if im.ValidateReadPaths(req.AttributeRequests, req.EventRequests) != im.StatusSuccess {
		return b.rejectSubscribeInvalidAction(src, requestHdr, proto, "path")
	}

	// A Subscribe naming no attribute and no event paths at all is
	// InvalidAction — matter.js InteractionServer.ts:628-633 ("No
	// attributes or events requested").
	if len(req.AttributeRequests) == 0 && len(req.EventRequests) == 0 {
		return b.rejectSubscribeInvalidAction(src, requestHdr, proto, "empty")
	}

	// Stamp the FabricFiltered flag + requesting FabricIndex into the
	// context for the initial ReportData pass. Subscribe requests carry
	// FabricFiltered the same way ReadRequests do (Matter §10.6.3);
	// fabric-scoped cluster servers (OperationalCredentials.Fabrics,
	// AccessControl.ACL) must project their lists accordingly.
	// Mirrors matter.js InteractionServer.ts:startReadInteraction →
	// OnlineContext.forFabricFilteredRead.
	subFabricIndex := b.resolveSessionFabric(requestHdr.SessionID)
	subSubjectNodeID, subSubjectCATs := b.resolveSessionSubject(requestHdr.SessionID)
	subCtx := im.WithFabricFilter(ctx, req.FabricFiltered, subFabricIndex)
	subCtx = im.WithSubject(subCtx, subSubjectNodeID, subSubjectCATs)

	initialReport, matchedPaths := b.buildInitialReport(subCtx, dispatcher, req)
	// A Subscribe whose (possibly wildcard) paths match zero attributes
	// AND zero events cannot be established — matter.js
	// ServerSubscription.ts:610-614 rejects it with InvalidAction. The
	// matched count is taken BEFORE DataVersionFilter suppression, so a
	// legitimately quiet re-subscribe (all clusters cached) still
	// establishes.
	if matchedPaths == 0 {
		return b.rejectSubscribeInvalidAction(src, requestHdr, proto, "no_match")
	}
	subID := b.registerSubscription(src, requestHdr, proto, req, &initialReport)
	if err := b.streamInitialReportChunks(src, requestHdr, proto, subID, initialReport); err != nil {
		return err
	}
	return b.sendSubscribeResponse(src, requestHdr, proto, req, subID, initialReport)
}

// rejectSubscribeInvalidAction ships a top-level
// StatusResponse(InvalidAction) for a Subscribe that cannot be
// established (illegal paths, no paths, or zero matching paths) and
// discharges the owed MRP ack. stage tags the debug log line.
func (b *Bridge) rejectSubscribeInvalidAction(src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader, stage string) error {
	body, err := EncodeStatusResponse(im.StatusResponse{Status: im.StatusInvalidAction})
	if err != nil {
		debugReplyError(b.logger, "encode_subscribe_reject_"+stage, src, err)
		return err
	}
	if err := b.sendReply(src, requestHdr, proto, im.OpcodeStatusResponse, body); err != nil {
		debugReplyError(b.logger, "send_subscribe_reject_"+stage, src, err)
		return err
	}
	b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID)
	b.logger.Debug("matter.rx.im.subscribe.reject",
		slog.String("src", srcString(src)),
		slog.String("stage", stage))
	return nil
}

// subscriptionManagerLocked is a small helper to read b.subManager
// under the bridge's RLock. Returns nil when no manager is wired.
func (b *Bridge) subscriptionManagerLocked() *subscription.Manager {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.subManager
}

// wireMeasurementListenersLocked binds every bridged endpoint whose
// Measurement source implements [interfaces.MatterChangeNotifier] to
// the subscription manager's OnAttributeChanged hook. On a value push
// from the CCU the notifier fires, the bridge marks every
// (endpoint, cluster, attribute) path advertised by the cluster
// server's MatterReportable() set dirty, and the manager's next tick
// ships a fresh ReportData via [Bridge.reportSubscription]. Without
// this hop the Subscribe pipeline emits only empty heartbeats and
// Apple Home flags bridged sensors as "not responding" once the
// initial ReportData expires.
//
// Mirrors matter.js's reactor wiring (e.g. ThermostatServer.ts:450's
// `reactTo(...measuredValue$Changed, handler)`) translated to Go:
// the cluster server is a struct, not a behavior with attached
// observables, so the source notifies and the bridge looks up the
// reportable paths via [endpoint.ReportablePaths].
//
// Caller must hold b.mu.Lock. The bridge re-runs this on every
// reassemble (topology may add or remove bridged endpoints) and
// whenever a fresh subscription manager is attached. Old unsubscribe
// closures are drained before new ones are registered so listeners
// never leak across reassembles.
func (b *Bridge) wireMeasurementListenersLocked() {
	for _, unsub := range b.measurementUnsubscribers {
		if unsub != nil {
			unsub()
		}
	}
	b.measurementUnsubscribers = nil

	if b.subManager == nil || b.topology == nil {
		return
	}
	mgr := b.subManager
	var (
		examined, notifierOK, withPaths int
		sourceSeen, measurementSeen     int
	)
	for _, ep := range b.topology.Endpoints {
		if ep == nil || ep.IsRoot() || ep.IsAggregator() {
			continue
		}
		examined++
		if ep.Source != nil {
			sourceSeen++
		}
		if ep.Measurement != nil {
			measurementSeen++
		}
		// Custom-DP-backed endpoints (Source set, e.g. Switch wrapping
		// generic.Switch via custom/switch) carry their notifier on
		// the Source. Measurement-only endpoints (TempSensor /
		// HumiditySensor / Contact / …) carry it on Measurement. Try
		// Source first because custom DPs subsume the measurement
		// surface (e.g. a HmIP-PSM Switch also has POWER / ENERGY
		// sub-sensors attached) and the bridge prefers the richer
		// notifier when both are present.
		var notifier interfaces.MatterChangeNotifier
		var ok bool
		if ep.Source != nil {
			notifier, ok = ep.Source.(interfaces.MatterChangeNotifier)
		}
		if !ok && ep.Measurement != nil {
			notifier, ok = ep.Measurement.(interfaces.MatterChangeNotifier)
		}
		if !ok {
			continue
		}
		notifierOK++
		paths := endpointpkg.ReportablePaths(ep)
		if len(paths) == 0 {
			continue
		}
		// Narrow the dirty-mark set to the paths that BELONG to the
		// firing source's own cluster. The notifier reports "my value
		// changed" — it does not say "every cluster on this endpoint
		// changed". Marking the full reportable-path set dirty makes
		// every STATE flip emit a 5-attribute report (OnOff +
		// Identify.IdentifyTime + Descriptor.PartsList +
		// BridgedDeviceBasicInformation.{Reachable,NodeLabel}) where
		// only OnOff actually moved. Apple Home's HMOutlet treats
		// such bursty same-DataVersion reports as noisy and
		// suppresses the UI refresh. matter.js ships one attribute
		// report per real cluster-attribute change; mirror that.
		pathSet := filterPathsByNotifierCluster(notifier, paths)
		if len(pathSet) == 0 {
			// Defensive — if no path survives the filter the notifier
			// would fire to no effect. Leaving it un-wired keeps the
			// per-endpoint listener count diagnostic honest.
			continue
		}
		withPaths++
		epID := ep.ID
		epRef := ep
		logger := b.logger
		unsub := notifier.OnMatterValueChanged(func() {
			if logger != nil {
				logger.Debug("matter.bridge.measurement.notify",
					slog.Int("endpoint", int(epID)),
					slog.Int("paths", len(pathSet)))
			}
			// Advance the endpoint-hosted DataVersion of every cluster
			// this change touches BEFORE dirty-marking, so the report
			// the manager ships carries the post-change version and
			// controllers' DataVersionFilters miss on the next read.
			// Mirrors matter.js Datasource.ts:949 (increment per change).
			bumped := make(map[uint32]struct{}, 1)
			for _, p := range pathSet {
				if _, done := bumped[p.Cluster]; !done {
					epRef.BumpClusterDataVersion(p.Cluster)
					bumped[p.Cluster] = struct{}{}
				}
			}
			for _, p := range pathSet {
				mgr.OnAttributeChanged(p)
			}
		})
		if unsub == nil {
			unsub = func() {}
		}
		b.measurementUnsubscribers = append(b.measurementUnsubscribers, unsub)
	}
	if b.logger != nil {
		b.logger.Info("matter.bridge.measurement_listeners_wired",
			slog.Int("examined", examined),
			slog.Int("measurement_seen", measurementSeen),
			slog.Int("notifier_ok", notifierOK),
			slog.Int("with_paths", withPaths),
			slog.Int("registered", len(b.measurementUnsubscribers)))
	}
}

// filterPathsByNotifierCluster narrows reportable paths to only those
// whose cluster is owned by the notifier. A source typically also
// implements [interfaces.MatterClusterServer] (e.g. *switchdev.Switch
// is both the endpoint source and the OnOff cluster server), in which
// case its `MatterClusterID()` identifies the cluster whose attributes
// it can actually change. Paths on other clusters of the same
// endpoint (Identify, Descriptor, BridgedDeviceBasicInformation, …)
// are skipped because the notifier-fire signals a value change on the
// source's cluster only.
//
// When the notifier does not also implement
// [interfaces.MatterClusterServer] — the measurement-source path,
// where the source is a sensor-DP and the cluster server is materialised
// separately — we fall back to the full path set. The measurement
// projection has exactly one reportable cluster per endpoint by
// construction (TemperatureMeasurement, RelativeHumidityMeasurement,
// …), so the fallback's wider set is still effectively one cluster's
// attributes plus the static Descriptor/BDBI ones; the historical
// shipping behavior stays unchanged for that path. A future
// per-cluster-attribute refinement can tighten this further if Apple
// turns out to dislike the static-attr noise on sensor endpoints too.
func filterPathsByNotifierCluster(notifier interfaces.MatterChangeNotifier, paths []im.ConcreteAttributePath) []im.ConcreteAttributePath {
	srv, ok := notifier.(interfaces.MatterClusterServer)
	if !ok {
		return append([]im.ConcreteAttributePath(nil), paths...)
	}
	clusterID := srv.MatterClusterID()
	out := make([]im.ConcreteAttributePath, 0, 1)
	for _, p := range paths {
		if p.Cluster == clusterID {
			out = append(out, p)
		}
	}
	return out
}

// resolveSessionFabric maps an active SessionID to its operational
// FabricIndex via the [SessionFabricResolver] optional capability.
// Falls back to 0 (pre-fabric / PASE) when:
//
//   - sessionID == 0 (unsecured channel),
//   - the wired SessionLookup does not implement the resolver,
//   - the resolver reports the session as unknown.
//
// Used by Subscribe to scope per-fabric subscription quotas correctly,
// and by any other fabric-aware path that doesn't have a richer
// session handle around.
func (b *Bridge) resolveSessionFabric(sessionID uint16) uint8 {
	if sessionID == 0 {
		return 0
	}
	b.mu.RLock()
	sessions := b.sessions
	b.mu.RUnlock()
	resolver, ok := sessions.(SessionFabricResolver)
	if !ok {
		return 0
	}
	idx, _ := resolver.FabricFor(sessionID)
	return idx
}

// resolveSessionSubject returns the requesting peer's operational
// NodeID + CASE Authenticated Tags for sessionID, or (0, nil) when
// the lookup does not implement [SessionSubjectResolver] or the
// session is unknown. Used by the receive pipeline to stamp the
// subject into the IM dispatcher's context so [TopologyDispatcher.
// CheckACL] can enforce per-subject ACEs (Matter §9.10.5.6). PASE
// sessions (sessionID==0) return (0, nil) — the ACL gate's
// fabricIndex==0 bypass applies.
func (b *Bridge) resolveSessionSubject(sessionID uint16) (nodeID uint64, cats []uint32) {
	if sessionID == 0 {
		return 0, nil
	}
	b.mu.RLock()
	sessions := b.sessions
	b.mu.RUnlock()
	resolver, ok := sessions.(SessionSubjectResolver)
	if !ok {
		return 0, nil
	}
	nodeID, cats, _ = resolver.SubjectFor(sessionID)
	return nodeID, cats
}
