// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/secure/channel"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Errors surfaced by the receive pipeline. They are not returned to
// callers (datagrams arrive on a UDP handler with no error channel)
// — they exist so the bridge can attach them to slog records via
// [slog.String]("err", err.Error()) for diagnostics.
var (
	// ErrUnknownProtocol is logged when ProtocolHeader.ProtocolID is
	// neither [im.InteractionModelProtocolID] nor
	// [mrp.SecureChannelProtocolID]. Per Matter §4.10 the bridge
	// silently drops these.
	ErrUnknownProtocol = errors.New("receive: unknown protocol id")
	// ErrUnsupportedOpcode is logged when an IM datagram carries an
	// opcode the bridge does not service (responses, or future opcodes
	// outside the v1.1 surface).
	ErrUnsupportedOpcode = errors.New("receive: unsupported IM opcode")
	// ErrSessionMissing is logged when an encrypted datagram references
	// a SessionID that the operational session manager does not know.
	// Per Matter §4.6 the bridge silently drops the datagram.
	ErrSessionMissing = errors.New("receive: session not found")
)

// SessionLookup is the narrow operational-session port the receive
// pipeline depends on. Backed by `secure/operational.Manager` in the
// production daemon; tests substitute an in-memory fake.
//
// SessionID==0 always returns (nil, false) by convention — the
// unsecured "session 0" is reserved by Matter for PASE / commissioning
// traffic, which the receive pipeline routes to the unsecured path
// before consulting SessionLookup.
type SessionLookup interface {
	// Lookup returns the session for sessionID. The second return is
	// false when no session is registered.
	Lookup(sessionID uint16) (*channel.Session, bool)
}

// noopSessionLookup is the default when the bridge starts without a
// session manager — every Lookup misses, so encrypted datagrams are
// dropped with [ErrSessionMissing]. Sufficient for the v1.1 GA
// listener-only smoke (no commissioner can complete CASE without the
// operational manager).
type noopSessionLookup struct{}

func (noopSessionLookup) Lookup(uint16) (*channel.Session, bool) { return nil, false }

// AttachSessionLookup wires a session lookup into the bridge after
// construction. Calling this twice replaces the previous lookup.
// Pass nil to revert to the no-op.
//
// Wiring is post-construction (rather than a New parameter) because
// the operational manager itself needs the bridge's UDP listener to
// drive its session-establishment flows — a chicken-and-egg
// resolution the daemon orchestrates by passing the bridge to the
// manager and the manager to the bridge after both exist.
func (b *Bridge) AttachSessionLookup(lookup SessionLookup) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if lookup == nil {
		b.sessions = noopSessionLookup{}
		return
	}
	b.sessions = lookup
}

