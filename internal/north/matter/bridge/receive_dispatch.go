// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/schema"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// absorbStatusResponse handles an inbound StatusResponse opcode.
// StatusResponse is the spec-mandated ACK for a ReportData /
// SubscribeResponse / Invoke / Write reply we sent earlier (Matter
// §8.6.2). Apple Home, Google Home, and chip-tool all emit it
// after consuming our reply; silently absorbing it satisfies the
// MRP / Reliable Messaging contract without making the dispatcher
// see a bogus "request". Without this branch the commissioner
// retransmits its previous request indefinitely after pairing,
// which Apple eventually surfaces as "device added" → immediate
// disconnect.
func (b *Bridge) absorbStatusResponse(src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader) error {
	// Do NOT call dischargeOwedAck here. The previous code did
	// — the rationale "we just piggyback-acked on an outbound reply"
	// is correct for *request* opcodes that immediately produce a
	// reply, but a StatusResponse is itself the peer's IM-level
	// reply; there is no outbound on which to piggyback. Cancelling
	// the obligation that `owedInboundAck` (line ~147) registered a
	// moment earlier leaves the inbound StatusResponse silently
	// un-ACKed at the MRP layer, and chip-tool's ReliableMessaging
	// retransmits it 4 times before giving up (symptom:
	// `Dropping message without piggyback ack when we are waiting
	// for an ack`, then `CHIP Error 0x32 Timeout`). Let the ack pump
	// fire the synthesised StandaloneAck per its normal cadence so
	// the peer's MRP layer can advance to receive subsequent
	// retransmits / new exchanges.
	//
	// Subscribe-Initial chunk-streaming loop in subscribe.go blocks
	// on this signal between chunks to mirror matter.js's
	// `sendDataReportMessage(_, waitForAck=true)` round-trip
	// pattern. Apple's ReadClient emits one IM:StatusResponse per
	// inbound ReportData chunk; without per-chunk sync our burst
	// fires past Apple's state-machine and
	// `ProcessSubscribeResponse` never triggers (Run 19 of the
	// Apple-pair-diagnose cycle).
	b.signalStatusResponseRX(requestHdr.SessionID, proto.ExchangeID)
	b.logger.Debug("matter.rx.im.status_ack",
		slog.String("src", srcString(src)),
		slog.Int("exchange", int(proto.ExchangeID)))
	return nil
}

// rejectUnsupportedOpcode handles any opcode that is not a request opcode
// and not StatusResponse. Returns ErrUnsupportedOpcode.
func (b *Bridge) rejectUnsupportedOpcode(src *net.UDPAddr, proto message.ProtocolHeader) error {
	err := fmt.Errorf("%w: opcode=0x%02X", ErrUnsupportedOpcode, proto.Opcode)
	b.logger.Debug("matter.rx.im.unsupported",
		slog.String("src", srcString(src)),
		slog.Int("opcode", int(proto.Opcode)),
		slog.String("err", err.Error()))
	return err
}

// rejectGroupSession handles Read/Subscribe/Timed request opcodes that
// arrived over a Secure Group session, which is forbidden by Matter §8.5.7.
// Sends StatusResponse(InvalidAction) and discharges the owed ACK.
// The matter.js citation for the group-session rule is on classifyIMOpcode.
func (b *Bridge) rejectGroupSession(src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader) error {
	b.logger.Warn("matter.rx.im.group_reject",
		slog.String("src", srcString(src)),
		slog.Int("opcode", int(proto.Opcode)))
	body, err := EncodeStatusResponse(im.StatusResponse{Status: im.StatusInvalidAction})
	if err != nil {
		debugReplyError(b.logger, "encode_group_reject", src, err)
		return err
	}
	if err := b.sendReply(src, requestHdr, proto, im.OpcodeStatusResponse, body); err != nil {
		debugReplyError(b.logger, "send_group_reject", src, err)
		return err
	}
	b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID)
	return nil
}

