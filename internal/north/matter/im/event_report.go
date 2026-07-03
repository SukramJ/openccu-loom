// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// EventPathIB tag numbers (Matter Core Spec §10.6.6 EventPathIB).
const (
	tagEventPathNode     uint8 = 0
	tagEventPathEndpoint uint8 = 1
	tagEventPathCluster  uint8 = 2
	tagEventPathEvent    uint8 = 3
	tagEventPathIsUrgent uint8 = 4
)

// EventDataIB tag numbers (Matter Core Spec §10.6.9, table 80).
// Mirrors matter.js `packages/types/src/protocol/types/TlvEventData.ts`.
// The previous (incorrect) layout used tag=3 for SystemTimestamp, tag=4 for
// EpochTimestamp, and tag=5 for Data — chip-tool's `EventDataIB.Parser`
// at `src/app/MessageDef/Parser.h:123` decodes by tag-position-into-type
// and fails the EventDataIB with `CHIP Error 0x26: Wrong TLV type` the
// moment it reads the wrong wire shape at tag 5 (expects uint64 for
// DeltaEpochTimestamp, gets a TLV null/struct from the data writer).
const (
	tagEventDataPath                 uint8 = 0
	tagEventDataNumber               uint8 = 1
	tagEventDataPriority             uint8 = 2
	tagEventDataEpochTimestamp       uint8 = 3
	tagEventDataSystemTimestamp      uint8 = 4
	tagEventDataDeltaEpochTimestamp  uint8 = 5
	tagEventDataDeltaSystemTimestamp uint8 = 6
	tagEventDataData                 uint8 = 7
)

// EventStatusIB tag numbers (Matter Core Spec §10.6.6.2).
const (
	tagEventStatusPath   uint8 = 0
	tagEventStatusStatus uint8 = 1
)

// EventReportIB tag numbers (Matter Core Spec §10.6.6).
const (
	tagEventReportStatus uint8 = 0
	tagEventReportData   uint8 = 1
)

// EventPriority is the Matter §10.6.6.1 priority enum.
type EventPriority uint8

const (
	// EventPriorityDebug — least important; controllers may drop.
	EventPriorityDebug EventPriority = 0
	// EventPriorityInfo — informational.
	EventPriorityInfo EventPriority = 1
	// EventPriorityCritical — must be delivered.
	EventPriorityCritical EventPriority = 2
)

// ConcreteEventPath identifies one event. Has* flags distinguish
// "field present, value below" from "wildcard".
type ConcreteEventPath struct {
	Node     uint64
	Endpoint uint16
	Cluster  uint32
	Event    uint32

	HasNode     bool
	HasEndpoint bool
	HasCluster  bool
	HasEvent    bool
	// IsUrgent flips the per-path urgency bit; combined with
	// EventPriorityCritical it forces an immediate report.
	IsUrgent bool
}

// IsWildcardEndpoint reports whether the path matches every endpoint.
func (p ConcreteEventPath) IsWildcardEndpoint() bool { return !p.HasEndpoint }

// IsWildcardCluster reports whether the path matches every cluster.
func (p ConcreteEventPath) IsWildcardCluster() bool { return !p.HasCluster }

// IsWildcardEvent reports whether the path matches every event.
func (p ConcreteEventPath) IsWildcardEvent() bool { return !p.HasEvent }

// Matches returns true when this subscription path covers other.
// Wildcards in this path expand to match any concrete value in other.
func (p ConcreteEventPath) Matches(other ConcreteEventPath) bool {
	if p.HasEndpoint && p.Endpoint != other.Endpoint {
		return false
	}
	if p.HasCluster && p.Cluster != other.Cluster {
		return false
	}
	if p.HasEvent && p.Event != other.Event {
		return false
	}
	return true
}

// MarshalTLV encodes p as an EventPathIB (LIST).
func (p ConcreteEventPath) MarshalTLV(enc *tlv.Encoder, tag tlv.Tag) {
	enc.StartList(tag)
	if p.HasNode {
		enc.PutUint(tlv.ContextTag(tagEventPathNode), p.Node)
	}
	if p.HasEndpoint {
		enc.PutUint(tlv.ContextTag(tagEventPathEndpoint), uint64(p.Endpoint))
	}
	if p.HasCluster {
		enc.PutUint(tlv.ContextTag(tagEventPathCluster), uint64(p.Cluster))
	}
	if p.HasEvent {
		enc.PutUint(tlv.ContextTag(tagEventPathEvent), uint64(p.Event))
	}
	if p.IsUrgent {
		enc.PutBool(tlv.ContextTag(tagEventPathIsUrgent), true)
	}
	_ = enc.EndContainer()
}

