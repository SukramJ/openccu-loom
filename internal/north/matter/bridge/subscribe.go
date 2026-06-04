// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sort"
	"time"

	endpointpkg "github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
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

// reportSubscriptionEvents assembles + ships an event-only ReportData
// for sub. Called by the manager's engine tick.
func (b *Bridge) reportSubscriptionEvents(_ context.Context, sub *subscription.Subscription, events []im.EventReport) {
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

	report := im.ReportData{
		HasSubscription: true,
		SubscriptionID:  sub.ID,
		EventReports:    append([]im.EventReport(nil), events...),
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
		b.reportCounterOwner.Store(counter, sub.ID)
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

	report := im.ReportData{
		HasSubscription: true,
		SubscriptionID:  sub.ID,
	}
	for _, path := range paths {
		for _, rr := range dispatcher.Read(ctx, path) {
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
		b.reportCounterOwner.Store(counter, sub.ID)
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
func (b *Bridge) closeSubscriptionByCounter(counter uint32) {
	raw, ok := b.reportCounterOwner.LoadAndDelete(counter)
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

// releaseReportCounter clears the counter→subID mapping after a
// successful peer ACK. Caller is the inbound HasAck pipeline.
func (b *Bridge) releaseReportCounter(counter uint32) {
	if counter == 0 {
		return
	}
	b.reportCounterOwner.Delete(counter)
}

// captureSubTarget records the routing metadata for sub on the bridge
// so reportSubscription can find it later. Overwrites any prior entry
// with the same subID — recycled IDs are rare in practice but the
// engine guarantees ordering of Subscribe-then-tick on the same ID.
func (b *Bridge) captureSubTarget(subID uint32, src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader) {
	if subID == 0 || src == nil || requestHdr == nil {
		return
	}
	b.subTargets.Store(subID, subTarget{
		src:                 src,
		hasPeerSourceNodeID: requestHdr.HasSourceNodeID,
		peerSourceNodeID:    requestHdr.SourceNodeID,
		exchangeID:          proto.ExchangeID,
		sessionID:           requestHdr.SessionID,
		peerInitiator:       proto.Initiator,
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
		exch := uint16(next & 0x7FFF) //nolint:gosec // masked to 15 bits
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
		tracker.Track(counter, target.exchangeID, datagram, target.src, time.Now())
		return counter, nil
	}
	return 0, nil
}

// handleSubscribeRequest is the Subscribe-opcode branch of the IM
// dispatcher. Exposed as a method so the receive-pipeline test can
// drive it without spinning up a real UDP listener.
func (b *Bridge) handleSubscribeRequest( //nolint:gocognit,gocyclo,funlen // wire/dispatch table over many attribute/opcode cases
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

	// Build the initial ReportData by running each requested path
	// through the dispatcher and collecting the results into one
	// AttributeReport list. Mirrors HandleReadRequest at the path
	// level but lets the caller stamp HasSubscription/SubscriptionID
	// on the output.
	initialReport := im.ReportData{
		HasSubscription: false, // overwritten below when manager wires the subscription
		Reports:         nil,
	}
	for _, path := range req.AttributeRequests {
		for _, rr := range dispatcher.Read(subCtx, path) {
			// DataVersionFilter evaluation: skip attributes whose cluster
			// DataVersion matches the controller's cached version.
			// Matter §10.6.5 — the controller infers "no change since
			// cached" from the absence of reports for that cluster.
			// Mirrors matter.js InteractionServer.ts:startReadInteraction
			// DataVersionFilter evaluation.
			//
			// Sentinel guard: clusters without per-instance tracking
			// return rr.DataVersion=0 and the wire encoder substitutes
			// the §10.6.1.4 floor of 1. A controller that cached the
			// sentinel-1 and replays it on re-pair must NOT cause the
			// entire cluster to be omitted from the Subscribe-Initial —
			// the bridged-endpoint clusters would silently disappear
			// from Apple's MTRDevice ClusterStateCache and
			// `endpointDeviceTypes` would collapse to `{0=(22)}`.
			// Guard at >1 keeps the sentinel from ever matching.
			if len(req.DataVersionFilters) > 0 && rr.Status == im.StatusSuccess {
				if cached, ok := im.MatchDataVersionFilter(req.DataVersionFilters, rr.Path.Endpoint, rr.Path.Cluster); ok {
					if cached == rr.DataVersion && rr.DataVersion > 1 {
						continue
					}
				}
			}
			rep := im.AttributeReport{Path: rr.Path, DataVersion: rr.DataVersion}
			if rr.Status != im.StatusSuccess {
				rep.IsStatus = true
				rep.Status = im.StatusIB{Status: rr.Status}
			} else {
				rep.Value = rr.Value
			}
			initialReport.Reports = append(initialReport.Reports, rep)
		}
	}

	// EventRequests handling — matter.js's `InteractionServer.ts:
	// startReadInteraction` (mirrored here for Subscribe) walks
	// req.EventRequests and emits any cached events that match. Apple
	// Home's MTRDevice (`MTRDevice_Concrete`) transitions from
	// `Subscribing` to `InitialSubscriptionEstablished` only AFTER
	// the report-end frame; an Apple Subscribe with `events: *.*.*`
	// that comes back attributes-only leaves the controller's state
	// machine permanently stuck in `Subscribing`, which surfaces as
	// `Storing cluster information count: 3` and the bridge as
	// "added but not supported". matter.js Sample byte-dump
	// emitted 0+ EventReports per Subscribe-Initial and
	// Apple flipped to count: 21 + InitialSubscriptionEstablished.
	if len(req.EventRequests) > 0 {
		evs := im.BuildEventReports(req.EventRequests, b.eventLog, req.EventFilters)
		initialReport.EventReports = evs
	}
	// Sort reports by (endpoint, cluster, attribute) ascending. Apple
	// Home's MTRDevice processes the wildcard Subscribe-Initial in
	// stream order and the HAP-Service-Mapper's topology builder needs
	// Descriptor (0x001D) cached BEFORE the cluster surfaces it
	// references — otherwise `_attributeValueDictionaryForAttributePath`
	// reports "PartsList absent from cache" and HAP-Build aborts with
	// HAPErrorDomain Code=14 ~5s after Subscribe-Initial. matter.js
	// emits its reports in the same ascending order
	// (`packages/protocol/src/interaction/InteractionServer.ts:
	// generateAttributeListReport` walks endpoints sorted then
	// clusters sorted then attributes sorted).
	sort.Slice(initialReport.Reports, func(i, j int) bool {
		a, b := initialReport.Reports[i].Path, initialReport.Reports[j].Path
		if a.Endpoint != b.Endpoint {
			return a.Endpoint < b.Endpoint
		}
		if a.Cluster != b.Cluster {
			return a.Cluster < b.Cluster
		}
		return a.Attribute < b.Attribute
	})
	// Diagnostic: log each AttributeReport's (endpoint, cluster,
	// attribute, value_type) so a failing pair (Apple's IM-decoder
	// silently dropping a chunk) can be traced to a single attribute
	// without re-hexing the wire bytes. Apple's `<private>` log mask
	// hides the offending path on its side; this emit is the only
	// bridge-side window into what the Subscribe-Initial actually
	// carried for a given cluster.
	for idx, r := range initialReport.Reports {
		valueType := "<status>"
		var valuePreview any
		if !r.IsStatus {
			valueType = fmt.Sprintf("%T", r.Value.Value)
			valuePreview = r.Value.Value
		}
		b.logger.Debug("matter.tx.subscribe.initial_report",
			slog.Int("idx", idx),
			slog.Any("endpoint", r.Path.Endpoint),
			slog.Any("cluster", r.Path.Cluster),
			slog.Any("attribute", r.Path.Attribute),
			slog.Bool("status", r.IsStatus),
			slog.Any("status_code", r.Status.Status),
			slog.String("value_type", valueType),
			slog.Any("value", valuePreview))
	}

	// Subscribe in the manager so quotas + cadence math happen even
	// when the report-pump is not yet wired.
	var subID uint32
	if m := b.subscriptionManagerLocked(); m != nil {
		fabricIndex := b.resolveSessionFabric(requestHdr.SessionID)
		// KeepSubscriptions=false teardown — Matter §10.6.5: when the
		// commissioner clears `KeepSubscriptions`, the bridge MUST
		// cancel every existing subscription owned by the same peer
		// before admitting the new one.
		//
		// Mirrors matter.js packages/node/src/node/server/
		// InteractionServer.ts:549-566 which matches on same-session
		// for pre-fabric (PASE) subscriptions and on (fabric, peerNodeID)
		// for operational (CASE) subscriptions. The previous guard
		// `fabricIndex != 0 && SourceNodeID != 0` was too narrow — it
		// silently skipped PASE re-subscriptions (fabricIndex=0) so a
		// PASE subscriber that re-subscribes with KeepSubscriptions=false
		// would accumulate stale subscriptions.
		if !req.KeepSubscriptions {
			if fabricIndex != 0 && requestHdr.SourceNodeID != 0 {
				// CASE session — tear down by (fabric, peer) tuple.
				if cleared := m.ClosePeer(fabricIndex, requestHdr.SourceNodeID); cleared > 0 {
					b.logger.Info("matter.rx.im.subscribe.peer_teardown",
						slog.String("src", srcString(src)),
						slog.Int("fabric", int(fabricIndex)),
						slog.Uint64("peer_node", requestHdr.SourceNodeID),
						slog.Int("cleared", cleared))
				}
			} else {
				// PASE session (fabricIndex=0) — tear down by SessionID so
				// a re-subscribe on the same PASE exchange replaces, rather
				// than accumulates, the prior subscription.
				// Mirrors matter.js InteractionServer.ts:549-566 same-session
				// match for the pre-fabric path.
				m.CloseSession(requestHdr.SessionID)
			}
		}
		sub, err := m.Subscribe(subscription.SubscribeArgs{
			FabricIndex:        fabricIndex,
			PeerNodeID:         requestHdr.SourceNodeID,
			SessionID:          requestHdr.SessionID,
			MinIntervalFloor:   req.MinIntervalFloor,
			MaxIntervalCeiling: req.MaxIntervalCeiling,
			KeepSubscriptions:  req.KeepSubscriptions,
			AttributePaths:     req.AttributeRequests,
			// EventPaths wiring was missing — without this every
			// cluster-emitted event (GenericSwitch press, BDBI
			// ReachableChanged, OperationalCredentials NOCsChanged,
			// …) queued via [Manager.OnEventFired] failed to match
			// any active subscription and was silently dropped.
			// matter.js
			// packages/protocol/src/interaction/SubscriptionHandler.ts
			// always passes both path arrays.
			EventPaths: req.EventRequests,
			// Replace any stale subscription that arrived on the same
			// CASE session earlier (parallel-reconnect race). Two
			// SubscribeRequests on the same session can only arise when
			// the peer's first attempt is racing a reconnect; keeping
			// the older entry drains quota and causes duplicate reports.
			// Applied only for CASE sessions (fabricIndex != 0 and a
			// real SourceNodeID) because PASE re-subscribe cleanup is
			// handled by the KeepSubscriptions=false / CloseSession
			// path above.
			ReplaceSessionDuplicate: fabricIndex != 0 && requestHdr.HasSourceNodeID,
		})
		if err != nil {
			b.logger.Warn("matter.rx.im.subscribe.manager",
				slog.String("src", srcString(src)),
				slog.String("err", err.Error()))
			// Fall through with subID=0; controller treats this as
			// "subscription request denied" and may retry.
		} else {
			subID = sub.ID
			initialReport.HasSubscription = true
			initialReport.SubscriptionID = subID
			b.captureSubTarget(subID, src, requestHdr, proto)
		}
	}

	// Send initial ReportData (opcode 0x05) chunked. Apple Home's
	// initial subscribe over CASE expands to ~1100 attribute reports
	// totalling 60+ KB — well past the AES-CCM 64KB plaintext cap
	// AND the per-datagram budget. chunkReportData splits on
	// per-attribute boundaries; each chunk except the last carries
	// MoreChunkedMessages=true (Matter §10.6.6).
	chunks, err := chunkReportData(initialReport, reportChunkPayloadBudget)
	if err != nil {
		debugReplyError(b.logger, "chunk_initial_report", src, err)
		return err
	}
	// Subscribe-Initial chunk-streaming pattern (Matter §10.6.6, mirror
	// of matter.js `InteractionMessenger.ts:sendDataReportMessage(_,
	// waitForAck=true)`): send one ReportData chunk, block on the
	// peer's IM:StatusResponse for this exchange, then send the next.
	//
	// Apple's ReadClient (`connectedhomeip/src/app/ReadClient.cpp:541`
	// → `OnMessageReceived`) emits exactly one StatusResponse per
	// inbound ReportData and stays in the per-chunk read state until
	// that response is on the wire. matter.js's successful Apple-pair
	// byte-trace shows ~1 ms ping-pong between every
	// chunk and Apple's StatusResponse. Earlier openccu-loom burst-
	// fired every chunk without ever syncing on the StatusResponse —
	// Apple's state-machine collapsed into a path where
	// `ProcessSubscribeResponse` never triggered (Run 19 of the Apple-pair-diagnose cycle).
	//
	// 2 s timeout per chunk is generous — matter.js's observed round-
	// trip is sub-millisecond. On timeout we fall through and ship the
	// next chunk anyway; the underlying MRP layer keeps the wire
	// reliable even if Apple skips a StatusResponse (matter.js test
	// commissioners do, and the burst-mode path stayed functional for
	// them — only Apple's strict per-chunk state-machine cares).
	for i, chunk := range chunks {
		body, err := EncodeReportData(chunk)
		if err != nil {
			debugReplyError(b.logger, "encode_initial_report", src, err)
			return err
		}
		// chip-strict pre-flight: optional TLV strict validation of the
		// encoded chunk before it hits the wire. chip TLVReader.cpp:806-839
		// drops the whole IM message when any of the strict tag /
		// container rules fire — Apple's MTRDevice surfaces the failure
		// as a silent "could not find cached attribute values for
		// attribute" log breadcrumb. Gated on the same dump env var so
		// the cost stays in diagnose mode.
		if os.Getenv("OPENCCU_LOOM_MATTER_DUMP_SUBSCRIBE") != "" {
			if vErr := tlv.Validate(body); vErr != nil {
				b.logger.Warn("matter.tx.im.subscribe.chunk_strict_violation",
					slog.Int("chunk", i),
					slog.String("err", vErr.Error()))
			}
		}
		// Diagnose hook: structured log line + optional disk dump for
		// every Subscribe-initial chunk so the TLV tree can be
		// post-mortemed against the Matter spec / matter.js reference
		// output. Apple's MTRDevice has been seen logging "last
		// report: (null)" while MRP-acking every chunk, which points
		// at an IM-decoder rejection of this exact byte stream. The
		// disk dump is gated on the OPENCCU_LOOM_MATTER_DUMP_SUBSCRIBE
		// env var so production deployments do not fill /tmp; the log
		// line is always emitted at debug level. Dump path is scoped
		// per-subscription so a later Sub does not overwrite the first
		// Pair's chunks (the ones Apple validates topology against).
		b.logger.Debug("matter.tx.im.subscribe.chunk",
			slog.Int("subscription_id", int(subID)),
			slog.Int("chunk", i),
			slog.Int("chunk_count", len(chunks)),
			slog.Int("bytes", len(body)),
			slog.Bool("final", i == len(chunks)-1))
		if os.Getenv("OPENCCU_LOOM_MATTER_DUMP_SUBSCRIBE") != "" {
			_ = os.WriteFile(fmt.Sprintf("/tmp/subscribe-init-sub%d-chunk%d.bin", subID, i), body, 0o600)
		}

		// Arm the per-exchange StatusResponse waiter BEFORE the send
		// to avoid a missed-wakeup race: Apple can reply faster than
		// our scheduler returns from sendReplyReliable.
		waitCh := b.armStatusResponseWait(proto.ExchangeID)
		// Piggyback the latest peer-sent counter on this chunk's
		// AckCounter. Without this rewrite every chunk carries the
		// stale SubscribeRequest counter, and python-matter-server's
		// ReliableMessaging drops chunk N+1 after the peer has
		// StatusResponse-acked chunk N — "Dropping message without
		// piggyback ack when we are waiting for an ack".
		chunkHdr := *requestHdr
		b.refreshAckCounter(&chunkHdr, proto.ExchangeID)
		if err := b.sendReplyReliable(src, &chunkHdr, proto, im.OpcodeReportData, body); err != nil {
			b.disarmStatusResponseWait(proto.ExchangeID)
			debugReplyError(b.logger, "send_initial_report", src, err)
			return err
		}
		// Block until Apple StatusResponse-acks this chunk on the IM
		// layer, or the timeout expires.
		//
		// chip's ReadHandler.cpp:241-271 sends `SendSubscribeResponse`
		// ONLY AFTER `OnStatusResponse` for the FINAL chunk. Verified
		// empirically: with the final-chunk-wait, Apple's MTRDevice reached
		// the `1 => 2` transition (`Subscription established in 14090ms`,
		// `InitialSubscriptionEstablished`) for the first time; without the
		// final-wait (Run 22) the cache reverted to 0 sensors and Apple
		// remained in `Subscribing`. Keep the wait unconditional on all
		// chunks including final — the `final` flag in the timeout log is
		// diagnostic only.
		select {
		case <-waitCh:
			b.disarmStatusResponseWait(proto.ExchangeID)
		case <-time.After(perChunkStatusRespTimeout):
			b.disarmStatusResponseWait(proto.ExchangeID)
			b.logger.Debug("matter.tx.subscribe.chunk_ack_timeout",
				slog.String("src", srcString(src)),
				slog.Int("chunk", i),
				slog.Int("exchange", int(proto.ExchangeID)),
				slog.Bool("final", !chunk.MoreChunkedMessages),
				slog.String("timeout", perChunkStatusRespTimeout.String()))
		}

		b.logger.Debug("matter.rx.im.subscribe.chunk",
			slog.String("src", srcString(src)),
			slog.Int("chunk", i),
			slog.Int("of", len(chunks)),
			slog.Int("bytes", len(body)),
			slog.Bool("more", chunk.MoreChunkedMessages))
	}

	// Send SubscribeResponse (opcode 0x04). Caps MaxInterval at the
	// manager's effective ceiling (stored on the Subscription
	// record); when no manager is wired we echo the requested max.
	// Reliable path — Apple ACKs the SubscribeResponse via MRP; if
	// the response drops Apple times the exchange out and tears the
	// fabric down, so MRP retransmit is mandatory here.
	maxInt := req.MaxIntervalCeiling
	if m := b.subscriptionManagerLocked(); m != nil && subID != 0 {
		if sub, err := m.Get(subID); err == nil {
			maxInt = sub.MaxIntervalCeiling
		}
	}
	respBody, err := EncodeSubscribeResponse(im.SubscribeResponse{
		SubscriptionID: subID,
		MaxInterval:    maxInt,
	})
	if err != nil {
		debugReplyError(b.logger, "encode_subscribe_response", src, err)
		return err
	}
	// Piggyback the LATEST inbound counter on this exchange — not just
	// the SubscribeRequest's counter that triggered this reply. The
	// chip-tool commissioner sends an IM:StatusResponse acking the
	// initial-report chunk before the bridge fires the
	// SubscribeResponse; with the UDP-per-datagram goroutine model that
	// StatusResponse hits the receive pipeline a few ms after the chunk
	// send, BEFORE the chunk-ack
	// wait unblocks. Acking only the original SubscribeRequest
	// counter (`requestHdr.MessageCounter`) leaves chip-tool's
	// ReliableMessaging layer still waiting on the StatusResponse
	// counter, so it drops the SubscribeResponse with
	// `Dropping message without piggyback ack when we are waiting
	// for an ack` and the subscription times out. Atomically
	// fetch+clear the latest pending ack-counter and rewrite
	// requestHdr.MessageCounter to that value so sendReplyReliable's
	// `AckCounter: requestHdr.MessageCounter` piggybacks the freshest
	// observed counter (cumulative ack per Matter §4.12.4.2).
	subRespHdr := *requestHdr
	b.mu.RLock()
	ackTracker := b.ackTracker
	b.mu.RUnlock()
	if ackTracker != nil {
		if counter, ok := ackTracker.LookupAndDischarge(proto.ExchangeID); ok {
			subRespHdr.MessageCounter = counter
		}
	}
	if err := b.sendReplyReliable(src, &subRespHdr, proto, im.OpcodeSubscribeResponse, respBody); err != nil {
		debugReplyError(b.logger, "send_subscribe_response", src, err)
		return err
	}
	// Do NOT call dischargeOwedAck here. The piggyback ack on the
	// SubscribeResponse itself fulfils the obligation for the original
	// SubscribeRequest counter, but the tracker is exchange-keyed:
	// `tracker.Discharge(exchangeID)` clears ALL pending obligations on
	// the exchange, including any registered between sendReplyReliable
	// constructing the reply and this line — most notably chip-tool's
	// IM:StatusResponse acking the initial-report chunk (Matter §8.6.2)
	// which arrives a millisecond after the initial chunk's wait
	// unblocks. Discharging that StatusResponse obligation here means
	// the ack pump never emits a StandaloneAck for it, chip-tool's
	// ReliableMessaging retransmits 4x and gives up, and the Subscribe
	// times out with `CHIP Error 0x32 Timeout`. Leaving the
	// obligation in the tracker lets the 50 ms pump fire the
	// StandaloneAck on its own cadence; the piggyback we already
	// shipped is benign-redundant for the SubscribeRequest counter,
	// and chip-tool's cumulative-ack semantics (Matter §4.12.4.2)
	// absorb a StandaloneAck that lands after the piggyback.

	// IMPORTANT: NO primer ReportData after SubscribeResponse.
	//
	// Earlier code shipped an empty `ReportData{HasSubscription:true,
	// SubscriptionID:subID}` here under the assumption that Apple's
	// MTRDevice expected a follow-up "subscription primed" signal —
	// that hypothesis is now falsified. matter.js's bridge sample (Apple-
	// pair-success byte-dump) sends *only* the chunked
	// ReportData burst followed by exactly one SubscribeResponse and
	// stops; Apple's ReadClient (`connectedhomeip/src/app/ReadClient.cpp:
	// ProcessSubscribeResponse:1124`) MoveToState(SubscriptionActive)
	// + OnSubscriptionEstablished synchronously on the SubscribeResponse
	// itself, which is what flips MTRDevice from `Subscribing` to
	// `InitialSubscriptionEstablished`. Sending an extra ReportData
	// AFTER SubscribeResponse arrives at a ReadClient already in
	// SubscriptionActive — Apple's IM machinery routes it as a
	// regular subscription update rather than a primer, and (per
	// Apple-pair Run 18 byte-trace) appears to interfere with the
	// HAP-Service-Mapper's cache-flush step that gates the
	// `1 => 2` MTRDevice state transition.

	// Stamp lastReport=now on the freshly-created subscription so the
	// engine treats the initial-report flush + primer as the first
	// report. Without this lastReport stays zero, maxIntervalElapsed
	// fires on the very first 250 ms tick, and the engine immediately
	// emits an empty keep-alive — extra wire chatter that races the
	// primer above.
	if m := b.subscriptionManagerLocked(); m != nil && subID != 0 {
		if sub, err := m.Get(subID); err == nil {
			sub.TouchLastReport(time.Now())
		}
	}
	b.logger.Debug("matter.rx.im.subscribe",
		slog.String("src", srcString(src)),
		slog.Int("subscription_id", int(subID)),
		slog.Int("paths", len(req.AttributeRequests)),
		slog.Int("event_paths", len(req.EventRequests)),
		slog.Int("initial_reports", len(initialReport.Reports)),
		slog.Int("initial_events", len(initialReport.EventReports)),
		slog.Int("min_interval", int(req.MinIntervalFloor)),
		slog.Int("max_interval", int(req.MaxIntervalCeiling)))
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
		logger := b.logger
		unsub := notifier.OnMatterValueChanged(func() {
			if logger != nil {
				logger.Debug("matter.bridge.measurement.notify",
					slog.Int("endpoint", int(epID)),
					slog.Int("paths", len(pathSet)))
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