// dispatchReadRequest handles a decoded ReadRequest. The TLV decode and
// dispatcher nil-check are done by the caller (handleIMOpcode); this method
// receives the already-decoded req and runs fabric-context stamping, event
// merging, chunked report encoding, and the per-chunk IM:StatusResponse wait.
func (b *Bridge) dispatchReadRequest(ctx context.Context, src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader, dispatcher im.Dispatcher, req im.ReadRequest) error {
	// Diagnostic: dump every requested attribute path so we can
	// identify which spec-conformance probe Apple Home runs
	// post-CommissioningComplete. The 27/28-byte read requests
	// that immediately precede RemoveFabric carry one
	// AttributePath each — logging endpoint/cluster/attribute
	// triples reveals exactly what Apple's iCloud-Heim sync is
	// reading and rejecting.
	for _, p := range req.AttributeRequests {
		b.logger.Debug("matter.rx.im.read_path",
			slog.String("src", srcString(src)),
			slog.Any("endpoint", p.Endpoint),
			slog.String("endpoint_set", strconv.FormatBool(p.HasEndpoint)),
			slog.Any("cluster", p.Cluster),
			slog.String("cluster_set", strconv.FormatBool(p.HasCluster)),
			slog.Any("attribute", p.Attribute),
			slog.String("attribute_set", strconv.FormatBool(p.HasAttribute)))
	}
	// Reject illegal paths up front (wildcard cluster + concrete non-global
	// attribute, or wildcard cluster + concrete event) with a top-level
	// InvalidAction StatusResponse. Mirrors matter.js InteractionServer.ts
	// validateReadPaths (#3926, Matter §8.4.3.2).
	if im.ValidateReadPaths(req.AttributeRequests, req.EventRequests) != im.StatusSuccess {
		body, err := EncodeStatusResponse(im.StatusResponse{Status: im.StatusInvalidAction})
		if err != nil {
			debugReplyError(b.logger, "encode_read_path_reject", src, err)
			return err
		}
		if err := b.sendReply(src, requestHdr, proto, im.OpcodeStatusResponse, body); err != nil {
			debugReplyError(b.logger, "send_read_path_reject", src, err)
			return err
		}
		b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID)
		return nil
	}
	// Stamp the FabricFiltered flag + the requesting FabricIndex
	// into the context so fabric-scoped cluster servers
	// (OperationalCredentials, AccessControl) can project their
	// fabric-sensitive list attributes to the requesting fabric.
	// Mirrors matter.js InteractionServer.ts:startReadInteraction →
	// OnlineContext.forFabricFilteredRead + fabricIndex.
	readFabricIndex := b.resolveSessionFabric(requestHdr.SessionID)
	readSubjectNodeID, readSubjectCATs := b.resolveSessionSubject(requestHdr.SessionID)
	readCtx := im.WithFabricFilter(ctx, req.FabricFiltered, readFabricIndex)
	readCtx = im.WithSubject(readCtx, readSubjectNodeID, readSubjectCATs)
	report := im.HandleReadRequest(readCtx, dispatcher, req)
	// Evaluate EventRequests against the persistent event log so
	// chip-tool `read-event-by-id` and Apple MTRDevice liveness
	// checks return the buffered StartUp / BootReason / etc. events.
	// Matter §10.6.6: a ReadRequest may carry both AttributeRequests
	// and EventRequests; the bridge merges both into one ReportData.
	if len(req.EventRequests) > 0 {
		report.EventReports = im.HandleReadEventRequest(req, b.eventLog)
	}
	// Diagnostic: show what we returned per path.
	for i, r := range report.Reports {
		b.logger.Debug("matter.tx.im.read_report",
			slog.Int("idx", i),
			slog.Any("endpoint", r.Path.Endpoint),
			slog.Any("cluster", r.Path.Cluster),
			slog.Any("attribute", r.Path.Attribute),
			slog.Bool("status", r.IsStatus),
			slog.Any("status_code", r.Status.Status),
			slog.String("value_type", fmt.Sprintf("%T", r.Value.Value)),
			slog.Any("value", r.Value.Value))
	}
	if len(report.EventReports) > 0 {
		b.logger.Debug("matter.tx.im.read_event_reports",
			slog.Int("event_reports", len(report.EventReports)))
	}
	chunks, err := chunkReportData(report, reportChunkPayloadBudget)
	if err != nil {
		debugReplyError(b.logger, "chunk_report", src, err)
		return err
	}
	for i, chunk := range chunks {
		body, err := EncodeReportData(chunk)
		if err != nil {
			debugReplyError(b.logger, "encode_report", src, err)
			return err
		}
		// Mirror the Subscribe path's per-chunk IM:StatusResponse wait
		// so Apple Home's MTRDevice processes each ReportData frame on
		// the IM layer before the next arrives. matter.js's
		// InteractionMessenger acks every chunk on the IM layer
		// (not just MRP); without the wait openccu-loom
		// burst-fires all chunks back-to-back and Apple's
		// `ProcessReadResponse` state machine drops late chunks.
		// Subscribe path applied this fix as P15; this closes
		// the symmetric drift on Read.
		waitCh := b.armStatusResponseWait(requestHdr.SessionID, proto.ExchangeID)
		// Piggyback the latest peer-sent counter on this chunk's
		// AckCounter. Without this rewrite every chunk carries
		// the stale ReadRequest counter, and python-matter-server's
		// ReliableMessaging drops chunk N+1 after the peer has
		// StatusResponse-acked chunk N — "Dropping message without
		// piggyback ack when we are waiting for an ack". Mirrors
		// the symmetric fix in the Subscribe-Initial loop.
		chunkHdr := *requestHdr
		b.refreshAckCounter(&chunkHdr, proto.ExchangeID)
		if err := b.sendReplyReliable(src, &chunkHdr, proto, im.OpcodeReportData, body); err != nil {
			b.disarmStatusResponseWait(requestHdr.SessionID, proto.ExchangeID)
			debugReplyError(b.logger, "send_report", src, err)
			return err
		}
		select {
		case <-waitCh:
			b.disarmStatusResponseWait(requestHdr.SessionID, proto.ExchangeID)
		case <-time.After(perChunkStatusRespTimeout):
			b.disarmStatusResponseWait(requestHdr.SessionID, proto.ExchangeID)
			b.logger.Debug("matter.tx.read.chunk_ack_timeout",
				slog.String("src", srcString(src)),
				slog.Int("chunk", i),
				slog.Int("exchange", int(proto.ExchangeID)),
				slog.Bool("final", !chunk.MoreChunkedMessages),
				slog.String("timeout", perChunkStatusRespTimeout.String()))
		}
		b.logger.Debug("matter.rx.im.read.chunk",
			slog.String("src", srcString(src)),
			slog.Int("chunk", i),
			slog.Int("of", len(chunks)),
			slog.Int("bytes", len(body)),
			slog.Bool("more", chunk.MoreChunkedMessages))
	}
	b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID)
	b.logger.Debug("matter.rx.im.read",
		slog.String("src", srcString(src)),
		slog.Int("attribute_reports", len(report.Reports)),
		slog.Int("chunks", len(chunks)))
	return nil
}