// dispatch is the actual receive pipeline, broken out of
// [Bridge.handleDatagram] so tests can drive it without spinning up
// a real UDP listener. Returns the first error encountered for
// observability; the UDP handler discards it after logging.
func (b *Bridge) dispatch(ctx context.Context, buf []byte, src *net.UDPAddr) error {
	// Privacy unmask: when Security Flags carries the P bit, the
	// header bytes from offset 4 onwards (MessageCounter + optional
	// NodeIDs) are XOR-masked with an AES-ECB-derived key (Matter
	// §4.7.3.1). Inbound Privacy frames need the unmask BEFORE
	// UnmarshalHeader can read MessageCounter / NodeIDs. SessionID
	// (bytes 1-2) and Security Flags (byte 3) are unprotected, so we
	// can peek at them first to determine whether to apply the unmask.
	if err := b.maybeUnmaskPrivacy(buf); err != nil {
		// Unmask failures are diagnostic only — drop the datagram.
		// chip-tool retransmits per MRP if it cares.
		b.logger.Debug("matter.rx.privacy_unmask",
			slog.String("src", srcString(src)),
			slog.String("err", err.Error()))
		return err
	}
	hdr, hdrLen, err := message.UnmarshalHeader(buf)
	if err != nil {
		b.logger.Warn("matter.rx.header",
			slog.String("src", srcString(src)),
			slog.Int("bytes", len(buf)),
			slog.String("err", err.Error()))
		return err
	}
	if hdrLen > len(buf) {
		// Defensive — UnmarshalHeader should already fail in this
		// case, but the dispatch loop must never index past the slice.
		err := fmt.Errorf("receive: header consumed %d > %d bytes", hdrLen, len(buf))
		b.logger.Warn("matter.rx.header_overflow", slog.String("err", err.Error()))
		return err
	}
	body := buf[hdrLen:]

	plain, duplicate, err := b.decryptIfNeeded(&hdr, body)
	if err != nil {
		return err
	}
	if len(plain) == 0 {
		// No body after decrypt — every well-formed datagram carries
		// at least a protocol header, so an empty plaintext means the
		// frame was truncated en route or the decrypt accepted a
		// zero-length ciphertext. Drop silently; counterparts will
		// retransmit per MRP if they care.
		return nil
	}

	proto, _, err := message.UnmarshalProtocolHeader(plain)
	if err != nil {
		b.logger.Warn("matter.rx.protocol_header",
			slog.String("src", srcString(src)),
			slog.String("err", err.Error()))
		return err
	}

	// Reliable inbound traffic: register an ACK obligation. The
	// pump goroutine fires a StandaloneAck if no piggyback opportunity
	// arises within the grace window; sendReply paths discharge the
	// obligation directly when they fire.
	if proto.NeedsAck {
		b.owedInboundAck(src, &hdr, proto)
	}

	// Authentic duplicate (MRP retransmit): we already ran the IM
	// handler when the original arrived. Re-running would corrupt
	// fabric-scoped state (e.g. AddNOC twice) — but the peer is
	// retransmitting because it never saw our previous ack, so we
	// MUST send a fresh StandaloneAck for THIS counter. The pump
	// goroutine fires it per-exchange via owedInboundAck above; flush
	// the pump immediately so the ack hits the wire before the peer's
	// next retransmit, then drop processing.
	if duplicate {
		b.RunAckPumpOnce(time.Now())
		b.logger.Debug("matter.rx.duplicate_acked",
			slog.String("src", srcString(src)),
			slog.Int("session_id", int(hdr.SessionID)),
			slog.Int("exchange_id", int(proto.ExchangeID)),
			slog.Uint64("counter", uint64(hdr.MessageCounter)))
		return nil
	}

	// Inbound piggyback ACK: clear any reliable outbound message we
	// shipped whose MessageCounter matches AckCounter (Matter §4.12.3).
	// Applies to both IM and SC datagrams; the SC dispatch also
	// runs an exchange-id-keyed Discharge for the legacy AckHandler
	// (those two layers track different obligations). Successful
	// ACK also releases the counter→subscription mapping so the
	// auto-close path can't fire on a since-completed report.
	if proto.HasAck {
		b.mu.RLock()
		outbound := b.outboundReliable
		b.mu.RUnlock()
		if outbound != nil && outbound.Ack(proto.AckCounter) {
			b.releaseReportCounter(proto.AckCounter)
		}
	}

	// Strip the protocol header so the per-protocol handler gets the
	// payload only.
	bodyOffset := protocolHeaderSize(proto)
	if bodyOffset > len(plain) {
		err := fmt.Errorf("receive: protocol header consumed %d > %d bytes", bodyOffset, len(plain))
		b.logger.Warn("matter.rx.protocol_overflow", slog.String("err", err.Error()))
		return err
	}
	payload := plain[bodyOffset:]

	switch proto.ProtocolID {
	case im.InteractionModelProtocolID:
		return b.handleIMOpcode(ctx, src, &hdr, proto, payload)
	case mrp.SecureChannelProtocolID:
		//nolint:contextcheck // secure-channel handshake (PASE/CASE) runs on the bridge handler context internally; the per-datagram dispatch + test harness keep a ctx-free signature
		return b.dispatchSecureChannel(src, &hdr, proto, payload)
	default:
		err := fmt.Errorf("%w: 0x%04X", ErrUnknownProtocol, proto.ProtocolID)
		b.logger.Debug("matter.rx.unknown_protocol",
			slog.String("src", srcString(src)),
			slog.Int("protocol_id", int(proto.ProtocolID)),
			slog.Int("opcode", int(proto.Opcode)),
			slog.String("err", err.Error()))
		return err
	}
}

