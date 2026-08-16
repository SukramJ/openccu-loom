// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// SubscribeRequestMessage tag numbers (Matter Core Spec §10.6.9).
const (
	tagSubReqKeepSubscriptions  uint8 = 0
	tagSubReqMinIntervalFloor   uint8 = 1
	tagSubReqMaxIntervalCeiling uint8 = 2
	tagSubReqAttributeRequests  uint8 = 3
	tagSubReqEventRequests      uint8 = 4
	tagSubReqEventFilters       uint8 = 5
	tagSubReqFabricFiltered     uint8 = 7
	tagSubReqDataVersionFilters uint8 = 8
)

// SubscribeResponseMessage tag numbers.
const (
	tagSubRespSubscriptionID uint8 = 0
	tagSubRespMaxInterval    uint8 = 2
	// tagInteractionModelRevision is the global Matter 1.x IM-revision
	// marker every IM message carries on tag 0xFF. matter.js's
	// TlvSubscribeResponse / TlvDataReportForSend / TlvStatusResponse
	// encode it on every reply. Apple Home strict-decodes the field
	// and silently re-sends the request when it is missing — the
	// SubscribeResponse appears to "never arrive", Apple's MTRDevice
	// times the transaction out after ~10 s and resubscribes in a
	// loop.
	tagInteractionModelRevision uint8 = 0xFF

	// MatterInteractionModelRevision is the IM revision the bridge
	// advertises. Matches matter.js v0.16.10 (Matter 1.5).
	MatterInteractionModelRevision uint8 = 13
)

// Errors.
var (
	// ErrInvalidSubscribeRequest is returned for malformed subscribes.
	ErrInvalidSubscribeRequest = errors.New("im: invalid SubscribeRequest")
)

// EventMinimumNumber is one entry in the EventFilters list that a
// commissioner sends to tell the bridge "the lowest event number I still
// want is EventMin; everything below it I already have."
//
// Mirrors Matter Core Spec §10.6.9 EventFilterIB and matter.js
// packages/types/src/protocol/types/TlvEventFilter.ts. chip
// src/app/ReadHandler.cpp:598 ProcessEventFilters stores EventMin
// from EventFilterIB.EventMin and uses it to gate the event query.
type EventMinimumNumber struct {
	// NodeID is the optional node filter from EventFilterIB tag 0.
	// In a bridge context this is always the bridge's own NodeID or
	// omitted; openccu-loom stores it for completeness but does not
	// use it in the query — only EventMin is applied.
	NodeID    uint64
	HasNodeID bool
	// EventMin is the minimum event number (INCLUSIVE lower bound): the
	// bridge emits events with Number >= EventMin — see [EventLog.Query].
	// Corresponds to EventFilterIB tag 1.
	EventMin uint64
}

// SubscribeRequest is the in-memory form of a SubscribeRequestMessage.
//
// This type is the message shape only. The state machine around it —
// cadence enforcement and the per-fabric subscription cap — lives in
// im/subscription.Manager, which the daemon attaches to the bridge, so
// both are in force in production. Replay buffering is the part that
// remains unbuilt. The message-layer surface is kept stable against
// cluster-side callers that need to express "subscribe to OnOff state".
type SubscribeRequest struct {
	KeepSubscriptions  bool
	MinIntervalFloor   uint16
	MaxIntervalCeiling uint16
	AttributeRequests  []ConcreteAttributePath
	// EventRequests carries the subscription's interest in cluster
	// events (Matter §10.6.6). Wildcards work the same way as
	// AttributeRequests: leaving HasEndpoint / HasCluster / HasEvent
	// false matches every value.
	EventRequests []ConcreteEventPath
	// EventFilters carries per-source minimum-event-number hints the
	// controller sends to avoid re-receiving events it has already seen
	// (Matter §10.6.9 EventFilterIB). The bridge evaluates the filters
	// in BuildEventReports and skips events whose Number ≤ the
	// supplied EventMin.
	EventFilters   []EventMinimumNumber
	FabricFiltered bool
	// DataVersionFilters carries per-cluster version hints the
	// controller sends to avoid re-receiving attribute data it already
	// has cached (Matter §10.6.5). The bridge evaluates the filters
	// during the initial ReportData assembly and skips clusters whose
	// current DataVersion matches the cached one.
	DataVersionFilters []DataVersionFilter
}

// MarshalTLV encodes r.
func (r SubscribeRequest) MarshalTLV(enc *tlv.Encoder) {
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(tagSubReqKeepSubscriptions), r.KeepSubscriptions)
	enc.PutUint(tlv.ContextTag(tagSubReqMinIntervalFloor), uint64(r.MinIntervalFloor))
	enc.PutUint(tlv.ContextTag(tagSubReqMaxIntervalCeiling), uint64(r.MaxIntervalCeiling))
	enc.StartArray(tlv.ContextTag(tagSubReqAttributeRequests))
	for _, p := range r.AttributeRequests {
		p.MarshalTLV(enc, tlv.AnonymousTag())
	}
	_ = enc.EndContainer()
	if len(r.EventRequests) > 0 {
		enc.StartArray(tlv.ContextTag(tagSubReqEventRequests))
		for _, p := range r.EventRequests {
			p.MarshalTLV(enc, tlv.AnonymousTag())
		}
		_ = enc.EndContainer()
	}
	enc.PutBool(tlv.ContextTag(tagSubReqFabricFiltered), r.FabricFiltered)
	_ = enc.EndContainer()
}