// dispatchWriteRequest handles a decoded WriteRequest. The TLV decode and
// dispatcher nil-check are done by the caller (handleIMOpcode).
func (b *Bridge) dispatchWriteRequest(ctx context.Context, src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader, dispatcher im.Dispatcher, req im.WriteRequest) error {
	for _, w := range req.Writes {
		b.logger.Debug("matter.rx.im.write_path",
			slog.String("src", srcString(src)),
			slog.Any("endpoint", w.Path.Endpoint),
			slog.Any("cluster", w.Path.Cluster),
			slog.Any("attribute", w.Path.Attribute),
			slog.String("value_type", fmt.Sprintf("%T", w.Value.Value)))
	}
	// A chunked write may not suppress the response — matter.js
	// InteractionServer.ts:397-402 rejects the combination with
	// InvalidAction before any timed-interaction handling.
	if req.MoreChunkedMessages && req.SuppressResponse {
		return b.replyTimedStatus(src, requestHdr, proto, "write_chunked_suppress", im.StatusInvalidAction)
	}
	if status, gated := b.checkTimedGate(req.TimedRequest, requestHdr.SessionID, proto.ExchangeID); gated {
		return b.replyTimedStatus(src, requestHdr, proto, "write", status)
	}
	// A write inside a timed interaction may not be chunked — matter.js
	// InteractionServer.ts:408-413 ("Write Request action that is part
	// of a Timed Write Interaction SHALL NOT be chunked"). The gate
	// above passed, so a set TimedRequest flag means the timed window
	// existed and was valid.
	if req.TimedRequest && req.MoreChunkedMessages {
		return b.replyTimedStatus(src, requestHdr, proto, "write_timed_chunked", im.StatusInvalidAction)
	}
	// Stamp the session FabricIndex onto ctx so fabric-scoped writes
	// (AccessControl.ACL above all) resolve the caller's fabric the
	// same way reads do via [im.FabricFilterFromContext]. Without
	// this stamp AccessControl.MatterWrite falls back to fabric=1
	// (last resort) and Apple's post-CommissioningComplete ACL
	// update — which carries the new Resident NodeID + CAT subjects
	// — never reaches the requesting fabric. Apple then reads the
	// ACL from its own fabric on the Subscribe-Initial, sees only
	// the pre-existing case_admin_subject entry, classifies the
	// bridge as missing Administer privilege, and tears the pair
	// down with the iOS "accessory could not be added" dialog.
	// Mirrors the Invoke pathway below.
	writeFabricIndex := b.resolveSessionFabric(requestHdr.SessionID)
	writeSubjectNodeID, writeSubjectCATs := b.resolveSessionSubject(requestHdr.SessionID)
	writeCtx := im.WithFabricFilter(ctx, false, writeFabricIndex)
	writeCtx = im.WithSubject(writeCtx, writeSubjectNodeID, writeSubjectCATs)
	resp := im.HandleWriteRequest(writeCtx, dispatcher, req)
	// Honor SuppressResponse=true per Matter §10.6.3.1: when the
	// initiator opts out of the WriteResponse the server MUST
	// elide it. matter.js InteractionServer.ts and chip
	// WriteHandler.cpp both gate the reply on this flag. We still
	// drive the side-effects (cluster writes already happened
	// inside HandleWriteRequest) and discharge any owed MRP ack.
	if req.SuppressResponse {
		b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID)
		b.logger.Debug("matter.rx.im.write.suppressed",
			slog.String("src", srcString(src)),
			slog.Int("attribute_statuses", len(resp.Responses)))
		return nil
	}
	body, err := EncodeWriteResponse(resp)
	if err != nil {
		debugReplyError(b.logger, "encode_write", src, err)
		return err
	}
	// Reliable: a lost WriteResponse leaves the controller waiting and
	// retrying the whole write. matter.js ships every non-standalone-ack
	// reply on an MRP session reliably (MessageExchange.ts:602
	// `requiresAck ?? (session.usesMrp && !isStandaloneAck)`); the ACK
	// pump retransmits until the peer acks. The response is a pure
	// outcome report, so it meets sendReplyReliable's idempotency
	// contract.
	if err := b.sendReplyReliable(src, requestHdr, proto, im.OpcodeWriteResponse, body); err != nil {
		debugReplyError(b.logger, "send_write", src, err)
		return err
	}
	b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID)
	b.logger.Debug("matter.rx.im.write",
		slog.String("src", srcString(src)),
		slog.Int("attribute_statuses", len(resp.Responses)))
	// Surface non-Success statuses one-per-line so failed writes
	// (notably AccessControl CONSTRAINT_ERROR during Apple pair)
	// leave a forensic trail. Apple's homed prints only its own
	// MTRInteractionErrorDomain mapping, never the raw IM status
	// code — without daemon-side logging the chain of cause is
	// impossible to reconstruct from the wire alone.
	for _, r := range resp.Responses {
		if r.Status.Status.IsSuccess() {
			continue
		}
		b.logger.Warn("matter.tx.im.write_status",
			slog.String("src", srcString(src)),
			slog.Any("endpoint", r.Path.Endpoint),
			slog.Any("cluster", r.Path.Cluster),
			slog.Any("attribute", r.Path.Attribute),
			slog.Any("status_code", uint8(r.Status.Status)),
			slog.String("status_name", r.Status.Status.String()),
			slog.Uint64("fabric", uint64(writeFabricIndex)))
	}
	return nil
}

