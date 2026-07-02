// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	mattercore "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/core"
	matterlock "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/lock"
	mattermeasure "github.com/SukramJ/openccu-loom/internal/north/matter/cluster/measurement"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// Errors surfaced by the reply path. They are not returned to the
// UDP handler (which has no error channel); the bridge logs them and
// drops the reply, leaving the commissioner to retry per MRP.
var (
	// ErrReplyEncode wraps any TLV-encoding failure when assembling
	// the response payload.
	ErrReplyEncode = errors.New("reply: encode")
	// ErrReplyEncrypt wraps any AES-CCM seal failure when sealing the
	// response for an encrypted session.
	ErrReplyEncrypt = errors.New("reply: encrypt")
	// ErrReplySend wraps any UDP transmit failure.
	ErrReplySend = errors.New("reply: send")
)

// nextUnsecuredCounter returns a fresh per-bridge monotonic counter
// for SessionID==0 (unsecured) replies. Starts at 1 so the first
// reply ever emitted carries counter=1; wraps freely past 2^32 per
// Matter §4.5.4.4 (the unsecured peer is a transient PASE-only
// commissioner that never reaches counter exhaustion in practice).
func (b *Bridge) nextUnsecuredCounter() uint32 {
	return b.unsecuredCounter.Add(1)
}

// sendReply is a thin wrapper around [Bridge.sendReplyOpts] that
// keeps the v1.1 best-effort default (NeedsAck=false, no MRP
// tracking). Use [Bridge.sendReplyReliable] when an IM response
// must survive a single packet drop — see that method's docstring
// for the obligation contract.
func (b *Bridge) sendReply(
	src *net.UDPAddr,
	requestHdr *message.Header,
	requestProto message.ProtocolHeader,
	responseOpcode uint8,
	responsePayload []byte,
) error {
	return b.sendReplyOpts(src, requestHdr, requestProto, responseOpcode, responsePayload, false)
}

// sendReplyReliable is the MRP-tracked variant of [Bridge.sendReply].
// The reply is shipped with NeedsAck=true and registered with the
// outbound-reliable tracker, so the ACK pump retransmits it until
// the peer ACKs or the retransmit cap fires.
//
// Caller contract: the reply must idempotently encode the same
// IM-level result on every retransmit (fresh attribute reads, fresh
// timestamps, etc. are NOT allowed) — otherwise the peer might
// observe two divergent successes for the same exchange. v1.1 IM
// responses (status / write-response / invoke-response) are pure
// outcome reports and meet the contract trivially.
//
// When the bridge has no AckTracker wired (test paths) the call
// degrades silently to the best-effort path.
func (b *Bridge) sendReplyReliable(
	src *net.UDPAddr,
	requestHdr *message.Header,
	requestProto message.ProtocolHeader,
	responseOpcode uint8,
	responsePayload []byte,
) error {
	return b.sendReplyOpts(src, requestHdr, requestProto, responseOpcode, responsePayload, true)
}

