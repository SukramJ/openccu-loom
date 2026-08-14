// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// ReadRequestMessage tag numbers (Matter Core Spec §10.6.4).
const (
	tagReadReqAttributeRequests  uint8 = 0
	tagReadReqEventRequests      uint8 = 1
	tagReadReqEventFilters       uint8 = 2
	tagReadReqFabricFiltered     uint8 = 3
	tagReadReqDataVersionFilters uint8 = 4
)

// ReportDataMessage tag numbers (Matter Core Spec §10.6.6).
const (
	tagReportSubscriptionID      uint8 = 0
	tagReportAttributeReports    uint8 = 1
	tagReportEventReports        uint8 = 2
	tagReportMoreChunkedMessages uint8 = 3
	tagReportSuppressResponse    uint8 = 4
)

// AttributeReportIB / AttributeDataIB tag numbers.
const (
	tagAttributeReportStatus uint8 = 0 // AttributeStatusIB
	tagAttributeReportData   uint8 = 1 // AttributeDataIB

	tagAttributeDataDataVersion uint8 = 0
	tagAttributeDataPath        uint8 = 1
	tagAttributeDataValue       uint8 = 2

	tagAttributeStatusPath   uint8 = 0
	tagAttributeStatusStatus uint8 = 1
)

// Errors.
var (
	// ErrInvalidReadRequest is returned for malformed read requests.
	ErrInvalidReadRequest = errors.New("im: invalid ReadRequest")
)

// DataVersionFilter is a single (endpoint, cluster, version) triple
// the controller sends to tell the bridge "I have this version cached;
// skip encoding attributes for this cluster if your DataVersion still
// matches". Matter Core Spec §10.6.5.
type DataVersionFilter struct {
	Endpoint    uint16
	Cluster     uint32
	DataVersion uint32
}

// ReadRequest is the in-memory form of a ReadRequestMessage.
// EventRequests carries the §10.6.4 EventPathIB list when the
// controller requests historical events (e.g. chip-tool's
// `read-event-by-id`). AttributeRequests and EventRequests may both
// be present in the same ReadRequest.
type ReadRequest struct {
	AttributeRequests []ConcreteAttributePath
	EventRequests     []ConcreteEventPath
	// EventFilters carries per-source minimum-event-number hints per
	// Matter §10.6.4 EventFilterIB. The bridge skips events with
	// Number < EventMin in BuildEventReports (EventMin is an inclusive
	// lower bound — see [EventLog.Query]).
	EventFilters       []EventMinimumNumber
	FabricFiltered     bool
	DataVersionFilters []DataVersionFilter
}

// MarshalTLV encodes r at the top level (always anonymous tag).
func (r ReadRequest) MarshalTLV(enc *tlv.Encoder) {
	enc.StartStruct(tlv.AnonymousTag())
	enc.StartArray(tlv.ContextTag(tagReadReqAttributeRequests))
	for _, p := range r.AttributeRequests {
		p.MarshalTLV(enc, tlv.AnonymousTag())
	}
	_ = enc.EndContainer()
	enc.PutBool(tlv.ContextTag(tagReadReqFabricFiltered), r.FabricFiltered)
	_ = enc.EndContainer()
}