// UnmarshalSubscribeRequestTLV decodes a SubscribeRequestMessage.
func UnmarshalSubscribeRequestTLV(dec *tlv.Decoder) (SubscribeRequest, error) { //nolint:gocognit,gocyclo // wire/dispatch table over many attribute/opcode cases
	open, err := dec.Next()
	if err != nil {
		return SubscribeRequest{}, err
	}
	if !open.IsContainer || open.Type != tlv.TypeStructure {
		return SubscribeRequest{}, fmt.Errorf("%w: expected struct, got 0x%02X", ErrInvalidSubscribeRequest, open.Type)
	}
	var req SubscribeRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return SubscribeRequest{}, fmt.Errorf("%w: %w", ErrInvalidSubscribeRequest, err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case tagSubReqKeepSubscriptions:
			req.KeepSubscriptions = el.Bool
		case tagSubReqMinIntervalFloor:
			req.MinIntervalFloor = uint16(el.Uint & 0xFFFF)
		case tagSubReqMaxIntervalCeiling:
			req.MaxIntervalCeiling = uint16(el.Uint & 0xFFFF)
		case tagSubReqAttributeRequests:
			if !el.IsContainer || el.Type != tlv.TypeArray {
				return SubscribeRequest{}, fmt.Errorf("%w: AttributeRequests not array", ErrInvalidSubscribeRequest)
			}
			paths, err := readPathArray(dec)
			if err != nil {
				return SubscribeRequest{}, err
			}
			req.AttributeRequests = paths
		case tagSubReqEventRequests:
			if !el.IsContainer || el.Type != tlv.TypeArray {
				return SubscribeRequest{}, fmt.Errorf("%w: EventRequests not array", ErrInvalidSubscribeRequest)
			}
			eventPaths, err := readEventPathArray(dec)
			if err != nil {
				return SubscribeRequest{}, err
			}
			req.EventRequests = eventPaths
		case tagSubReqEventFilters:
			// EventFilters: Array of EventFilterIB structs per Matter §10.6.9.
			// Each entry carries NodeID (tag 0, optional) and EventMin (tag 1,
			// mandatory) — matter.js
			// packages/types/src/protocol/types/TlvEventFilter.ts:16-17.
			// Mirrors chip src/app/ReadHandler.cpp:598 ProcessEventFilters.
			if !el.IsContainer || el.Type != tlv.TypeArray {
				// Malformed but non-fatal: skip the field. Draining is
				// only correct for a container — a scalar is already
				// consumed, and skipContainer would swallow the
				// enclosing SubscribeRequest struct's EndContainer.
				if el.IsContainer {
					if err := skipContainer(dec); err != nil {
						return SubscribeRequest{}, err
					}
				}
				continue
			}
			filters, err := readEventFilterArray(dec)
			if err != nil {
				return SubscribeRequest{}, err
			}
			req.EventFilters = filters
		case tagSubReqFabricFiltered:
			req.FabricFiltered = el.Bool
		case tagSubReqDataVersionFilters:
			if !el.IsContainer || el.Type != tlv.TypeArray {
				return SubscribeRequest{}, fmt.Errorf("%w: DataVersionFilters not array", ErrInvalidSubscribeRequest)
			}
			filters, err := readDataVersionFilterArray(dec)
			if err != nil {
				return SubscribeRequest{}, err
			}
			req.DataVersionFilters = filters
		default:
			if el.IsContainer {
				if err := skipContainer(dec); err != nil {
					return SubscribeRequest{}, err
				}
			}
		}
	}
}

// SubscribeResponse is the in-memory form of a
// SubscribeResponseMessage.
type SubscribeResponse struct {
	SubscriptionID uint32
	MaxInterval    uint16
}

// MarshalTLV encodes sr.
//
// Per Matter Core Spec §8.5.5.5 SubscriptionId is `uint32` and
// MaxInterval is `uint16` — both are spec-fixed widths, not magnitude-
// driven. Strict commissioner IM-decoders (chip-tool's `ReadClient`,
// Apple's MTRDevice) compute the post-establishment subscription
// liveness timer as `MaxInterval + RoundTripTimeout`; when MaxInterval
// is encoded as a magnitude-narrowed `uint8` (e.g. value 30 → 1 byte)
// the strict decoder reads MaxInterval as 0 / unparsed and the
// liveness timer collapses to the round-trip alone (~10 s).
// Empirically this surfaced as `CHIP Error 0x32 Timeout` from
// `ReadClient.cpp:745` exactly 10 s after SubscribeResponse — the
// chip-tool reproducer of the Subscribe-pump hardstop documented in
// Session 14 §19.4. Same shape as Session 13 Fix #38 for ReportData's
// DataVersion / SubscriptionId.
func (sr SubscribeResponse) MarshalTLV(enc *tlv.Encoder) {
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint32(tlv.ContextTag(tagSubRespSubscriptionID), sr.SubscriptionID)
	enc.PutUint16(tlv.ContextTag(tagSubRespMaxInterval), sr.MaxInterval)
	enc.PutUint(tlv.ContextTag(tagInteractionModelRevision), uint64(MatterInteractionModelRevision))
	_ = enc.EndContainer()
}