// decryptIfNeeded returns the plaintext body for the datagram. For
// SessionID==0 the body is already plaintext. For non-zero SessionID
// we look up the session and decrypt; misses surface
// [ErrSessionMissing] and the caller drops the datagram.
// decryptIfNeeded returns plain, duplicate, err.
// duplicate=true means the frame authenticated successfully but its
// counter has already been seen (MRP retransmit). Caller must
// extract the ProtocolHeader from `plain` and ack the duplicate to
// stop the peer's retransmit storm — but skip handler dispatch.
func (b *Bridge) decryptIfNeeded(hdr *message.Header, body []byte) (plaintext []byte, duplicate bool, err error) {
	if hdr.SessionID == 0 {
		return body, false, nil
	}
	b.mu.RLock()
	lookup := b.sessions
	b.mu.RUnlock()
	if lookup == nil {
		lookup = noopSessionLookup{}
	}
	sess, ok := lookup.Lookup(hdr.SessionID)
	if !ok {
		err := fmt.Errorf("%w: id=%d", ErrSessionMissing, hdr.SessionID)
		b.logger.Debug("matter.rx.session_miss",
			slog.Int("session_id", int(hdr.SessionID)),
			slog.String("err", err.Error()))
		// Burst-detector emits a single INFO row when the same
		// session-id arrives repeatedly within a short window — the
		// classic fingerprint of an iPhone holding a stale
		// MTRDeviceController SecureSession after a RemoveFabric. The
		// remediation is operator-visible (reboot the iPhone) because
		// the daemon already closed the session as part of
		// RemoveFabric; there is no further wire action that can clear
		// Apple's local cache. matter.js and chip behave identically on
		// the wire (silent drop); only the diagnostic differs.
		if b.sessionMissTracker.record(hdr.SessionID, time.Now()) {
			b.logger.Info("matter.rx.session_miss.burst",
				slog.Int("session_id", int(hdr.SessionID)),
				slog.String("hint", "controller is retransmitting on a session-id the bridge no longer holds — typical fingerprint of Apple iPhone stale-session cache after RemoveFabric; remediation is to reboot the iPhone"))
		}
		return nil, false, err
	}
	plain, duplicate, err := sess.Decrypt(hdr, securityFlagsByte(hdr), body)
	if err != nil {
		// Log at DEBUG when the failure looks like a stale-session
		// retry (Apple Home re-emits old MRP retransmissions on a
		// previously-known session-id for ~30 s after the controller
		// clears the bridge from iCloud-Heim). Spec-compliant response
		// is a Secure-Channel CLOSE_SESSION StatusReport, but the
		// authenticated exchange ID lives inside the body we can no
		// longer decrypt — we cannot answer cleanly. Downgrading the
		// log keeps the operator focused on real failures while the
		// MRP retries fade out naturally.
		b.logger.Debug("matter.rx.decrypt",
			slog.Int("session_id", int(hdr.SessionID)),
			slog.String("err", err.Error()))
		return nil, false, err
	}
	return plain, duplicate, nil
}