// anyTimedRequiredInvoke reports whether any command in req targets a
// timed-required (cluster, command) pair per matter.js (schema.IsTimedInvoke).
// A batched invoke is timed-required as a whole if any of its commands is.
func anyTimedRequiredInvoke(req im.InvokeRequest) bool {
	for i := range req.Invokes {
		p := req.Invokes[i].Path
		if schema.IsTimedInvoke(p.Cluster, p.Command) {
			return true
		}
	}
	return false
}

// dispatchInvokeRequest handles a decoded InvokeRequest. The TLV decode and
// dispatcher nil-check are done by the caller (handleIMOpcode).
//
// Server-side timed-required conformance: a command marked "T" in the matter.js
// model (schema.IsTimedInvoke) must be invoked inside a valid timed window even
// when the controller left the InvokeRequest's own Timed flag clear. Folding it
// into the gate flag makes a timed-required command with no window yield
// NEEDS_TIMED_INTERACTION, mirroring matter.js CommandInvokeResponse.ts:266
// `if (limits.timed && !this.session.timed)`. For a non-timed command the flag
// is unchanged, so the existing flag-vs-window mismatch handling is preserved.
func (b *Bridge) dispatchInvokeRequest(ctx context.Context, src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader, dispatcher im.Dispatcher, req im.InvokeRequest) error {
	// Server-side timed-required conformance: a command marked "T" in the
	// matter.js model (schema.IsTimedInvoke) must be invoked inside a valid
	// timed window even when the controller left the InvokeRequest's own Timed
	// flag clear. Fold it into the gate flag so a timed-required command with no
	// window yields NEEDS_TIMED_INTERACTION. Mirrors matter.js
	// CommandInvokeResponse.ts:266 `if (limits.timed && !this.session.timed)`.
	if status, gated := b.checkTimedGate(req.TimedRequest || anyTimedRequiredInvoke(req), requestHdr.SessionID, proto.ExchangeID); gated {
		return b.replyTimedStatus(src, requestHdr, proto, "invoke", status)
	}
	// Stamp the FabricIndex into the context so cluster handlers
	// can distinguish PASE (0) from CASE (>0) — required by
	// GeneralCommissioning.CommissioningComplete (matter.js
	// GeneralCommissioningServer.ts:218-243), AccessControl Write
	// validation (AuthMode-vs-fabric coupling), and
	// OperationalCredentials AddNOC PASE-only guard.
	invokeFabricIndex := b.resolveSessionFabric(requestHdr.SessionID)
	invokeSubjectNodeID, invokeSubjectCATs := b.resolveSessionSubject(requestHdr.SessionID)
	invokeCtx := im.WithFabricFilter(ctx, false, invokeFabricIndex)
	invokeCtx = im.WithSubject(invokeCtx, invokeSubjectNodeID, invokeSubjectCATs)
	// Stamp the operational session ID so
	// OperationalCredentials.handleAddNOC can verify it matches
	// the session that issued the CSRRequest (matter.js
	// OperationalCredentialsServer.ts session-ID binding guard).
	invokeCtx = core.WithInvokeSessionID(invokeCtx, requestHdr.SessionID)
	resp := im.HandleInvokeRequest(invokeCtx, dispatcher, req)
	for i := range resp.Responses {
		rewriteInvokeResponseCommand(&resp.Responses[i])
	}
	body, err := EncodeInvokeResponse(resp)
	if err != nil {
		debugReplyError(b.logger, "encode_invoke", src, err)
		return err
	}
	// Reliable: a lost InvokeResponse surfaces to the controller as
	// "Not Responding" (Apple Home) even though the command executed.
	// matter.js ships every non-standalone-ack reply on an MRP session
	// reliably (MessageExchange.ts:602); the ACK pump retransmits the
	// identical datagram until the peer acks. The response is a pure
	// outcome report, meeting sendReplyReliable's idempotency contract.
	if err := b.sendReplyReliable(src, requestHdr, proto, im.OpcodeInvokeResponse, body); err != nil {
		debugReplyError(b.logger, "send_invoke", src, err)
		return err
	}
	b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID)
	// Diagnose: capture Endpoint/Cluster/Command per InvokeRequest +
	// Status per InvokeResponse. Apple retransmit-loops on Invokes
	// whose response is malformed or contains an unexpected status,
	// so we need to see which command Apple insists on and what we
	// reply.
	reqPaths := make([]string, 0, len(req.Invokes))
	for _, ir := range req.Invokes {
		reqPaths = append(reqPaths, fmt.Sprintf("ep=%d cl=0x%04X cmd=0x%X", ir.Path.Endpoint, ir.Path.Cluster, ir.Path.Command))
	}
	respStatuses := make([]string, 0, len(resp.Responses))
	for _, r := range resp.Responses {
		if r.IsStatus {
			respStatuses = append(respStatuses, fmt.Sprintf("status=%d (path=ep=%d cl=0x%04X cmd=0x%X)", r.Status.Status, r.Path.Endpoint, r.Path.Cluster, r.Path.Command))
		} else {
			respStatuses = append(respStatuses, fmt.Sprintf("cmd_data cmd=0x%X has_response=%v", r.Path.Command, r.HasResponse))
		}
	}
	b.logger.Debug("matter.rx.im.invoke",
		slog.String("src", srcString(src)),
		slog.Int("responses", len(resp.Responses)),
		slog.Int("session_id", int(requestHdr.SessionID)),
		slog.Int("invoke_fabric_index", int(invokeFabricIndex)),
		slog.String("req_paths", strings.Join(reqPaths, ",")),
		slog.String("resp", strings.Join(respStatuses, ",")))
	return nil
}

