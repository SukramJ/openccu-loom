// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"sort"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// buildInitialReport assembles the initial ReportData for a Subscribe-Initial
// by running each requested attribute path through the dispatcher, merging
// cached EventReports, sorting the result, and emitting per-attribute
// diagnostic log lines. Returns the assembled report with
// HasSubscription=false (the caller stamps that after registerSubscription)
// plus the count of matched attribute/event paths — taken BEFORE
// DataVersionFilter suppression, so the caller can reject a
// zero-matching Subscribe (ServerSubscription.ts:610-614) without
// misfiring on an all-cached re-subscribe.
//
// Mirrors matter.js InteractionServer.ts:startReadInteraction for the
// Subscribe path (attribute reading + event merging + sort).
func (b *Bridge) buildInitialReport(
	subCtx context.Context,
	dispatcher im.Dispatcher,
	req im.SubscribeRequest,
) (report im.ReportData, matchedPaths int) {
	// Build the initial ReportData by running each requested path
	// through the dispatcher and collecting the results into one
	// AttributeReport list. Mirrors HandleReadRequest at the path
	// level but lets the caller stamp HasSubscription/SubscriptionID
	// on the output.
	initialReport := im.ReportData{
		HasSubscription: false, // overwritten below when manager wires the subscription
		Reports:         nil,
	}
	matched := 0
	for _, path := range req.AttributeRequests {
		// Authorize each result against the requesting subject (subCtx
		// carries fabric + subject from handleSubscribeRequest). Without
		// this the Subscribe-Initial would leak fabric-sensitive
		// attributes (ACL, NOCs) to a View-only / ACE-less subject, the
		// same bypass reportSubscription closes for ongoing reports.
		for _, rr := range b.readAuthorizedResults(subCtx, dispatcher, path) {
			matched++
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
		raw := im.BuildEventReports(req.EventRequests, b.eventLog, req.EventFilters)
		// The establish decision (matched > 0) counts the REQUESTED event paths,
		// not the priming-log instances: a momentary/event-only subscribe
		// (GenericSwitch button) is placed BEFORE the event fires, so its event
		// log is empty at establish time — counting log records would reject it
		// with no_match and it could never receive the future press. matter.js
		// establishes an event subscription regardless of the priming log
		// (ServerSubscription.ts emits 0+ EventReports on Subscribe-Initial).
		// The raw records still seed the initial report below; authorization
		// filtering only affects which events are disclosed, not whether the
		// subscription establishes — then authorize + fabric-project the priming
		// events so a wildcard event subscribe from fabric B does not receive
		// fabric A's AccessControl-change events, and a non-Administer subject
		// sees no AccessControl events (Matter §8.4.3.2 / §9.10.7.1). Mirrors
		// matter.js EventReadResponse.ts #readAllowedEvents; subCtx carries the
		// requesting fabric + subject.
		matched += len(req.EventRequests)
		_, subFabricIndex := im.FabricFilterFromContext(subCtx)
		subSubjectNodeID, subSubjectCATs := im.SubjectFromContext(subCtx)
		auth := b.eventReadAuthorizer(dispatcher, subFabricIndex, subSubjectNodeID, subSubjectCATs)
		initialReport.EventReports = im.AuthorizeEventReports(subCtx, auth, raw)
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
	return initialReport, matched
}

// registerSubscription registers the subscribe request in the subscription
// manager (handling KeepSubscriptions teardown), captures the routing
// subTarget, and stamps HasSubscription + SubscriptionID on initialReport in
// place. Returns the allocated subID (0 when no manager is wired or Subscribe
// fails — the orchestrator falls through with a synthetic subID=0 reply).
//
// Mirrors matter.js packages/node/src/node/server/InteractionServer.ts:549-566
// for the KeepSubscriptions teardown, and
// packages/protocol/src/interaction/SubscriptionHandler.ts for the
// EventPaths wiring.
func (b *Bridge) registerSubscription(
	src *net.UDPAddr,
	requestHdr *message.Header,
	proto message.ProtocolHeader,
	req im.SubscribeRequest,
	initialReport *im.ReportData,
) uint32 {
	// Subscribe in the manager so quota + cadence bookkeeping is
	// centralised there. The report pump is wired in the daemon bring-up
	// (SetEventReporter / SubscriptionReporter in daemon_matter.go); the
	// manager's engine tick drives delivery through the per-subscription
	// subTarget.
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
			b.captureSubTarget(subID, src, requestHdr, proto, req.FabricFiltered)
		}
	}
	return subID
}