// sendReplyOpts assembles + (optionally encrypts +) ships a single
// response datagram. The request's headers drive header assembly:
//
//   - Message: SessionID inherited from request; MessageCounter is a
//     fresh per-session value (Session.Encrypt allocates for
//     encrypted sessions; bridge counter for unsecured).
//   - Protocol: ExchangeID inherited from request; ProtocolID
//     inherited; Opcode is the response opcode; Initiator flipped
//     (we are responding to the peer's exchange-opener); HasAck=true
//     with the request's MessageCounter (acknowledges the request);
//     NeedsAck mirrors `reliable` and only takes effect when the
//     outbound MRP tracker is wired — without a tracker, NeedsAck
//     would block the peer on a reply that nothing rebroadcasts on
//     loss.
//
// When src is nil the function returns ErrReplySend without
// attempting to write — the receive pipeline never invents a src on
// the response path, so a nil src here is a programming error.
func (b *Bridge) sendReplyOpts(
	src *net.UDPAddr,
	requestHdr *message.Header,
	requestProto message.ProtocolHeader,
	responseOpcode uint8,
	responsePayload []byte,
	reliable bool,
) error {
	b.mu.RLock()
	listener := b.listener
	sessions := b.sessions
	tracker := b.outboundReliable
	b.mu.RUnlock()
	if listener == nil {
		return fmt.Errorf("%w: listener nil", ErrReplySend)
	}
	if src == nil {
		return fmt.Errorf("%w: src nil", ErrReplySend)
	}

	// Engage MRP only when both the caller asked for it AND the bridge
	// has a tracker wired — degrading to best-effort on a missing
	// tracker keeps test fixtures (which never wire one) functional.
	needsAck := reliable && tracker != nil

	respProto := message.ProtocolHeader{
		Initiator:  !requestProto.Initiator,
		HasAck:     true,
		AckCounter: requestHdr.MessageCounter,
		Opcode:     responseOpcode,
		ExchangeID: requestProto.ExchangeID,
		ProtocolID: requestProto.ProtocolID,
		NeedsAck:   needsAck,
	}
	protoBytes := respProto.Marshal()

	// Body = ProtocolHeader || responsePayload. Encryption (when
	// enabled) seals the body with the request's MessageHeader as
	// AAD; for unsecured the body is shipped verbatim.
	body := append(protoBytes, responsePayload...) //nolint:gocritic // small, single-allocation join

	respHdr := message.Header{
		// SessionID is filled below — for unsecured (==0) it stays 0;
		// for encrypted we look up the session and stamp the peer's
		// view of the SessionID (PeerSessionID) so the peer's table
		// can resolve it. Stamping our own local id (which is what
		// requestHdr.SessionID carries — peer sent that to address
		// us) results in chip-tool dropping the reply silently.
		Privacy:      requestHdr.Privacy,
		Control:      requestHdr.Control,
		HasExtension: false,
		SessionType:  requestHdr.SessionType,
	}
	// Mirror node-id routing: peer is ALWAYS the destination, we
	// (the bridge) are the source. chip-tool's commissioner sets
	// HasSourceNodeID=true on inbound PASE messages with a random
	// non-zero ephemeral NodeID; the bridge's reply MUST echo it as
	// DestNodeID with DestSize=DestNodeID, otherwise chip-tool drops
	// the datagram with "malformed unsecure packet with source
	// 0x...0 destination 0x...0". Per Matter §4.4.1.2.
	//
	// For SECURE unicast (SessionID > 0) Matter §4.4.1.4 says peers
	// resolve identity via the SessionID alone — the Source / Dest
	// NodeID fields are typically NOT set on encrypted traffic, and
	// echoing them confuses chip-tool's secure-receive validator.
	// Restrict the echo to the unsecured pre-PASE path.
	if requestHdr.SessionID == 0 && requestHdr.HasSourceNodeID {
		respHdr.DestSize = message.DestNodeID
		respHdr.DestNodeID = requestHdr.SourceNodeID
	}
	// Bridge's source-node-id is its own ephemeral until CASE
	// installs an operational identity. For SessionID==0 (PASE
	// pre-fabric) we leave HasSourceNodeID=false; the peer doesn't
	// gate on it for unsecured traffic per spec §4.4.1.1.

	if requestHdr.SessionID == 0 {
		respHdr.SessionID = 0
		counter := b.nextUnsecuredCounter()
		respHdr.MessageCounter = counter
		hdrBytes := respHdr.Marshal()
		datagram := append(hdrBytes, body...) //nolint:gocritic // single-allocation join
		if err := listener.Send(src, datagram); err != nil {
			return fmt.Errorf("%w: %w", ErrReplySend, err)
		}
		if needsAck {
			tracker.Track(counter, requestProto.ExchangeID, datagram, src, time.Now())
		}
		return nil
	}

	sess, ok := sessions.Lookup(requestHdr.SessionID)
	if !ok {
		// Session vanished between request and response (rare; usually
		// only happens during a fabric removal that races the
		// in-flight request). Drop the reply.
		return fmt.Errorf("%w: session=%d", ErrReplySend, requestHdr.SessionID)
	}
	// Stamp the peer's view of the SessionID — that's what they
	// expect on inbound. Falls back to requestHdr.SessionID if the
	// peer's id was never captured (legacy CASE path or test
	// fixture); chip-tool will drop in that case but at least the
	// frame is structurally valid.
	respHdr.SessionID = sess.PeerSessionID()
	if respHdr.SessionID == 0 {
		respHdr.SessionID = requestHdr.SessionID
	}
	enc, err := sess.Encrypt(&respHdr, securityFlagsByte(&respHdr), body)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReplyEncrypt, err)
	}
	// Encrypt allocates the message counter from the session's Tx
	// counter; read it back so the retransmit tracker stores the
	// canonical value the peer will see.
	counter := respHdr.MessageCounter
	hdrBytes := respHdr.Marshal()
	datagram := make([]byte, 0, len(hdrBytes)+len(enc.Ciphertext))
	datagram = append(datagram, hdrBytes...)
	datagram = append(datagram, enc.Ciphertext...)
	if err := listener.Send(src, datagram); err != nil {
		return fmt.Errorf("%w: %w", ErrReplySend, err)
	}
	if needsAck {
		tracker.Track(counter, requestProto.ExchangeID, datagram, src, time.Now())
	}
	return nil
}

// EncodeReportData serialises rd into the TLV bytes that ride as the
// IM ReportData payload. Uses [defaultAttributeValueWriter] for
// every attribute value — clusters that need richer value shapes
// (structs, lists) need a richer writer; the v1.1 measurement +
// custom-DP cluster set sticks to scalars.
func EncodeReportData(rd im.ReportData) ([]byte, error) {
	enc := tlv.NewEncoder()
	rd.MarshalTLV(enc, defaultAttributeValueWriter)
	buf, err := enc.Bytes()
	if err != nil {
		return nil, fmt.Errorf("%w: report data: %w", ErrReplyEncode, err)
	}
	return buf, nil
}

// reportChunkPayloadBudget is the per-datagram budget for the
// IM ReportData payload alone — the raw TLV bytes that ride inside
// one Matter message. The IPv6 minimum MTU caps the wire datagram at
// 1280 bytes ([udp.MaxDatagramSize]); subtracting the message header
// (~16 B), protocol header (~8 B), and AES-CCM tag (16 B) plus a bit
// of headroom leaves ~1100 B for the encoded ReportData. Apple Home
// commissioner reads (PartsList, ServerList, FeatureMap, …) routinely
// breach this budget by an order of magnitude, so the read handler
// now splits oversized reports into multiple chunks via
// [chunkReportData].
const reportChunkPayloadBudget = 1100