// EventReport is one (path, value | status) tuple inside the
// EventReports array of a ReportDataMessage.
//
// Mirrors matter.js packages/protocol/src/action/server/EventReadResponse.ts
// (EventReadResponse.#asValue / EventReadResponse.#asStatus) — the two
// constructors that produce the ReadResult.EventValue / EventStatus shapes
// that the IM layer eventually encodes into EventDataIB / EventStatusIB.
type EventReport struct {
	Path      ConcreteEventPath
	Number    uint64 // Monotonic event number per cluster
	Priority  EventPriority
	Timestamp uint64 // EpochTimestamp: POSIX milliseconds (encoded at tag 3)
	Data      AttributeValue
	Status    StatusIB
	IsStatus  bool
}

// EventDataWriter writes the event-specific Data payload into TLV. It
// mirrors AttributeValueWriter — the IM layer stays cluster-blind.
type EventDataWriter func(enc *tlv.Encoder, tag tlv.Tag, v AttributeValue)

// readEventPathArray walks an Array of EventPathIB elements until
// EndContainer.
func readEventPathArray(dec *tlv.Decoder) ([]ConcreteEventPath, error) {
	var paths []ConcreteEventPath
	for {
		el, err := dec.Next()
		if err != nil {
			return nil, err
		}
		if el.IsEndContainer {
			return paths, nil
		}
		if !el.IsContainer || el.Type != tlv.TypeList {
			return nil, fmt.Errorf("%w: EventPathIB not list, got 0x%02X", ErrInvalidPath, el.Type)
		}
		p, err := readEventPathFields(dec)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
}

// readEventPathFields is the inner half of EventPathIB decoding —
// used when the caller has already consumed the list opener.
func readEventPathFields(dec *tlv.Decoder) (ConcreteEventPath, error) {
	var p ConcreteEventPath
	for {
		el, err := dec.Next()
		if err != nil {
			return ConcreteEventPath{}, fmt.Errorf("%w: %w", ErrInvalidPath, err)
		}
		if el.IsEndContainer {
			return p, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case tagEventPathNode:
			p.Node = el.Uint
			p.HasNode = true
		case tagEventPathEndpoint:
			p.Endpoint = uint16(el.Uint & 0xFFFF)
			p.HasEndpoint = true
		case tagEventPathCluster:
			p.Cluster = uint32(el.Uint & 0xFFFFFFFF)
			p.HasCluster = true
		case tagEventPathEvent:
			p.Event = uint32(el.Uint & 0xFFFFFFFF)
			p.HasEvent = true
		case tagEventPathIsUrgent:
			p.IsUrgent = el.Bool
		}
	}
}

func (rep EventReport) marshal(enc *tlv.Encoder, valueWriter EventDataWriter) {
	enc.StartStruct(tlv.AnonymousTag())
	if rep.IsStatus {
		enc.StartStruct(tlv.ContextTag(tagEventReportStatus))
		rep.Path.MarshalTLV(enc, tlv.ContextTag(tagEventStatusPath))
		rep.Status.MarshalTLV(enc, tlv.ContextTag(tagEventStatusStatus))
		_ = enc.EndContainer()
	} else {
		enc.StartStruct(tlv.ContextTag(tagEventReportData))
		// IsUrgent is a SubscribeRequest path qualifier; it MUST NOT appear in
		// an EventDataIB path. Mirrors matter.js
		// packages/protocol/src/interaction/AttributeDataEncoder.ts:131 (#3988,
		// Matter §8.9.3.4), which deletes isUrgent before encoding the data path.
		dataPath := rep.Path
		dataPath.IsUrgent = false
		dataPath.MarshalTLV(enc, tlv.ContextTag(tagEventDataPath))
		// EventNumber and EpochTimestamp must be encoded as exactly 8 bytes.
		// chip-tool's strict IM decoder rejects narrower widths with
		// CHIP Error 0x26 (Wrong TLV type). Use PutUintWidth(8) rather than
		// PutUint64 so the fixed-width intent is visible at the callsite;
		// PutUint64 now uses minimal-fit encoding (matter.js wire parity).
		enc.PutUintWidth(tlv.ContextTag(tagEventDataNumber), rep.Number, 8)
		enc.PutUint(tlv.ContextTag(tagEventDataPriority), uint64(rep.Priority))
		enc.PutUintWidth(tlv.ContextTag(tagEventDataEpochTimestamp), rep.Timestamp, 8)
		// Tag 7 carries the cluster-specific event payload (TlvAny).
		// Omit entirely when there is no payload — matter.js treats the
		// field as optional and an explicit `null` at tag 7 is still
		// preferable to emitting at the wrong tag.
		if !rep.Data.IsNull && rep.Data.Value != nil {
			valueWriter(enc, tlv.ContextTag(tagEventDataData), rep.Data)
		}
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
}