// streamInitialReportChunks encodes initialReport into per-datagram chunks
// and sends them one at a time, blocking for a peer IM:StatusResponse
// between each chunk (Matter §10.6.6).
//
// Subscribe-Initial chunk-streaming pattern mirrors matter.js
// `InteractionMessenger.ts:sendDataReportMessage(_, waitForAck=true)`:
// send one ReportData chunk, block on the peer's IM:StatusResponse for
// this exchange, then send the next.
//
// Apple's ReadClient (`connectedhomeip/src/app/ReadClient.cpp:541`
// → `OnMessageReceived`) emits exactly one StatusResponse per
// inbound ReportData and stays in the per-chunk read state until
// that response is on the wire.
func (b *Bridge) streamInitialReportChunks( //nolint:gocognit // per-chunk ack synchronisation loop mirrors matter.js sendDataReportMessage; splitting would obscure the atomic arm→send→wait sequence
	src *net.UDPAddr,
	requestHdr *message.Header,
	proto message.ProtocolHeader,
	subID uint32,
	initialReport im.ReportData,
) error {
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
		waitCh := b.armStatusResponseWait(requestHdr.SessionID, proto.ExchangeID)
		// Piggyback the latest peer-sent counter on this chunk's
		// AckCounter. Without this rewrite every chunk carries the
		// stale SubscribeRequest counter, and python-matter-server's
		// ReliableMessaging drops chunk N+1 after the peer has
		// StatusResponse-acked chunk N — "Dropping message without
		// piggyback ack when we are waiting for an ack".
		chunkHdr := *requestHdr
		b.refreshAckCounter(&chunkHdr, proto.ExchangeID)
		if err := b.sendReplyReliable(src, &chunkHdr, proto, im.OpcodeReportData, body); err != nil {
			b.disarmStatusResponseWait(requestHdr.SessionID, proto.ExchangeID)
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
			b.disarmStatusResponseWait(requestHdr.SessionID, proto.ExchangeID)
		case <-time.After(perChunkStatusRespTimeout):
			b.disarmStatusResponseWait(requestHdr.SessionID, proto.ExchangeID)
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
	return nil
}

// sendSubscribeResponse sends the SubscribeResponse (opcode 0x04),
// piggybacks the latest inbound counter, stamps lastReport on the
// freshly-created subscription, and emits the final subscribe diagnostic
// log line.
//
// Reliable path — Apple ACKs the SubscribeResponse via MRP; if
// the response drops Apple times the exchange out and tears the
// fabric down, so MRP retransmit is mandatory here.
//
// The piggyback ack counter refresh mirrors Matter §4.12.4.2
// (cumulative ack): the chip-tool commissioner sends an IM:StatusResponse
// acking the initial-report chunk before the bridge fires the
// SubscribeResponse, so acking only the original SubscribeRequest counter
// leaves chip-tool's ReliableMessaging still waiting on the StatusResponse
// counter.
func (b *Bridge) sendSubscribeResponse(
	src *net.UDPAddr,
	requestHdr *message.Header,
	proto message.ProtocolHeader,
	req im.SubscribeRequest,
	subID uint32,
	initialReport im.ReportData,
) error {
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
		if counter, ok := ackTracker.LookupAndDischarge(requestHdr.SessionID, proto.ExchangeID); ok {
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