// chunkReportData splits rd into per-datagram chunks whose encoded
// size stays within budget. All chunks except the last carry
// MoreChunkedMessages=true (Matter §10.6.6); a SuppressResponse=true
// only rides on the final chunk because Matter requires the receiver
// to ACK every intermediate chunk with a StatusResponse before the
// next one can be safely processed.
//
// Greedy fill: each AttributeReport / EventReport is appended one at
// a time; whenever the running encode breaches budget the current
// chunk closes and the entry seeds a fresh chunk. A single oversized
// entry (e.g. a Descriptor.PartsList with 1000+ endpoint IDs) cannot
// be sub-split at this layer — it ships in its own chunk and the
// receiver decides whether to tolerate the over-sized datagram or
// fragment further. v1.1 leaves the in-attribute split to a future
// iteration; controllers we test (Apple Home, chip-tool) accept
// over-budget chunks when no other option exists.
func chunkReportData(rd im.ReportData, budget int) ([]im.ReportData, error) {
	// Fast path: single small report → no work.
	probe, err := EncodeReportData(rd)
	if err != nil {
		return nil, err
	}
	if len(probe) <= budget {
		return []im.ReportData{rd}, nil
	}

	chunks := make([]im.ReportData, 0, 4)
	current := im.ReportData{
		HasSubscription: rd.HasSubscription,
		SubscriptionID:  rd.SubscriptionID,
	}

	addAttributeReport := func(rep im.AttributeReport) error {
		candidate := current
		candidate.Reports = append(candidate.Reports, rep)
		body, err := EncodeReportData(candidate)
		if err != nil {
			return err
		}
		if len(body) > budget && (len(current.Reports) > 0 || len(current.EventReports) > 0) {
			chunks = append(chunks, current)
			current = im.ReportData{
				HasSubscription: rd.HasSubscription,
				SubscriptionID:  rd.SubscriptionID,
				Reports:         []im.AttributeReport{rep},
			}
			return nil
		}
		current = candidate
		return nil
	}
	addEventReport := func(ev im.EventReport) error {
		candidate := current
		candidate.EventReports = append(candidate.EventReports, ev)
		body, err := EncodeReportData(candidate)
		if err != nil {
			return err
		}
		if len(body) > budget && (len(current.Reports) > 0 || len(current.EventReports) > 0) {
			chunks = append(chunks, current)
			current = im.ReportData{
				HasSubscription: rd.HasSubscription,
				SubscriptionID:  rd.SubscriptionID,
				EventReports:    []im.EventReport{ev},
			}
			return nil
		}
		current = candidate
		return nil
	}

	for _, rep := range rd.Reports {
		if err := addAttributeReport(rep); err != nil {
			return nil, err
		}
	}
	for _, ev := range rd.EventReports {
		if err := addEventReport(ev); err != nil {
			return nil, err
		}
	}
	if len(current.Reports) > 0 || len(current.EventReports) > 0 {
		chunks = append(chunks, current)
	}
	if len(chunks) == 0 {
		// Empty input still surfaces as a single (empty) chunk so the
		// receiver sees a valid ReportDataMessage close out the read.
		chunks = []im.ReportData{rd}
	}

	for i := range chunks[:len(chunks)-1] {
		chunks[i].MoreChunkedMessages = true
	}
	if rd.SuppressResponse {
		chunks[len(chunks)-1].SuppressResponse = true
	}
	return chunks, nil
}

// EncodeWriteResponse serialises wr into the TLV bytes that ride as
// the IM WriteResponse payload.
func EncodeWriteResponse(wr im.WriteResponse) ([]byte, error) {
	enc := tlv.NewEncoder()
	wr.MarshalTLV(enc)
	buf, err := enc.Bytes()
	if err != nil {
		return nil, fmt.Errorf("%w: write response: %w", ErrReplyEncode, err)
	}
	return buf, nil
}

// EncodeInvokeResponse serialises ir into the TLV bytes that ride as
// the IM InvokeResponse payload. Uses [defaultCommandFieldsWriter]
// for response payloads — currently emits an empty struct for
// non-status responses (covers the v1.1 cluster set, which surfaces
// status-only commands).
func EncodeInvokeResponse(ir im.InvokeResponse) ([]byte, error) {
	enc := tlv.NewEncoder()
	ir.MarshalTLV(enc, defaultCommandFieldsWriter)
	buf, err := enc.Bytes()
	if err != nil {
		return nil, fmt.Errorf("%w: invoke response: %w", ErrReplyEncode, err)
	}
	return buf, nil
}

// EncodeStatusResponse serialises sr into the TLV bytes that ride
// as the IM StatusResponse payload. The bridge emits this in
// response to a TimedRequest (always Success in v1.1) and could in
// principle reply with one for any IM-action error condition (today
// errors are embedded into the dedicated response shapes).
func EncodeStatusResponse(sr im.StatusResponse) ([]byte, error) {
	enc := tlv.NewEncoder()
	sr.MarshalTLV(enc)
	buf, err := enc.Bytes()
	if err != nil {
		return nil, fmt.Errorf("%w: status response: %w", ErrReplyEncode, err)
	}
	return buf, nil
}

// EncodeSubscribeResponse serialises sr into the TLV bytes for an
// IM SubscribeResponse payload.
func EncodeSubscribeResponse(sr im.SubscribeResponse) ([]byte, error) {
	enc := tlv.NewEncoder()
	sr.MarshalTLV(enc)
	buf, err := enc.Bytes()
	if err != nil {
		return nil, fmt.Errorf("%w: subscribe response: %w", ErrReplyEncode, err)
	}
	return buf, nil
}

