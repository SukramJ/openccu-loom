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
	// MUST send a fresh StandaloneAck for THIS counter, and
	// immediately: the obligation owedInboundAck registered above
	// carries the piggyback grace window, behind which a duplicate
	// ack must not hide (the peer would retransmit again meanwhile).
	// Expedite it to due-now, flush the pump, drop processing.
	// Mirrors matter.js MessageExchange.ts:428-433 (duplicate +
	// requiresAck → sendStandaloneAckForMessage without delay).
	if duplicate {
		b.expediteDuplicateAck(hdr.SessionID, proto.ExchangeID)
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

	// Interaction Model (0x0001) and Secure Channel (0x0000) are
	// Common-vendor protocols. A datagram that carries a vendor id (the
	// exchange V flag, VendorID != Common 0x0000) belongs to a
	// vendor-specific protocol the bridge does not implement — even if its
	// low 16-bit protocol id collides with IM/SC. matter.js keys dispatch on
	// the full 32-bit id (vendorId*0x10000 + protocolId,
	// MessageCodec.ts:377), so a vendor-qualified protocol never routes into
	// the common handlers; reject it before the switch rather than feeding a
	// forged frame into the PASE/IM machinery.
	if proto.HasVendorID && proto.VendorID != 0 {
		err := fmt.Errorf("%w: vendor=0x%04X protocol=0x%04X", ErrUnknownProtocol, proto.VendorID, proto.ProtocolID)
		b.logger.Debug("matter.rx.vendor_protocol",
			slog.String("src", srcString(src)),
			slog.Int("vendor_id", int(proto.VendorID)),
			slog.Int("protocol_id", int(proto.ProtocolID)))
		return err
	}

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
		// Unsecured (PASE / pre-fabric) traffic. Detect MRP retransmits per
		// source node id so a duplicate Pake1/Pake3 is acked without
		// re-invoking the handshake handler, mirroring matter.js
		// UnsecuredSession's MessageReceptionState
		// (packages/protocol/src/session/UnsecuredSession.ts). Without a
		// source node id there is nothing stable to key on — treat as fresh
		// (the handshake handler's own state-replay guard still catches it).
		if hdr.HasSourceNodeID && hdr.SourceNodeID != 0 {
			raw, _ := b.unsecuredWindows.LoadOrStore(hdr.SourceNodeID, mrp.NewWindow())
			if w, ok := raw.(*mrp.Window); ok && !w.Accept(hdr.MessageCounter) {
				return body, true, nil
			}
		}
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
func (b *Bridge) handleIMOpcode(ctx context.Context, src *net.UDPAddr, requestHdr *message.Header, proto message.ProtocolHeader, payload []byte) error {
	switch classifyIMOpcode(proto.Opcode, requestHdr.SessionType) {
	case imGateProceed:
		// fall through to decode + dispatch below
	case imGateAbsorbStatusResp:
		return b.absorbStatusResponse(src, requestHdr, proto)
	case imGateRejectUnsupported:
		return b.rejectUnsupportedOpcode(src, proto)
	case imGateRejectGroupSession:
		return b.rejectGroupSession(src, requestHdr, proto)
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
		return b.dispatchReadRequest(ctx, src, requestHdr, proto, dispatcher, req)
	case im.OpcodeWriteRequest:
		req, err := im.UnmarshalWriteRequestTLV(dec, attributeValueReader)
		if err != nil {
			b.logger.Warn("matter.rx.im.write_decode", slog.String("err", err.Error()))
			return err
		}
		return b.dispatchWriteRequest(ctx, src, requestHdr, proto, dispatcher, req)
	case im.OpcodeInvokeRequest:
		req, err := im.UnmarshalInvokeRequestTLV(dec, commandFieldsReader)
		if err != nil {
			b.logger.Warn("matter.rx.im.invoke_decode", slog.String("err", err.Error()))
			return err
		}
		return b.dispatchInvokeRequest(ctx, src, requestHdr, proto, dispatcher, req)
	case im.OpcodeSubscribeRequest:
		req, err := im.UnmarshalSubscribeRequestTLV(dec)
		if err != nil {
			b.logger.Warn("matter.rx.im.subscribe_decode", slog.String("err", err.Error()))
			return err
		}
		return b.handleSubscribeRequest(ctx, src, requestHdr, proto, req)
	case im.OpcodeTimedRequest:
		req, err := im.UnmarshalTimedRequestTLV(dec)
		if err != nil {
			b.logger.Warn("matter.rx.im.timed_decode", slog.String("err", err.Error()))
			return err
		}
		return b.dispatchTimedRequest(src, requestHdr, proto, req)
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
	mgr := b.subscriptionManagerLocked()
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
		// Also dirty the Reachable ATTRIBUTE (BDBI 0x0039 / 0x0011), not
		// just fire the event: a controller subscribed to the attribute
		// (Google Home tracks it) otherwise shows stale reachability until
		// it re-subscribes. matter.js's reactive state marks the attribute
		// dirty on the same change. Mirrors the Descriptor.PartsList
		// dirty-mark pattern in bridge.go.
		if mgr != nil {
			mgr.OnAttributeChanged(im.ConcreteAttributePath{
				Endpoint:     ep.ID,
				Cluster:      core.BridgedDeviceBasicInformationClusterID,
				Attribute:    0x0011, // Reachable (§9.13.5)
				HasEndpoint:  true,
				HasCluster:   true,
				HasAttribute: true,
			})
		}
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
	b.dischargeOwedAck(requestHdr.SessionID, proto.ExchangeID)
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
	b |= uint8(hdr.SessionType&0xFF) & 0x1F
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