// UnmarshalReadRequestTLV reads a ReadRequestMessage. Skips the
// optional Event* fields — they're not consumed by v1.1.
func UnmarshalReadRequestTLV(dec *tlv.Decoder) (ReadRequest, error) { //nolint:gocognit // wire/dispatch table over many attribute/opcode cases
	open, err := dec.Next()
	if err != nil {
		return ReadRequest{}, err
	}
	if !open.IsContainer || open.Type != tlv.TypeStructure {
		return ReadRequest{}, fmt.Errorf("%w: expected struct, got 0x%02X", ErrInvalidReadRequest, open.Type)
	}
	var req ReadRequest
	for {
		el, err := dec.Next()
		if err != nil {
			return ReadRequest{}, fmt.Errorf("%w: %w", ErrInvalidReadRequest, err)
		}
		if el.IsEndContainer {
			return req, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case tagReadReqAttributeRequests:
			if !el.IsContainer || el.Type != tlv.TypeArray {
				return ReadRequest{}, fmt.Errorf("%w: AttributeRequests not array", ErrInvalidReadRequest)
			}
			paths, err := readPathArray(dec)
			if err != nil {
				return ReadRequest{}, err
			}
			req.AttributeRequests = paths
		case tagReadReqEventRequests:
			if !el.IsContainer || el.Type != tlv.TypeArray {
				return ReadRequest{}, fmt.Errorf("%w: EventRequests not array", ErrInvalidReadRequest)
			}
			paths, err := readEventPathArray(dec)
			if err != nil {
				return ReadRequest{}, err
			}
			req.EventRequests = paths
		case tagReadReqEventFilters:
			// EventFilters: Array of EventFilterIB structs per Matter §10.6.9.
			// Each entry carries NodeID (tag 0, optional) and EventMin (tag 1,
			// mandatory) — matter.js
			// packages/types/src/protocol/types/TlvEventFilter.ts:16-17.
			// Mirrors chip src/app/ReadHandler.cpp:598 ProcessEventFilters.
			if !el.IsContainer || el.Type != tlv.TypeArray {
				// Malformed but non-fatal: skip the field.
				if err := skipContainer(dec); err != nil {
					return ReadRequest{}, err
				}
				continue
			}
			filters, err := readEventFilterArray(dec)
			if err != nil {
				return ReadRequest{}, err
			}
			req.EventFilters = filters
		case tagReadReqFabricFiltered:
			req.FabricFiltered = el.Bool
		case tagReadReqDataVersionFilters:
			// DataVersionFilters is an Array of structs per Matter §10.6.4.
			if !el.IsContainer || el.Type != tlv.TypeArray {
				return ReadRequest{}, fmt.Errorf("%w: DataVersionFilters not array", ErrInvalidReadRequest)
			}
			filters, err := readDataVersionFilterArray(dec)
			if err != nil {
				return ReadRequest{}, err
			}
			req.DataVersionFilters = filters
		default:
			// Skip unknown / future fields per Matter forward-compat rules.
			if el.IsContainer {
				if err := skipContainer(dec); err != nil {
					return ReadRequest{}, err
				}
			}
		}
	}
}