// defaultAttributeValueWriter is the wire writer the bridge plugs
// into [im.ReportData.MarshalTLV]. It type-switches on the
// cluster-native Go value and emits the matching TLV element. Null
// or unknown types degrade to a TLV null element so the reply still
// round-trips structurally — controllers see `null` rather than a
// silent drop.
//
// Coverage matches the cluster-native types every cluster server in
// internal/north/matter/cluster/{core,measurement,...} returns from
// MatterRead. Add a case here when a new cluster server starts
// returning a richer type (struct, list, …).
func defaultAttributeValueWriter(enc *tlv.Encoder, tag tlv.Tag, v im.AttributeValue) { //nolint:gocognit,gocyclo,funlen // wire/dispatch table over many attribute/opcode cases
	if v.IsNull || v.Value == nil {
		enc.PutNull(tag)
		return
	}
	// Width selection: each Go uint/int type maps to a Matter spec
	// type with a fixed wire width. matter.js's TlvCodec validates the
	// width (per the cluster schema's typeLength), and Apple Home's
	// HAP service mapper rejects an attribute whose declared type is
	// `uint16`/`uint32` if the encoded TLV uses TypeUnsignedInt1.
	// Root cause: BasicInformation / BridgedDeviceBasicInformation
	// HardwareVersion (uint16) and SoftwareVersion (uint32) values of 1
	// were emitted as 1-byte uints by the magnitude-driven `PutUint(uint64)`
	// path; Apple
	// silently rejects the topology and sends RemoveFabric.
	switch x := v.Value.(type) {
	case bool:
		enc.PutBool(tag, x)
	case uint8:
		enc.PutUint(tag, uint64(x))
	case uint16:
		enc.PutUint16(tag, x)
	case uint32:
		enc.PutUint32(tag, x)
	case uint64:
		enc.PutUint64(tag, x)
	case int8:
		enc.PutInt(tag, int64(x))
	case int16:
		enc.PutInt16(tag, x)
	case int32:
		enc.PutInt32(tag, x)
	case int64:
		enc.PutInt64(tag, x)
	case float32:
		enc.PutFloat32(tag, x)
	case float64:
		enc.PutFloat64(tag, x)
	case string:
		enc.PutUTF8(tag, x)
	case tlv.BoundedString:
		// Cluster servers that declare a maximum byte length for a UTF-8
		// attribute return BoundedString from MatterRead so the encoder
		// applies the Matter-spec bound at encode time (trim + log on
		// overflow). See Encoder.PutUTF8Bounded.
		enc.PutUTF8Bounded(tag, x.Value, x.MaxBytes)
	case []byte:
		enc.PutOctets(tag, x)
	case mattercore.BasicCommissioningInfoStruct:
		// GeneralCommissioning attribute 0x0001 (Matter §11.10.5.3).
		// Two uint16 fields under context tags 0 / 1 — encode at the
		// declared width so the wire shape matches the spec table.
		// PutUint(auto) would have downsized values < 256 to uint8 and
		// the IM decoder rejects the type/length mismatch.
		enc.StartStruct(tag)
		enc.PutUint16(tlv.ContextTag(0), x.FailSafeExpiryLengthSeconds)
		enc.PutUint16(tlv.ContextTag(1), x.MaxCumulativeFailsafeSeconds)
		_ = enc.EndContainer()
	case mattercore.CapabilityMinimaStruct:
		// BasicInformation attribute 0x0013 (Matter §11.1.5.20). Two
		// uint16 fields under context tags 0 / 1.
		enc.StartStruct(tag)
		enc.PutUint16(tlv.ContextTag(0), x.CaseSessionsPerFabric)
		enc.PutUint16(tlv.ContextTag(1), x.SubscriptionsPerFabric)
		_ = enc.EndContainer()
	case mattercore.ProductAppearanceStruct:
		// BasicInformation attribute 0x0014. Finish (tag 0) is a plain
		// enum8; PrimaryColor (tag 1) has Quality "X" (nullable) per the
		// spec element definition. Encode PrimaryColorAbsent (0xFF) as
		// TLV-Null so strict controllers see a well-formed nullable field.
		enc.StartStruct(tag)
		enc.PutUint(tlv.ContextTag(0), uint64(x.Finish))
		if x.PrimaryColor == mattercore.PrimaryColorAbsent {
			enc.PutNull(tlv.ContextTag(1))
		} else {
			enc.PutUint(tlv.ContextTag(1), uint64(x.PrimaryColor))
		}
		_ = enc.EndContainer()
	case []mattercore.AccessControlEntryStruct:
		// AccessControl.acl (Matter §9.10.4.4). TLV array of
		// AccessControlEntryStruct; each entry is a struct with
		// context-tagged fields:
		//   [1] privilege  enum8
		//   [2] auth-mode  enum8
		//   [3] subjects   nullable list of uint64
		//   [4] targets    nullable list of struct{ [0] cluster, [1] endpoint, [2] device-type }
		//   [254] fabric-index uint8
		// Apple Home's first post-CASE read targets this attribute;
		// returning `null` (the previous default-writer fall-through)
		// surfaces as ACCESS_DENIED on every follow-up read and Apple
		// tears the fabric down via RemoveFabric.
		enc.StartArray(tag)
		for _, e := range x {
			enc.StartStruct(tlv.AnonymousTag())
			enc.PutUint(tlv.ContextTag(1), uint64(e.Privilege))
			enc.PutUint(tlv.ContextTag(2), uint64(e.AuthMode))
			if e.Subjects == nil {
				enc.PutNull(tlv.ContextTag(3))
			} else {
				enc.StartArray(tlv.ContextTag(3))
				for _, s := range e.Subjects {
					enc.PutUint(tlv.AnonymousTag(), s)
				}
				_ = enc.EndContainer()
			}
			if e.Targets == nil {
				enc.PutNull(tlv.ContextTag(4))
			} else {
				enc.StartArray(tlv.ContextTag(4))
				for _, t := range e.Targets {
					enc.StartStruct(tlv.AnonymousTag())
					if t.Cluster != nil {
						enc.PutUint(tlv.ContextTag(0), uint64(*t.Cluster))
					} else {
						enc.PutNull(tlv.ContextTag(0))
					}
					if t.Endpoint != nil {
						enc.PutUint(tlv.ContextTag(1), uint64(*t.Endpoint))
					} else {
						enc.PutNull(tlv.ContextTag(1))
					}
					if t.DeviceType != nil {
						enc.PutUint(tlv.ContextTag(2), uint64(*t.DeviceType))
					} else {
						enc.PutNull(tlv.ContextTag(2))
					}
					_ = enc.EndContainer()
				}
				_ = enc.EndContainer()
			}
			enc.PutUint(tlv.ContextTag(254), uint64(e.FabricIndex))
			_ = enc.EndContainer()
		}
		_ = enc.EndContainer()
	case []mattercore.NetworkInterfaceStruct:
		// GeneralDiagnostics.NetworkInterfaces (Matter §11.12.4.1).
		// list of struct; each entry:
		//   [0] Name                            char_string<32>
		//   [1] IsOperational                   bool
		//   [2] OffPremiseServicesReachableIPv4 bool? (nullable)
		//   [3] OffPremiseServicesReachableIPv6 bool? (nullable)
		//   [4] HardwareAddress                 octet_string (6 or 8)
		//   [5] IPv4Addresses                   list[octet_string<4>]
		//   [6] IPv6Addresses                   list[octet_string<16>]
		//   [7] InterfaceType                   enum8
		// Without this case Apple Home logs "No enumeration/topology
		// dictionary found" + "Nil supported link layer types" and
		// tears the fabric down via RemoveFabric ~5 s after
		// Subscribe-Initial. Mirrors matter.js
		// packages/types/src/clusters/general-diagnostics.ts:
		// NetworkInterface.
		enc.StartArray(tag)
		for _, n := range x {
			enc.StartStruct(tlv.AnonymousTag())
			enc.PutUTF8(tlv.ContextTag(0), n.Name)
			enc.PutBool(tlv.ContextTag(1), n.IsOperational)
			if n.OffPremiseServicesReachableIPv4 == nil {
				enc.PutNull(tlv.ContextTag(2))
			} else {
				enc.PutBool(tlv.ContextTag(2), *n.OffPremiseServicesReachableIPv4)
			}
			if n.OffPremiseServicesReachableIPv6 == nil {
				enc.PutNull(tlv.ContextTag(3))
			} else {
				enc.PutBool(tlv.ContextTag(3), *n.OffPremiseServicesReachableIPv6)
			}
			enc.PutOctets(tlv.ContextTag(4), n.HardwareAddress)
			enc.StartArray(tlv.ContextTag(5))
			for _, a := range n.IPv4Addresses {
				enc.PutOctets(tlv.AnonymousTag(), a)
			}
			_ = enc.EndContainer()
			enc.StartArray(tlv.ContextTag(6))
			for _, a := range n.IPv6Addresses {
				enc.PutOctets(tlv.AnonymousTag(), a)
			}
			_ = enc.EndContainer()
			enc.PutUint(tlv.ContextTag(7), uint64(n.InterfaceType))
			_ = enc.EndContainer()
		}
		_ = enc.EndContainer()
	case []mattercore.FabricDescriptorStruct:
		// OperationalCredentials.Fabrics (Matter §11.18.5.6).
		// fabric-sensitive list of struct; each entry:
		//   [1]   RootPublicKey octet_string<65>
		//   [2]   VendorID      vendor_id (uint16)
		//   [3]   FabricID      fabric_id (uint64)
		//   [4]   NodeID        node_id   (uint64)
		//   [5]   Label         char_string<32>
		//   [254] FabricIndex   fabric_idx (uint8)
		// Without this case the writer falls through to `default:` and
		// emits TLV null — Apple Home then sees CommissionedFabrics=1
		// but Fabrics=null, declares the bridge inconsistent, and
		// tears the fabric down via RemoveFabric.
		enc.StartArray(tag)
		for _, f := range x {
			enc.StartStruct(tlv.AnonymousTag())
			enc.PutOctets(tlv.ContextTag(1), f.RootPublicKey)
			enc.PutUint16(tlv.ContextTag(2), f.VendorID)
			enc.PutUint64(tlv.ContextTag(3), f.FabricID)
			enc.PutUint64(tlv.ContextTag(4), f.NodeID)
			enc.PutUTF8(tlv.ContextTag(5), f.Label)
			if len(f.VidVerificationStatement) > 0 {
				enc.PutOctets(tlv.ContextTag(6), f.VidVerificationStatement)
			}
			enc.PutUint(tlv.ContextTag(254), uint64(f.FabricIndex))
			_ = enc.EndContainer()
		}
		_ = enc.EndContainer()
	case []mattercore.NOCStruct:
		// OperationalCredentials.NOCs (Matter §11.18.5.7).
		// fabric-sensitive list of struct:
		//   [1]   NOC          octet_string
		//   [2]   ICAC         octet_string (nullable)
		//   [3]   Vvsc         octet_string (optional; omitted when nil)
		//   [254] FabricIndex  fabric_idx (uint8)
		enc.StartArray(tag)
		for _, n := range x {
			enc.StartStruct(tlv.AnonymousTag())
			enc.PutOctets(tlv.ContextTag(1), n.NOC)
			if len(n.ICAC) == 0 {
				enc.PutNull(tlv.ContextTag(2))
			} else {
				enc.PutOctets(tlv.ContextTag(2), n.ICAC)
			}
			if len(n.Vvsc) > 0 {
				enc.PutOctets(tlv.ContextTag(3), n.Vvsc)
			}
			enc.PutUint(tlv.ContextTag(254), uint64(n.FabricIndex))
			_ = enc.EndContainer()
		}
		_ = enc.EndContainer()
	case [][]byte:
		// OperationalCredentials.TrustedRootCertificates (Matter
		// §11.18.5.x): list of octet_string<400>. Also covers any
		// other list-of-octets attribute.
		enc.StartArray(tag)
		for _, b := range x {
			enc.PutOctets(tlv.AnonymousTag(), b)
		}
		_ = enc.EndContainer()
	case []any:
		// Empty list pass-through — used by AccessControl.extension
		// and any cluster that surfaces an empty-list attribute.
		// Returning `null` here would parse as "missing", whereas an
		// empty array is a present, empty value.
		enc.StartArray(tag)
		for range x {
			// Cannot encode arbitrary `any` items without per-type
			// branches; the typical use case is the empty-extension
			// surface where the list is always 0-length.
		}
		_ = enc.EndContainer()
	case []uint32:
		// list of cluster / attribute / command / event IDs. Covers the
		// universal global attributes the dispatcher synthesises
		// (Matter §7.13.2) — `AttributeList` (0xFFFB),
		// `AcceptedCommandList` (0xFFF9), `GeneratedCommandList`
		// (0xFFF8), `EventList` (0xFFFA) — plus Descriptor.ServerList
		// (0x001D:0x0001) and Descriptor.ClientList (0x001D:0x0002).
		//
		// Spec types: AttributeId / ClusterId / EventId / CommandId are
		// all `TlvUInt32`. Apple Home's MTRDevice IM-decoder is strict
		// on element width — mixing TypeUnsignedInt1 (cluster IDs ≤
		// 0xFF like 0x28 BasicInformation) with TypeUnsignedInt2
		// (globals like 0xFFFC FeatureMap) inside the same Array makes
		// Apple's decoder reject the whole list. Use the explicit-width
		// helper so every entry rides as uint32.
		enc.StartArray(tag)
		for _, id := range x {
			enc.PutUint32(tlv.AnonymousTag(), id)
		}
		_ = enc.EndContainer()
	case []uint16:
		// list of endpoint IDs. Used by Descriptor.PartsList
		// (0x001D:0x0003) — the root endpoint enumerates every bridged
		// endpoint here. Spec type: `TlvEndpointNumber = TlvUInt16`.
		// Same explicit-width rationale as the []uint32 case above.
		enc.StartArray(tag)
		for _, id := range x {
			enc.PutUint16(tlv.AnonymousTag(), id)
		}
		_ = enc.EndContainer()
	case []mattercore.NetworkInfoStruct:
		// NetworkCommissioning.Networks (Matter §11.9.6.2). list of
		// struct{ NetworkID octet_string<32>, Connected bool }.
		// Apple Home reads this immediately after GeneralCommissioning;
		// without an explicit case the writer emits null and Apple's
		// IM-decoder discards every subsequent cluster on the
		// subscribe-initial stream — surfacing as Code=24 HAP build
		// failure even though Subscribe-initial reports continue
		// arriving on the wire.
		enc.StartArray(tag)
		for _, ni := range x {
			enc.StartStruct(tlv.AnonymousTag())
			enc.PutOctets(tlv.ContextTag(0), ni.NetworkID)
			enc.PutBool(tlv.ContextTag(1), ni.Connected)
			_ = enc.EndContainer()
		}
		_ = enc.EndContainer()
	case []mattercore.GroupKeyMapStruct:
		// GroupKeyManagement.GroupKeyMap (Matter §11.2.10.5.1).
		// fabric-sensitive list of struct{ GroupId u16, GroupKeySetId
		// u16, FabricIndex u8 }.
		enc.StartArray(tag)
		for _, m := range x {
			enc.StartStruct(tlv.AnonymousTag())
			enc.PutUint16(tlv.ContextTag(1), m.GroupID)
			enc.PutUint16(tlv.ContextTag(2), m.GroupKeySetID)
			enc.PutUint(tlv.ContextTag(254), uint64(m.FabricIndex))
			_ = enc.EndContainer()
		}
		_ = enc.EndContainer()
	case []mattercore.GroupInfoMapStruct:
		// GroupKeyManagement.GroupTable (Matter §11.2.10.5.2).
		// fabric-sensitive list of struct{ GroupId u16, Endpoints
		// list<u16>, GroupName string, FabricIndex u8 }.
		enc.StartArray(tag)
		for _, m := range x {
			enc.StartStruct(tlv.AnonymousTag())
			enc.PutUint16(tlv.ContextTag(1), m.GroupID)
			enc.StartArray(tlv.ContextTag(2))
			for _, ep := range m.Endpoints {
				enc.PutUint16(tlv.AnonymousTag(), ep)
			}
			_ = enc.EndContainer()
			enc.PutUTF8(tlv.ContextTag(3), m.GroupName)
			enc.PutUint(tlv.ContextTag(254), uint64(m.FabricIndex))
			_ = enc.EndContainer()
		}
		_ = enc.EndContainer()
	case []mattercore.TargetStruct:
		// Binding.Binding (Matter §9.6.5.1). list of struct{ Node u64,
		// Group u16, Endpoint u16, Cluster u32, FabricIndex u8 }.
		enc.StartArray(tag)
		for _, t := range x {
			enc.StartStruct(tlv.AnonymousTag())
			if t.Node != 0 {
				enc.PutUint64(tlv.ContextTag(1), t.Node)
			}
			if t.Group != 0 {
				enc.PutUint16(tlv.ContextTag(2), t.Group)
			}
			if t.Endpoint != 0 {
				enc.PutUint16(tlv.ContextTag(3), t.Endpoint)
			}
			if t.Cluster != 0 {
				enc.PutUint32(tlv.ContextTag(4), t.Cluster)
			}
			enc.PutUint(tlv.ContextTag(254), uint64(t.FabricIndex))
			_ = enc.EndContainer()
		}
		_ = enc.EndContainer()
	case []mattercore.DeviceTypeStruct:
		// Descriptor.DeviceTypeList (Matter §9.5.5.1). list of struct{
		//   [0] DeviceType uint32 (vendor + device-type ID),
		//   [1] Revision   uint16
		// }. Mandatory on every endpoint; Apple Home's HAP-mapper
		// keys on this list to pick the HomeKit service template
		// (e.g. ContactSensor, Thermostat). DeviceType is spec
		// `TlvUInt32`, Revision is `TlvUInt16` — explicit widths
		// required for Apple's strict decoder.
		enc.StartArray(tag)
		for _, dt := range x {
			enc.StartStruct(tlv.AnonymousTag())
			enc.PutUint32(tlv.ContextTag(0), dt.DeviceType)
			enc.PutUint16(tlv.ContextTag(1), dt.Revision)
			_ = enc.EndContainer()
		}
		_ = enc.EndContainer()
	case []mattermeasure.AccuracyStruct:
		// ElectricalPowerMeasurement.Accuracy (0x0090:0x0002) and
		// ElectricalEnergyMeasurement.Accuracy (0x0091:0x0000) per
		// Matter §2.13.6.3 / §2.14.6.1. Each entry is a struct:
		//   [0] MeasurementType  enum16
		//   [1] Measured         bool
		//   [2] MinAccuracy      uint16 (percent-hundredths)
		//   [3] MaxAccuracy      uint16 (percent-hundredths)
		//   [4] AccuracyRanges   list[AccuracyRangeStruct]
		// AccuracyRangeStruct (inner):
		//   [0] RangeMin int64, [1] RangeMax int64
		// matter.js ref: packages/model/src/standard/elements/
		// electrical-power-measurement.element.ts — AccuracyStruct.
		enc.StartArray(tag)
		for _, a := range x {
			enc.StartStruct(tlv.AnonymousTag())
			enc.PutUint16(tlv.ContextTag(0), a.MeasurementType)
			enc.PutBool(tlv.ContextTag(1), a.Measured)
			enc.PutUint16(tlv.ContextTag(2), a.MinAccuracy)
			enc.PutUint16(tlv.ContextTag(3), a.MaxAccuracy)
			enc.StartArray(tlv.ContextTag(4))
			for _, r := range a.AccuracyRanges {
				enc.StartStruct(tlv.AnonymousTag())
				enc.PutInt64(tlv.ContextTag(0), r.RangeMin)
				enc.PutInt64(tlv.ContextTag(1), r.RangeMax)
				_ = enc.EndContainer()
			}
			_ = enc.EndContainer() // AccuracyRanges array
			_ = enc.EndContainer() // AccuracyStruct
		}
		_ = enc.EndContainer() // outer list
	case mattercore.StartUpEvent:
		// BasicInformation §11.1.8.1 — single field SoftwareVersion (uint32).
		// matter.js packages/model/src/standard/elements/basic-information.element.ts:84-90.
		enc.StartStruct(tag)
		enc.PutUint32(tlv.ContextTag(0), x.SoftwareVersion)
		_ = enc.EndContainer()
	case mattercore.ShutDownEvent:
		// BasicInformation §11.1.8.2 — no fields. Empty struct keeps the
		// EventDataIB.Data slot at tag 7 well-formed for chip-tool's
		// StructDecodeIterator.
		enc.StartStruct(tag)
		_ = enc.EndContainer()
	case mattercore.LeaveEvent:
		// BasicInformation §11.1.8.3 — single field FabricIndex (uint8).
		// matter.js basic-information.element.ts:96-105.
		enc.StartStruct(tag)
		enc.PutUint(tlv.ContextTag(0), uint64(x.FabricIndex))
		_ = enc.EndContainer()
	case mattercore.BootReasonEvent:
		// GeneralDiagnostics §11.12.8.1 — single field BootReason (enum8).
		enc.StartStruct(tag)
		enc.PutUint(tlv.ContextTag(0), uint64(x.BootReason))
		_ = enc.EndContainer()
	case mattercore.ReachableChangedEvent:
		// BridgedDeviceBasicInformation §9.13.6.1 — single field
		// ReachableNewValue (bool).
		enc.StartStruct(tag)
		enc.PutBool(tlv.ContextTag(0), x.ReachableNewValue)
		_ = enc.EndContainer()
	case matterlock.LockOperationEvent:
		// DoorLock §5.2.10.3 LockOperation. Field tags per matter.js
		// door-lock-cluster.element.ts:181-195:
		//   [0] LockOperationType enum8
		//   [1] OperationSource   enum8
		//   [2] UserIndex         uint16 nullable
		//   [3] FabricIndex       fabric-idx nullable
		//   [4] SourceNode        node-id nullable
		// Credentials [5] is USR-gated and absent (no USR feature).
		enc.StartStruct(tag)
		enc.PutUint(tlv.ContextTag(0), uint64(x.LockOperationType))
		enc.PutUint(tlv.ContextTag(1), uint64(x.OperationSource))
		if x.UserIndex != nil {
			enc.PutUint16(tlv.ContextTag(2), *x.UserIndex)
		} else {
			enc.PutNull(tlv.ContextTag(2))
		}
		if x.FabricIndex != nil {
			enc.PutUint(tlv.ContextTag(3), uint64(*x.FabricIndex))
		} else {
			enc.PutNull(tlv.ContextTag(3))
		}
		if x.SourceNode != nil {
			enc.PutUint64(tlv.ContextTag(4), *x.SourceNode)
		} else {
			enc.PutNull(tlv.ContextTag(4))
		}
		_ = enc.EndContainer()
	case mattercore.AccessControlEntryChangedEvent:
		// AccessControl §9.10.7.1. Field tags per matter.js
		// access-control.element.ts:62-74:
		//   [1] AdminNodeID        node-id (uint64) nullable
		//   [2] AdminPasscodeID    uint16          nullable
		//   [3] ChangeType         enum8
		//   [4] LatestValue        AccessControlEntryStruct nullable
		//   [0xFE] FabricIndex     uint8 (auto-added fabric-scoped tag)
		enc.StartStruct(tag)
		if x.AdminNodeID != nil {
			enc.PutUint64(tlv.ContextTag(1), *x.AdminNodeID)
		} else {
			enc.PutNull(tlv.ContextTag(1))
		}
		if x.AdminPasscodeID != nil {
			enc.PutUint16(tlv.ContextTag(2), *x.AdminPasscodeID)
		} else {
			enc.PutNull(tlv.ContextTag(2))
		}
		enc.PutUint(tlv.ContextTag(3), uint64(x.ChangeType))
		if x.LatestValue != nil {
			lv := *x.LatestValue
			enc.StartStruct(tlv.ContextTag(4))
			enc.PutUint(tlv.ContextTag(1), uint64(lv.Privilege))
			enc.PutUint(tlv.ContextTag(2), uint64(lv.AuthMode))
			if lv.Subjects == nil {
				enc.PutNull(tlv.ContextTag(3))
			} else {
				enc.StartArray(tlv.ContextTag(3))
				for _, s := range lv.Subjects {
					enc.PutUint(tlv.AnonymousTag(), s)
				}
				_ = enc.EndContainer()
			}
			if lv.Targets == nil {
				enc.PutNull(tlv.ContextTag(4))
			} else {
				enc.StartArray(tlv.ContextTag(4))
				for _, t := range lv.Targets {
					enc.StartStruct(tlv.AnonymousTag())
					if t.Cluster != nil {
						enc.PutUint(tlv.ContextTag(0), uint64(*t.Cluster))
					} else {
						enc.PutNull(tlv.ContextTag(0))
					}
					if t.Endpoint != nil {
						enc.PutUint(tlv.ContextTag(1), uint64(*t.Endpoint))
					} else {
						enc.PutNull(tlv.ContextTag(1))
					}
					if t.DeviceType != nil {
						enc.PutUint(tlv.ContextTag(2), uint64(*t.DeviceType))
					} else {
						enc.PutNull(tlv.ContextTag(2))
					}
					_ = enc.EndContainer()
				}
				_ = enc.EndContainer()
			}
			enc.PutUint(tlv.ContextTag(254), uint64(lv.FabricIndex))
			_ = enc.EndContainer()
		} else {
			enc.PutNull(tlv.ContextTag(4))
		}
		enc.PutUint(tlv.ContextTag(254), uint64(x.FabricIndex))
		_ = enc.EndContainer()
	default:
		// Cluster server returned a Go value the writer does not
		// handle (e.g. a struct or list). Emit null so the reply still
		// parses on the controller side; the cluster server should
		// either downcast to a primitive in MatterRead or supply its
		// own writer in a future iteration.
		enc.PutNull(tag)
	}
}