// handleIMOpcode fans out by opcode into the IM Handle* surfaces and
// ships the structured response back via [Bridge.sendReply]. Errors
// at any stage are logged and the datagram is dropped; the
// commissioner retries per MRP.
//
// The mapping from request opcode → response opcode follows Matter
// §10.5 Table 18:
//   - ReadRequest      → ReportData
//   - WriteRequest     → WriteResponse
//   - InvokeRequest    → InvokeResponse
//   - SubscribeRequest → ReportData (initial-report) + a SubscribeResponse
//     once the report pump catches up. v1.1 ships the initial empty
//     ReportData; the SubscribeResponse + ongoing pump are tracked
//     separately.
//   - TimedRequest     → StatusResponse(Success); a per-exchange
//     deadline is stamped into `Bridge.timedDeadlines` and the
//     matching follow-up Write/Invoke is gated against it via
//     `Bridge.checkTimedGate` per Matter §8.7.
func (b *Bridge) handleIMOpcode(ctx context.Context, src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader, payload []byte) error { //nolint:gocognit,gocyclo,funlen // wire/dispatch table over many attribute/opcode cases
	// StatusResponse is the spec-mandated ACK for a ReportData /
	// SubscribeResponse / Invoke / Write reply we sent earlier (Matter
	// §8.6.2). Apple Home, Google Home, and chip-tool all emit it
	// after consuming our reply; silently absorbing it satisfies the
	// MRP / Reliable Messaging contract without making the dispatcher
	// see a bogus "request". Without this branch the commissioner
	// retransmits its previous request indefinitely after pairing,
	// which Apple eventually surfaces as "device added" → immediate
	// disconnect.
	if proto.Opcode == im.OpcodeStatusResponse {
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
		b.signalStatusResponseRX(proto.ExchangeID)
		b.logger.Debug("matter.rx.im.status_ack",
			slog.String("src", srcString(src)),
			slog.Int("exchange", int(proto.ExchangeID)))
		return nil
	}
	if !im.IsRequestOpcode(proto.Opcode) {
		err := fmt.Errorf("%w: opcode=0x%02X", ErrUnsupportedOpcode, proto.Opcode)
		b.logger.Debug("matter.rx.im.unsupported",
			slog.String("src", srcString(src)),
			slog.Int("opcode", int(proto.Opcode)),
			slog.String("err", err.Error()))
		return err
	}

	// Group-session reject guard. Matter §8.5.7: only Write- and
	// Invoke-Requests are valid over Secure Group sessions; Read,
	// Subscribe, and Timed requests are unicast-only and must be
	// rejected with StatusResponse(InvalidAction).
	//
	// Mirrors matter.js packages/protocol/src/interaction/
	// InteractionMessenger.ts:240,269,287 — ReadRequest /
	// SubscribeRequest / TimedRequest each throw a
	// StatusResponseError(InvalidAction, "<op> is not supported in
	// group sessions"). Without this guard, a malicious group sender
	// could enumerate the bridge's attribute tree via a single
	// multicast Read.
	if requestHdr.SessionType == message.SessionGroup {
		switch proto.Opcode {
		case im.OpcodeReadRequest, im.OpcodeSubscribeRequest, im.OpcodeTimedRequest:
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
			b.dischargeOwedAck(proto.ExchangeID)
			return nil
		}
	}

	dispatcher := b.Dispatcher()
	if dispatcher == nil {
		b.logger.Debug("matter.rx.im.no_dispatcher",
			slog.String("src", srcString(src)),
			slog.Int("opcode", int(proto.Opcode)))
		return nil
	}

	dec := tlv.NewDecoder(payload)
	switch proto.Opcode {
	case im.OpcodeReadRequest:
		req, err := im.UnmarshalReadRequestTLV(dec)
		if err != nil {
			b.logger.Warn("matter.rx.im.read_decode", slog.String("err", err.Error()))
			return err
		}
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
			waitCh := b.armStatusResponseWait(proto.ExchangeID)
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
				b.disarmStatusResponseWait(proto.ExchangeID)
				debugReplyError(b.logger, "send_report", src, err)
				return err
			}
			select {
			case <-waitCh:
				b.disarmStatusResponseWait(proto.ExchangeID)
			case <-time.After(perChunkStatusRespTimeout):
				b.disarmStatusResponseWait(proto.ExchangeID)
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
		b.dischargeOwedAck(proto.ExchangeID)
		b.logger.Debug("matter.rx.im.read",
			slog.String("src", srcString(src)),
			slog.Int("attribute_reports", len(report.Reports)),
			slog.Int("chunks", len(chunks)))
	case im.OpcodeWriteRequest:
		req, err := im.UnmarshalWriteRequestTLV(dec, attributeValueReader)
		if err != nil {
			b.logger.Warn("matter.rx.im.write_decode", slog.String("err", err.Error()))
			return err
		}
		for _, w := range req.Writes {
			b.logger.Debug("matter.rx.im.write_path",
				slog.String("src", srcString(src)),
				slog.Any("endpoint", w.Path.Endpoint),
				slog.Any("cluster", w.Path.Cluster),
				slog.Any("attribute", w.Path.Attribute),
				slog.String("value_type", fmt.Sprintf("%T", w.Value.Value)))
		}
		if status, gated := b.checkTimedGate(req.TimedRequest, requestHdr.SessionID, proto.ExchangeID); gated {
			return b.replyTimedStatus(src, requestHdr, proto, "write", status)
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
			b.dischargeOwedAck(proto.ExchangeID)
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
		if err := b.sendReply(src, requestHdr, proto, im.OpcodeWriteResponse, body); err != nil {
			debugReplyError(b.logger, "send_write", src, err)
			return err
		}
		b.dischargeOwedAck(proto.ExchangeID)
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
	case im.OpcodeInvokeRequest:
		req, err := im.UnmarshalInvokeRequestTLV(dec, commandFieldsReader)
		if err != nil {
			b.logger.Warn("matter.rx.im.invoke_decode", slog.String("err", err.Error()))
			return err
		}
		if status, gated := b.checkTimedGate(req.TimedRequest, requestHdr.SessionID, proto.ExchangeID); gated {
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
		if err := b.sendReply(src, requestHdr, proto, im.OpcodeInvokeResponse, body); err != nil {
			debugReplyError(b.logger, "send_invoke", src, err)
			return err
		}
		b.dischargeOwedAck(proto.ExchangeID)
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
	case im.OpcodeSubscribeRequest:
		req, err := im.UnmarshalSubscribeRequestTLV(dec)
		if err != nil {
			b.logger.Warn("matter.rx.im.subscribe_decode", slog.String("err", err.Error()))
			return err
		}
		if err := b.handleSubscribeRequest(ctx, src, requestHdr, proto, req); err != nil {
			return err
		}
	case im.OpcodeTimedRequest:
		// TimedRequest gates a follow-up Write / Invoke against a
		// per-exchange deadline (Matter §8.7). The bridge captures
		// the deadline in `timedDeadlines` so the next Write / Invoke
		// on the same exchange can be checked against it; expired
		// or missing-prior-TimedRequest cases are rejected via
		// StatusResponse with the spec-mandated codes.
		req, err := im.UnmarshalTimedRequestTLV(dec)
		if err != nil {
			b.logger.Warn("matter.rx.im.timed_decode", slog.String("err", err.Error()))
			return err
		}
		deadline := time.Now().Add(time.Duration(req.TimeoutMs) * time.Millisecond)
		// Key on (sessionID, exchangeID) so a different session cannot
		// consume a deadline registered by another session.
		b.timedDeadlines.Store(timedKey{sessionID: requestHdr.SessionID, exchangeID: proto.ExchangeID}, deadline)
		body, err := EncodeStatusResponse(im.StatusResponse{Status: im.StatusSuccess})
		if err != nil {
			debugReplyError(b.logger, "encode_status", src, err)
			return err
		}
		if err := b.sendReply(src, requestHdr, proto, im.OpcodeStatusResponse, body); err != nil {
			debugReplyError(b.logger, "send_status", src, err)
			return err
		}
		b.dischargeOwedAck(proto.ExchangeID)
		b.logger.Debug("matter.rx.im.timed",
			slog.String("src", srcString(src)),
			slog.Int("timeout_ms", int(req.TimeoutMs)))
	}
	return nil
}

// checkTimedGate enforces the Matter §8.7 timed-interaction rules on
// a Write / Invoke. It returns (status, true) when the request must
// be rejected — caller emits StatusResponse(status) instead of
// processing — or (0, false) when the request can proceed (the
// deadline, if any, has been consumed and removed from the map).
//
//   - timedFlag=false, no prior TimedRequest      → proceed (untimed).
//   - timedFlag=false, prior TimedRequest pending → reject with
//     TIMED_REQUEST_MISMATCH (0xC9). A TimedRequest preceded this
//     Write/Invoke yet the request's own Timed flag is clear — the
//     two disagree, which the spec forbids in BOTH directions.
//   - timedFlag=true,  no prior TimedRequest      → reject with
//     NEEDS_TIMED_INTERACTION (0xC6).
//   - timedFlag=true,  prior expired              → reject with
//     TIMEOUT (0x94); deadline cleared so a retry would re-prime.
//   - timedFlag=true,  prior valid                → proceed; deadline
//     cleared so a duplicate Write/Invoke after this re-tests as
//     "no prior TimedRequest".
//
// Mirrors chip src/app/WriteHandler.cpp:669-673 (and the matching
// CommandHandler path): a Write/Invoke whose own Timed flag is clear
// while a TimedRequest preceded it is a TIMED_REQUEST_MISMATCH — the
// two disagree, which the spec forbids. The inverse half (Timed flag
// set, no prior TimedRequest) stays NEEDS_TIMED_INTERACTION.
func (b *Bridge) checkTimedGate(timedFlag bool, sessionID, exchangeID uint16) (im.StatusCode, bool) {
	raw, ok := b.timedDeadlines.LoadAndDelete(timedKey{sessionID: sessionID, exchangeID: exchangeID})
	if !timedFlag {
		if ok {
			// A TimedRequest opened a window but this request's Timed flag
			// is clear: the two disagree → mismatch. LoadAndDelete already
			// cleared the stale deadline.
			return im.StatusTimedRequestMismatch, true
		}
		// No prior deadline: a plain untimed request, proceed.
		return 0, false
	}
	if !ok {
		return im.StatusNeedsTimedInteraction, true
	}
	deadline, isTime := raw.(time.Time)
	if !isTime || time.Now().After(deadline) {
		return im.StatusTimeout, true
	}
	return 0, false
}

// NotifyDeviceReachable fires the §9.13.6 BridgedDeviceBasicInformation
// ReachableChanged event for every bridged endpoint backed by the given
// CCU device (centralName + deviceAddress) when its availability flips.
//
// The attribute itself self-heals — cluster servers are reconstructed
// per dispatch and read dev.Available() live (see endpoint/materialize.go)
// — but a Read alone does not push the change to an active subscription.
// This method supplies the event half: the daemon subscribes to the
// central's device-availability lifecycle signal and calls in here so
// subscribed commissioners (Apple Home, Google Home) learn about a CCU
// device dropping offline without polling.
//
// Mirrors matter.js BridgedDeviceBasicInformationServer, where mutating
// `reachable` triggers `events.reachableChanged.emit({ reachableNewValue })`.
// We route the same event through the bridge's MatterEmitEvent pipeline,
// addressed to each matching bridged endpoint.
func (b *Bridge) NotifyDeviceReachable(centralName, deviceAddress string, reachable bool) {
	if b == nil {
		return
	}
	topo := b.Topology()
	if topo == nil {
		return
	}
	for _, ep := range topo.Bridged() {
		if ep == nil {
			continue
		}
		if ep.SourceKey.CentralName != centralName || ep.SourceKey.DeviceAddress != deviceAddress {
			continue
		}
		b.MatterEmitEvent(ep.ID,
			core.BridgedDeviceBasicInformationClusterID,
			core.EventReachableChanged,
			core.ReachableChangedEvent{ReachableNewValue: reachable},
			interfaces.MatterEventPriorityCritical)
		b.logger.Debug("matter.bridge.reachable_changed",
			slog.Int("endpoint_id", int(ep.ID)),
			slog.String("central", centralName),
			slog.String("address", deviceAddress),
			slog.Bool("reachable", reachable))
	}
}

// replyTimedStatus is the rejection path for a checkTimedGate hit. It
// emits a StatusResponse with the supplied code and discharges the
// owed ACK so the commissioner doesn't retransmit.
func (b *Bridge) replyTimedStatus(src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader, stage string, status im.StatusCode) error {
	body, err := EncodeStatusResponse(im.StatusResponse{Status: status})
	if err != nil {
		debugReplyError(b.logger, "encode_timed_"+stage, src, err)
		return err
	}
	if err := b.sendReply(src, requestHdr, proto, im.OpcodeStatusResponse, body); err != nil {
		debugReplyError(b.logger, "send_timed_"+stage, src, err)
		return err
	}
	b.dischargeOwedAck(proto.ExchangeID)
	b.logger.Debug("matter.rx.im.timed_reject",
		slog.String("src", srcString(src)),
		slog.String("stage", stage),
		slog.String("status", status.String()))
	return nil
}

// protocolHeaderSize returns the wire size of a decoded
// ProtocolHeader. Matches the encode-side layout in
// [message.ProtocolHeader.Marshal].
func protocolHeaderSize(p message.ProtocolHeader) int {
	size := 6 // flags + opcode + exchange-id + protocol-id
	if p.HasVendorID {
		size += 2
	}
	if p.HasAck {
		size += 4
	}
	if p.HasSecuredExt {
		size += 2 + len(p.SecuredExtension) // uint16 length prefix + block
	}
	return size
}

// securityFlagsByte returns the Security-Flags byte associated with
// hdr, reconstructed from the typed fields the message package
// exposes. Mirrors the encode-side bit layout.
func securityFlagsByte(hdr *message.Header) uint8 {
	var b uint8
	b |= uint8(hdr.SessionType) & 0x1F //nolint:gosec // 5-bit field per spec
	if hdr.Privacy {
		b |= 0x80
	}
	if hdr.Control {
		b |= 0x40
	}
	if hdr.HasExtension {
		b |= 0x20
	}
	return b
}

// srcString defends against nil src — the udp.Handler signature
// allows it on certain error paths.
func srcString(src *net.UDPAddr) string {
	if src == nil {
		return "<nil>"
	}
	return src.String()
}