// readPathArray walks an Array of AttributePathIB elements until
// EndContainer.
func readPathArray(dec *tlv.Decoder) ([]ConcreteAttributePath, error) {
	var paths []ConcreteAttributePath
	for {
		// Peek the next element via Next; if it's the array's end,
		// return. Otherwise the element MUST be a List opener — the
		// path decoder consumes it.
		el, err := dec.Next()
		if err != nil {
			return nil, err
		}
		if el.IsEndContainer {
			return paths, nil
		}
		if !el.IsContainer || el.Type != tlv.TypeList {
			return nil, fmt.Errorf("%w: AttributePathIB not list, got 0x%02X", ErrInvalidPath, el.Type)
		}
		// Re-construct the path from the already-opened list. We
		// inline the field-loop body of UnmarshalAttributePathTLV
		// because the opener has been consumed.
		p, err := readAttributePathFields(dec)
		if err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
}

// readAttributePathFields is the inner half of UnmarshalAttributePathTLV
// — used when the caller has already consumed the list opener.
func readAttributePathFields(dec *tlv.Decoder) (ConcreteAttributePath, error) {
	var p ConcreteAttributePath
	for {
		el, err := dec.Next()
		if err != nil {
			return ConcreteAttributePath{}, fmt.Errorf("%w: %w", ErrInvalidPath, err)
		}
		if el.IsEndContainer {
			return p, nil
		}
		if el.Tag.Kind != tlv.TagKindContext {
			continue
		}
		switch uint8(el.Tag.Number & 0xFF) {
		case tagAttrPathNode:
			p.Node = el.Uint
			p.HasNode = true
		case tagAttrPathEndpoint:
			p.Endpoint = uint16(el.Uint & 0xFFFF)
			p.HasEndpoint = true
		case tagAttrPathCluster:
			p.Cluster = uint32(el.Uint & 0xFFFFFFFF)
			p.HasCluster = true
		case tagAttrPathAttribute:
			p.Attribute = uint32(el.Uint & 0xFFFFFFFF)
			p.HasAttribute = true
		case tagAttrPathListIndex:
			p.ListIndex = uint16(el.Uint & 0xFFFF)
			p.HasListIndex = true
		}
	}
}

// skipContainer drains every nested element until a balancing
// EndContainer is reached. Used to skip optional fields the v1.1
// implementation does not need.
func skipContainer(dec *tlv.Decoder) error {
	depth := 1
	for depth > 0 {
		el, err := dec.Next()
		if err != nil {
			return err
		}
		if el.IsContainer {
			depth++
		}
		if el.IsEndContainer {
			depth--
		}
	}
	return nil
}

// ReportData is the encoder side: the device assembles a
// ReportDataMessage from the dispatcher's [ReadResult] slice and ships
// it.
type ReportData struct {
	SubscriptionID  uint32
	HasSubscription bool
	Reports         []AttributeReport
	// EventReports carries the §10.6.6 EventReportIB list. Bridge
	// emits these when a subscribed cluster fires an event (e.g.
	// GenericSwitch InitialPress, DoorLock LockOperation).
	EventReports []EventReport
	// MoreChunkedMessages signals that this ReportDataMessage is one
	// of several chunks for the same logical report. Per Matter
	// §10.6.6 the sender splits oversized ReportData into multiple
	// datagrams, sets the flag on every chunk except the last, and
	// the receiver re-assembles by concatenating attribute / event
	// arrays in arrival order.
	MoreChunkedMessages bool
	SuppressResponse    bool
}

// AttributeReport is one (path, value | status) tuple inside the
// AttributeReports array.
type AttributeReport struct {
	Path   ConcreteAttributePath
	Status StatusIB
	Value  AttributeValue
	// IsStatus distinguishes a "status-only" entry (Status meaningful,
	// Value ignored) from a data entry (Value meaningful, Status zero).
	IsStatus bool
	// DataVersion is the per-cluster version tag chip emits in
	// AttributeDataIB.DataVersion (Matter §10.6.1.4). 0 means "let
	// the marshaller default to 1" — controllers tolerate any
	// non-zero version on first read.
	DataVersion uint32
}

// AttributeValueWriter is the function the Dispatcher's caller
// supplies for writing a cluster-native value into TLV. The IM layer
// stays cluster-blind; the writer translates Value into the right
// TLV-encoded shape (uint8, int16, struct, …).
type AttributeValueWriter func(enc *tlv.Encoder, tag tlv.Tag, v AttributeValue)

// MarshalTLV encodes r as a ReportDataMessage. valueWriter writes
// each [AttributeValue] into TLV; the IM layer does not own
// cluster-native type encoding. Event payload writing reuses
// valueWriter — events surface their data with the same cluster-native
// shapes that attributes do.
func (r ReportData) MarshalTLV(enc *tlv.Encoder, valueWriter AttributeValueWriter) {
	enc.StartStruct(tlv.AnonymousTag())
	if r.HasSubscription {
		// SubscriptionId is `TlvUInt32` per Matter §10.6.4.1.
		// Apple Home's IM-decoder rejects the whole ReportDataMessage
		// when this slot arrives as TypeUnsignedInt1 (which the
		// magnitude-driven PutUint emits for sub-IDs ≤ 0xFF). Use the
		// explicit-width helper to lock the wire type.
		enc.PutUint32(tlv.ContextTag(tagReportSubscriptionID), r.SubscriptionID)
	}
	enc.StartArray(tlv.ContextTag(tagReportAttributeReports))
	for _, rep := range r.Reports {
		rep.marshal(enc, valueWriter)
	}
	_ = enc.EndContainer()
	if len(r.EventReports) > 0 {
		enc.StartArray(tlv.ContextTag(tagReportEventReports))
		for _, ev := range r.EventReports {
			ev.marshal(enc, EventDataWriter(valueWriter))
		}
		_ = enc.EndContainer()
	}
	if r.MoreChunkedMessages {
		enc.PutBool(tlv.ContextTag(tagReportMoreChunkedMessages), true)
	}
	// Always emit SuppressResponse, including the explicit `false`. matter.js
	// (packages/types/src/protocol/messages/TlvDataReportForSend.ts:27)
	// declares this as a TlvOptionalField that ServerSubscription always
	// fills with a concrete value: `false` for non-empty data reports and
	// `true` for empty keepalives. Apple Home's IM layer treats an absent
	// SuppressResponse differently from an explicit `false` — without the
	// field the controller answers with only a Standalone MRP-ACK and never
	// emits the StatusResponse the spec §10.6.4.1 mandates for
	// non-suppressed reports, which leaves matter.js's waitForSuccess gate
	// open indefinitely (irrelevant here) but also drops the report's
	// "delivery + parse OK" half-handshake the HMOutlet projection appears
	// to gate on. Always-emitting locks the wire shape to matter.js HEAD's
	// behavior.
	enc.PutBool(tlv.ContextTag(tagReportSuppressResponse), r.SuppressResponse)
	// Global IM revision marker (Matter 1.x §10.6, Tag 0xFF). matter.js
	// emits this on every IM message — Apple Home decodes strictly and
	// times subscribe transactions out when it is absent.
	enc.PutUint(tlv.ContextTag(tagInteractionModelRevision), uint64(MatterInteractionModelRevision))
	_ = enc.EndContainer()
}

func (rep AttributeReport) marshal(enc *tlv.Encoder, valueWriter AttributeValueWriter) {
	enc.StartStruct(tlv.AnonymousTag())
	if rep.IsStatus {
		enc.StartStruct(tlv.ContextTag(tagAttributeReportStatus))
		rep.Path.MarshalTLV(enc, tlv.ContextTag(tagAttributeStatusPath))
		rep.Status.MarshalTLV(enc, tlv.ContextTag(tagAttributeStatusStatus))
		_ = enc.EndContainer()
	} else {
		enc.StartStruct(tlv.ContextTag(tagAttributeReportData))
		// DataVersion is mandatory in AttributeDataIB per Matter
		// §10.6.1.4. chip-tool's ClusterStateCache expects to find
		// it at context tag 0 — without it the controller surfaces
		// CHIP_ERROR_KEY_NOT_FOUND on every read. v1.1 emits a
		// constant 1 because the bridge does not yet implement the
		// per-cluster version-tracking required for cache-coherent
		// subscribe responses; controllers tolerate stale data
		// versions on first read.
		dataVersion := rep.DataVersion
		if dataVersion == 0 {
			dataVersion = 1
		}
		// DataVersion is `TlvUInt32` per Matter §10.6.1.4. Apple Home's
		// MTRDevice IM-decoder rejects the whole AttributeDataIB when
		// DataVersion arrives as TypeUnsignedInt1 — and the v1.x
		// constant 1 always fits in one byte, so the magnitude-driven
		// PutUint silently produced a non-spec wire shape. The result
		// was MTRDevice logging "last report: (null)" while the MRP
		// layer happily ACKed every chunk: Apple's per-element strict
		// type check threw out every report before MTRDevice ever saw
		// it, and HAP-service rebuild then failed with Code=24.
		enc.PutUint32(tlv.ContextTag(tagAttributeDataDataVersion), dataVersion)
		rep.Path.MarshalTLV(enc, tlv.ContextTag(tagAttributeDataPath))
		valueWriter(enc, tlv.ContextTag(tagAttributeDataValue), rep.Value)
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
}

// HandleReadRequest dispatches a parsed ReadRequest through d and
// returns the assembled ReportData. The caller marshals the result.
// Wildcard expansion is the Dispatcher's responsibility; this layer
// only routes paths.
//
// DataVersionFilter evaluation: for each (endpoint, cluster) pair in
// req.DataVersionFilters, if the cluster's current DataVersion matches
// the cached version the controller sent, the cluster's attributes are
// omitted from the report — the controller already has a fresh copy.
// Matter §10.6.5 / mirrors matter.js InteractionServer.ts
// startReadInteraction DataVersionFilter check.
//
// ACL gate: when d implements [ACLChecker], every RESOLVED result is
// authorized at its concrete (endpoint, cluster) using the resolved
// attribute's read privilege — run per result AFTER path expansion so a
// wildcard-endpoint read is authorized per concrete endpoint, not once
// against endpoint 0. A FULLY-CONCRETE path (endpoint + cluster + attribute
// all named) that is denied returns an AttributeStatusIB carrying
// UnsupportedAccess (0x7e); a WILDCARD-expanded result that is denied is
// SILENTLY OMITTED — a wildcard read discloses only authorized paths (Matter
// §8.4.3.2). PASE sessions (fabricIndex==0) bypass the check — commissioning
// reads must succeed before the fabric's ACL entry exists. Mirrors matter.js
// packages/protocol/src/action/server/AttributeReadResponse.ts:238-274
// (addConcrete → error status) and readAttributeForWildcard (bare return).
func HandleReadRequest(ctx context.Context, d Dispatcher, req ReadRequest) ReportData {
	_, fabricIndex := FabricFilterFromContext(ctx)
	subjectNodeID, subjectCATs := SubjectFromContext(ctx)
	aclChecker, hasACL := d.(ACLChecker)
	privProvider, hasPrivProvider := d.(AttributeReadPrivilegeProvider)

	// readPrivilege returns the minimum privilege needed to read the given
	// (endpoint, cluster, attribute) triplet. Falls back to View (1) when
	// no AttributeReadPrivilegeProvider is wired or the attribute has no
	// elevated requirement.
	readPrivilege := func(endpoint uint16, clusterID, attrID uint32) uint8 {
		if hasPrivProvider {
			return privProvider.MinReadPrivilege(endpoint, clusterID, attrID)
		}
		return 1
	}

	var rd ReportData
	for _, path := range req.AttributeRequests {
		// A fully-concrete path (endpoint + cluster + attribute all named)
		// returns an explicit AttributeStatusIB(UnsupportedAccess) on ACL
		// denial; a wildcard path silently omits the results the subject may
		// not read (Matter §8.4.3.2 — a wildcard read discloses only
		// authorized paths). Mirrors matter.js AttributeReadResponse.ts
		// addConcrete (error status) vs readAttributeForWildcard (omit).
		concretePath := path.HasEndpoint && path.HasCluster && path.HasAttribute

		results := d.Read(ctx, path)
		var filtered []AttributeReport
		for _, r := range results {
			// Per-RESULT ACL check at the RESOLVED concrete (endpoint,
			// cluster) using the resolved attribute's read privilege — run
			// for EVERY expanded result, including a wildcard-endpoint +
			// concrete-cluster read (previously authorized only against
			// endpoint 0, then expanded to every endpoint with no per-endpoint
			// recheck). Mirrors matter.js
			// packages/protocol/src/action/server/AttributeReadResponse.ts:
			// 238-274 — each resolved location is authorized with the resolved
			// attribute's readLevel AFTER path resolution. PASE
			// (fabricIndex==0) bypasses — commissioning reads must succeed
			// pre-AddNOC.
			if hasACL && fabricIndex != 0 {
				priv := readPrivilege(r.Path.Endpoint, r.Path.Cluster, r.Path.Attribute)
				if status := aclChecker.CheckACL(ctx, fabricIndex, subjectNodeID, subjectCATs, r.Path.Endpoint, r.Path.Cluster, priv); !status.IsSuccess() {
					if concretePath {
						filtered = append(filtered, AttributeReport{
							Path:     r.Path,
							IsStatus: true,
							Status:   StatusIB{Status: status},
						})
					}
					// Wildcard-expanded denial: omit, never disclose.
					continue
				}
			}

			// Check if the controller's cached DataVersion matches the
			// cluster's current DataVersion — if so, skip this cluster.
			//
			// Sentinel guard (>1, not !=0): clusters without per-instance
			// tracking report DataVersion=0 and the wire encoder substitutes
			// the §10.6.1.4 floor of 1. A controller that cached the
			// sentinel-1 and replays it MUST NOT cause the whole cluster to
			// be omitted — that collapses the bridged-endpoint topology in
			// Apple's cache. Mirrors the Subscribe-initial guard in
			// bridge/subscribe.go.
			if len(req.DataVersionFilters) > 0 && r.Status == StatusSuccess {
				if cached, ok := matchDataVersionFilter(req.DataVersionFilters, r.Path.Endpoint, r.Path.Cluster); ok {
					if cached == r.DataVersion && r.DataVersion > 1 {
						// Controller cache is fresh — omit this cluster.
						continue
					}
				}
			}
			rep := AttributeReport{Path: r.Path, DataVersion: r.DataVersion}
			if r.Status != StatusSuccess {
				rep.IsStatus = true
				rep.Status = StatusIB{Status: r.Status}
			} else {
				rep.Value = r.Value
			}
			filtered = append(filtered, rep)
		}
		rd.Reports = append(rd.Reports, filtered...)
	}
	// A plain (non-subscribe) Read's ReportData carries SuppressResponse=true:
	// the controller answers the final chunk with only a Standalone MRP-Ack, not
	// an IM StatusResponse. Mirrors matter.js
	// packages/node/src/node/server/InteractionServer.ts:346-350,371-374
	// (handleReadRequest returns `dataReport: { suppressResponse: true }`) and
	// chip src/app/ReadHandler.cpp:340 (`responseExpected = IsType(Subscribe) ||
	// aMoreChunks` → false on a read's final chunk). The chunker propagates this
	// to the terminal chunk only; intermediate chunks keep SuppressResponse=false
	// so the per-chunk StatusResponse handshake still runs. Subscribe priming
	// builds its own report and is unaffected — HandleReadRequest is only called
	// from the plain-read path.
	rd.SuppressResponse = true
	return rd
}

// minEventNumberFromFilters computes the minimum event-number floor from a
// set of [EventMinimumNumber] filters. Returns 0 (no filtering) when filters
// is empty. Takes the minimum across all entries — any event whose Number is
// < the floor is already known to the controller and shall not be re-sent
// (EventMin is an inclusive lower bound — see [EventLog.Query]).
//
// matter.js: EventHandler.ts getEvents → EventFilters passed to EventHandler.
// chip: src/app/ReadHandler.cpp:598 ProcessEventFilters — stores per-entry
// mEventMin and applies it in GetScheduledEventInfo.
func minEventNumberFromFilters(filters []EventMinimumNumber) uint64 {
	if len(filters) == 0 {
		return 0
	}
	floor := filters[0].EventMin
	for _, f := range filters[1:] {
		if f.EventMin < floor {
			floor = f.EventMin
		}
	}
	return floor
}

// BuildEventReports evaluates paths against log and returns a slice of
// [EventReport] suitable for embedding into a [ReportData].
//
// This is the event-only counterpart of [HandleReadRequest] for
// attributes. It mirrors the path-expansion + deduplication logic in
// matter.js packages/protocol/src/action/server/EventReadResponse.ts
// (EventReadResponse.process → #readAllowedEvents) and the event
// retrieval in matter.js packages/protocol/src/interaction/EventHandler.ts
// (getEvents). Wildcard-expand-then-filter semantics and sort-by-Number
// ascending output order match the matter.js reference implementation.
//
// Each [ConcreteEventPath] is evaluated independently; results are
// de-duplicated by [EventRecord].Number before returning so a wildcard
// path that overlaps with a concrete path does not produce duplicates.
//
// filters carries the EventFilterIB minimum-event-number constraints the
// controller sent (Matter §10.6.9). Pass nil to return all buffered events.
// When filters is non-empty the minimum EventMin across all entries is used
// as the INCLUSIVE lower bound — events with Number < minNumber are excluded.
// The returned [EventReport].Timestamp is sourced from
// [EventRecord].EpochMS (POSIX milliseconds — Matter §10.6.6.1
// EpochTimestamp, encoded at tag 3), matching the millisecond timestamp
// the subscribe path emits so the same event reads identically live and
// out-of-band.
//
// Callers that have a [ReadRequest] available should prefer the
// [HandleReadEventRequest] wrapper.
func BuildEventReports(paths []ConcreteEventPath, log *EventLog, filters []EventMinimumNumber) []EventReport {
	if log == nil || len(paths) == 0 {
		return nil
	}
	minNumber := minEventNumberFromFilters(filters)
	seen := make(map[uint64]struct{})
	var out []EventReport
	for _, path := range paths {
		// Resolve wildcard values for the EventLog.Query call.
		var ep uint16 = 0xFFFF
		if path.HasEndpoint {
			ep = path.Endpoint
		}
		var cl uint32 = 0xFFFFFFFF
		if path.HasCluster {
			cl = path.Cluster
		}
		var ev uint32 = 0xFFFFFFFF
		if path.HasEvent {
			ev = path.Event
		}
		records := log.Query(ep, cl, ev, minNumber)
		for _, rec := range records {
			if _, dup := seen[rec.Number]; dup {
				continue
			}
			seen[rec.Number] = struct{}{}
			out = append(out, EventReport{
				Path: ConcreteEventPath{
					Endpoint:    rec.Endpoint,
					HasEndpoint: true,
					Cluster:     rec.Cluster,
					HasCluster:  true,
					Event:       rec.EventID,
					HasEvent:    true,
				},
				Number:    rec.Number,
				Priority:  rec.Priority,
				Timestamp: rec.EpochMS,
				Data:      AttributeValue{Value: rec.Payload},
				IsStatus:  false,
			})
		}
	}
	return out
}

// HandleReadEventRequest evaluates the EventRequests in req against
// log and returns a slice of EventReports suitable for embedding into a
// ReportData.
//
// This is a thin wrapper around [BuildEventReports] that extracts the
// EventRequests and EventFilters from the supplied [ReadRequest]. The
// event-only core logic lives in [BuildEventReports]; this wrapper
// exists so call sites that already hold a [ReadRequest] (e.g. the
// read-opcode handler) do not need to unpack the path slice manually.
//
// Mirrors matter.js packages/protocol/src/action/server/EventReadResponse.ts
// (EventReadResponse.process) for the request-level dispatch and
// matter.js packages/protocol/src/interaction/EventHandler.ts (getEvents)
// for the per-path log query. Passes req.EventFilters to [BuildEventReports]
// for minimum-event-number gating.
func HandleReadEventRequest(req ReadRequest, log *EventLog) []EventReport {
	return BuildEventReports(req.EventRequests, log, req.EventFilters)
}

// matterAccessControlClusterID is the AccessControl cluster (Matter §9.10).
// Its events (AccessControlEntryChanged) are fabric-sensitive: each carries a
// FabricIndex, must never be disclosed across fabrics (§8.4.3.2 / §9.10.7.1),
// and requires Administer to read.
const matterAccessControlClusterID uint32 = 0x001F

// EventReadAuthorizer bundles the accessing subject + ACL checker used to gate
// event reads and subscriptions, mirroring the (fabricIndex, subject, CATs)
// tuple the attribute read path threads through [HandleReadRequest]. A zero
// FabricIndex marks a PASE / pre-commissioning session (ACL not yet
// applicable); a nil Checker disables enforcement (fail-open, matching the
// attribute path's "dispatcher without ACLChecker" fallback).
type EventReadAuthorizer struct {
	Checker       ACLChecker
	FabricIndex   uint8
	SubjectNodeID uint64
	SubjectCATs   []uint32
}

// AuthorizeEventReports filters events down to those the accessing subject may
// read and drops fabric-sensitive records that belong to another fabric.
// Denied paths/records are SILENTLY OMITTED — a wildcard event read discloses
// only authorized events (Matter §8.4.3.2), never an error status. A PASE
// session (FabricIndex==0) or a nil Checker returns every event unchanged.
//
// (a) ACL gate: each event is gated at its read privilege — View by default,
//
//	Administer for AccessControl (fabric-sensitive, Matter §9.10.7.1).
//	Mirrors matter.js packages/protocol/src/action/server/EventReadResponse.ts
//	#addConcrete/#addEventForWildcard (authorize at event.limits.readLevel).
//
// (b) Fabric-sensitive drop: a fabric-sensitive record is disclosed only to the
//
//	fabric that owns it, independent of the read's FabricFiltered flag. Fails
//	closed — a record whose fabric cannot be positively matched to the
//	accessing fabric is never disclosed. Mirrors EventReadResponse.ts
//	#readAllowedEvents (payload.fabricIndex !== accessingFabricIndex → skip).
func AuthorizeEventReports(ctx context.Context, auth EventReadAuthorizer, events []EventReport) []EventReport {
	if auth.FabricIndex == 0 || auth.Checker == nil {
		return append([]EventReport(nil), events...)
	}
	out := make([]EventReport, 0, len(events))
	for _, ev := range events {
		priv := eventReadPrivilege(ev.Path.Cluster)
		if status := auth.Checker.CheckACL(ctx, auth.FabricIndex, auth.SubjectNodeID, auth.SubjectCATs, ev.Path.Endpoint, ev.Path.Cluster, priv); !status.IsSuccess() {
			continue // subject not permitted to read this event — omit
		}
		if isFabricSensitiveEventCluster(ev.Path.Cluster) {
			recFabric, ok := eventPayloadFabricIndex(ev.Data.Value)
			if !ok || recFabric != auth.FabricIndex {
				continue // fabric-sensitive record owned by another fabric — omit
			}
		}
		out = append(out, ev)
	}
	return out
}

// eventReadPrivilege returns the minimum privilege required to read events on
// clusterID: Administer for AccessControl (§9.10.7.1 fabric-sensitive events),
// View for every other cluster (the Matter default event read privilege).
func eventReadPrivilege(clusterID uint32) uint8 {
	if clusterID == matterAccessControlClusterID {
		return 5 // Administer
	}
	return 1 // View
}

// isFabricSensitiveEventCluster reports whether clusterID's events carry a
// FabricIndex and must be filtered to the accessing fabric regardless of the
// read's FabricFiltered flag (Matter §8.4.3.2 / §9.10.7.1).
func isFabricSensitiveEventCluster(clusterID uint32) bool {
	return clusterID == matterAccessControlClusterID
}

// eventPayloadFabricIndex extracts the FabricIndex a fabric-sensitive event
// payload carries (e.g. AccessControl.AccessControlEntryChanged sets it per
// §9.10.7.1). Mirrors matter.js EventReadResponse.ts #readAllowedEvents
// `const fabricIndex = isObject(payload) ? payload.fabricIndex : undefined`:
// the payload is duck-typed for a FabricIndex field rather than matched to a
// concrete type (the IM layer stays cluster-blind and cannot import the
// cluster packages). Returns (0,false) when the payload is not a struct
// exposing an unsigned FabricIndex field.
func eventPayloadFabricIndex(payload any) (uint8, bool) {
	if payload == nil {
		return 0, false
	}
	v := reflect.ValueOf(payload)
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0, false
	}
	f := v.FieldByName("FabricIndex")
	if !f.IsValid() {
		return 0, false
	}
	switch f.Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return uint8(f.Uint() & 0xFF), true
	default:
		return 0, false
	}
}