// defaultCommandFieldsWriter is the wire writer the bridge plugs
// into [im.InvokeResponse.MarshalTLV]. Type-switches on the
// cluster-native response struct returned by [interfaces.MatterClusterServer.MatterInvoke]
// and emits the matching TLV. Commands without response fields (the
// status-only OnOff / LevelControl / etc. clusters) hit the
// `default` arm and emit an empty Structure — chip-tool's
// IM decoder accepts that for status-only command IDs.
//
// Add a case here whenever a new cluster command starts producing a
// rich response struct.
func defaultCommandFieldsWriter(enc *tlv.Encoder, tag tlv.Tag, v any) {
	switch x := v.(type) {
	case mattercore.ArmFailSafeResponse:
		// Matter §11.10.6.3 — [0] enum8 ErrorCode, [1] string DebugText.
		enc.StartStruct(tag)
		enc.PutUint(tlv.ContextTag(0), uint64(x.ErrorCode))
		enc.PutUTF8(tlv.ContextTag(1), x.DebugText)
		_ = enc.EndContainer()
	case mattercore.SetRegulatoryConfigResponse:
		// Matter §11.10.6.5 — same shape as ArmFailSafeResponse.
		enc.StartStruct(tag)
		enc.PutUint(tlv.ContextTag(0), uint64(x.ErrorCode))
		enc.PutUTF8(tlv.ContextTag(1), x.DebugText)
		_ = enc.EndContainer()
	case mattercore.CommissioningCompleteResponse:
		// Matter §11.10.6.7 — same shape.
		enc.StartStruct(tag)
		enc.PutUint(tlv.ContextTag(0), uint64(x.ErrorCode))
		enc.PutUTF8(tlv.ContextTag(1), x.DebugText)
		_ = enc.EndContainer()
	case mattercore.AttestationResponse:
		// Matter §11.18.7.2 — [0] AttestationElements, [1] AttestationSignature.
		enc.StartStruct(tag)
		enc.PutOctets(tlv.ContextTag(0), x.AttestationElements)
		enc.PutOctets(tlv.ContextTag(1), x.AttestationSignature)
		_ = enc.EndContainer()
	case mattercore.CertificateChainResponse:
		// Matter §11.18.7.4 — [0] Certificate.
		enc.StartStruct(tag)
		enc.PutOctets(tlv.ContextTag(0), x.Certificate)
		_ = enc.EndContainer()
	case mattercore.CSRResponse:
		// Matter §11.18.7.6 — [0] NOCSRElements, [1] AttestationSignature.
		enc.StartStruct(tag)
		enc.PutOctets(tlv.ContextTag(0), x.NOCSRElements)
		enc.PutOctets(tlv.ContextTag(1), x.AttestationSignature)
		_ = enc.EndContainer()
	case mattercore.NOCResponse:
		// Matter §11.18.7.9 — [0] StatusCode, [1] FabricIndex (optional),
		// [2] DebugText (optional).
		enc.StartStruct(tag)
		enc.PutUint(tlv.ContextTag(0), uint64(x.StatusCode))
		if x.FabricIndex != 0 {
			enc.PutUint(tlv.ContextTag(1), uint64(x.FabricIndex))
		}
		if x.DebugText != "" {
			enc.PutUTF8(tlv.ContextTag(2), x.DebugText)
		}
		_ = enc.EndContainer()
	default:
		// Status-only command — emit empty struct as the TLV
		// placeholder chip-tool's status-only decoder accepts.
		enc.StartStruct(tag)
		_ = enc.EndContainer()
	}
}

// debugReplyError is a small helper that turns reply-path errors
// into structured slog records. Each error gets its own attribute
// list so operators can grep for the specific failure stage.
func debugReplyError(logger *slog.Logger, stage string, src *net.UDPAddr, err error) {
	if err == nil {
		return
	}
	logger.Debug("matter.tx.reply",
		slog.String("stage", stage),
		slog.String("src", srcString(src)),
		slog.String("err", err.Error()))
}