// dispatchTimedRequest handles a decoded TimedRequest.
// TimedRequest gates a follow-up Write / Invoke against a
// per-exchange deadline (Matter §8.7). The bridge captures
// the deadline in `timedDeadlines` so the next Write / Invoke
// on the same exchange can be checked against it; expired
// or missing-prior-TimedRequest cases are rejected via
// StatusResponse with the spec-mandated codes.
func (b *Bridge) dispatchTimedRequest(src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader, req im.TimedRequest) error {
	deadline := time.Now().Add(time.Duration(req.TimeoutMs) * time.Millisecond)
	// Key on (sessionID, exchangeID) so a different session cannot
	// consume a deadline registered by another session.
	b.timedDeadlines.Store(timedKey{sessionID: requestHdr.SessionID, exchangeID: proto.ExchangeID}, deadline)
	body, err := EncodeStatusResponse(im.StatusResponse{Status: im.StatusSuccess})
	if err != nil {
		debugReplyError(b.logger, "encode_status", src, err)
		return err
	}
	// Reliable: this StatusResponse is the go-ahead the controller waits
	// for before sending its timed Write/Invoke (Matter §8.7). If it is
	// dropped the timed action never arrives and the exchange dies.
	// matter.js ships it reliably (MessageExchange.ts:602).
	if err := b.sendReplyReliable(src, requestHdr, proto, im.OpcodeStatusResponse, body); err != nil {
		debugReplyError(b.logger, "send_status", src, err)
		return err
	}
	b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID)
	b.logger.Debug("matter.rx.im.timed",
		slog.String("src", srcString(src)),
		slog.Int("timeout_ms", int(req.TimeoutMs)))
	return nil
}