// isGlobalAttributeID reports whether attrID is a universal global attribute
// (0xFFF8-0xFFFD): GeneratedCommandList, AcceptedCommandList, EventList,
// AttributeList, FeatureMap, ClusterRevision. These are legal on a
// wildcard-cluster read path; concrete non-global attributes are not.
func isGlobalAttributeID(attrID uint32) bool {
	return attrID >= 0xFFF8 && attrID <= 0xFFFD
}

// ValidateReadPaths enforces the Matter §8.4.3.2 path rules shared by Read and
// Subscribe: a wildcard cluster (HasCluster == false) combined with a concrete
// non-global attribute, or with a concrete event, is illegal — the whole action
// must be rejected up front with InvalidAction rather than silently expanded.
// Returns StatusInvalidAction on the first offending path, else StatusSuccess.
// Mirrors matter.js packages/node/src/node/server/InteractionServer.ts
// validateReadAttributesPath / validateReadEventPath (#3926). Callers (the read
// and subscribe dispatchers) run this before building any report.
func ValidateReadPaths(attrs []ConcreteAttributePath, events []ConcreteEventPath) StatusCode {
	for _, p := range attrs {
		if !p.HasCluster && p.HasAttribute && !isGlobalAttributeID(p.Attribute) {
			return StatusInvalidAction
		}
	}
	for _, p := range events {
		if !p.HasCluster && p.HasEvent {
			return StatusInvalidAction
		}
	}
	return StatusSuccess
}
